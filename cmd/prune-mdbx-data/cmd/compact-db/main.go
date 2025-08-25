package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/ledgerwatch/erigon-lib/common/dir"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/kv/backup"
	mdbx2 "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	logv3 "github.com/ledgerwatch/log/v3"
)

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	var (
		sourceDBPath = flag.String("source", "", "Source database path (required)")
		outputPath   = flag.String("output", "", "Output path for compacted database (required)")
		dbType       = flag.String("type", "chaindata", "Database type: 'chaindata' or 'smt'")
		dryRun       = flag.Bool("dry-run", false, "Only show space analysis without compacting")
	)
	flag.Parse()

	if *sourceDBPath == "" || *outputPath == "" {
		fmt.Println("Usage: compact-db -source <source_db_path> -output <output_path> [-type chaindata|smt] [-dry-run]")
		fmt.Println("\nExamples:")
		fmt.Println("  # Compact chaindata database")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -output /path/to/seq/chaindata.compact")
		fmt.Println("  # Compact SMT database")
		fmt.Println("  compact-db -source /path/to/seq/smt -output /path/to/seq/smt.compact -type smt")
		fmt.Println("  # Dry run to analyze potential space savings")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -output /tmp/compact -dry-run")
		os.Exit(1)
	}

	// Determine database label
	var label kv.Label
	switch *dbType {
	case "chaindata":
		label = kv.ChainDB
	case "smt":
		label = kv.SmtDB
	default:
		log.Error("Invalid database type", "type", *dbType, "expected", "chaindata or smt")
		os.Exit(1)
	}

	// Check source database exists
	if !dir.FileExist(filepath.Join(*sourceDBPath, "mdbx.dat")) {
		log.Error("Source database not found", "path", *sourceDBPath)
		os.Exit(1)
	}

	// Analyze space usage before compaction
	originalSize, tableSize, err := analyzeDatabase(*sourceDBPath, label, log)
	if err != nil {
		log.Error("Failed to analyze source database", "error", err)
		os.Exit(1)
	}

	difference := originalSize - tableSize
	differencePercent := float64(difference) / float64(originalSize) * 100

	fmt.Printf("\n=== Database Analysis ===\n")
	fmt.Printf("Database Type:       %s\n", *dbType)
	fmt.Printf("Source Path:         %s\n", *sourceDBPath)
	fmt.Printf("Original Size:       %s\n", datasize.ByteSize(originalSize).HumanReadable())
	fmt.Printf("Table Data Size:     %s\n", datasize.ByteSize(tableSize).HumanReadable())
	fmt.Printf("Overhead/Freelist:   %s (%.1f%%)\n", datasize.ByteSize(difference).HumanReadable(), differencePercent)

	if *dryRun {
		fmt.Printf("\nPotential Space Savings: %s (%.1f%%)\n",
			datasize.ByteSize(difference).HumanReadable(), differencePercent)
		fmt.Printf("Note: Actual savings may be less due to new database overhead\n")
		return
	}

	// Check if output path exists
	if dir.FileExist(*outputPath) {
		log.Error("Output path already exists", "path", *outputPath)
		os.Exit(1)
	}

	// Create output directory
	if err := os.MkdirAll(*outputPath, 0755); err != nil {
		log.Error("Failed to create output directory", "path", *outputPath, "error", err)
		os.Exit(1)
	}

	fmt.Printf("\n=== Starting Database Compaction ===\n")
	fmt.Printf("Output Path:         %s\n", *outputPath)
	fmt.Printf("Target Page Size:    Keep original\n")

	startTime := time.Now()

	// Open source and destination databases
	src, dst := backup.OpenPair(*sourceDBPath, *outputPath, label, 0, log)
	defer src.Close()
	defer dst.Close()

	// Perform the compaction
	ctx := context.Background()
	if err := backup.Kv2kv(ctx, src, dst, nil, backup.ReadAheadThreads, log); err != nil {
		log.Error("Database compaction failed", "error", err)
		// Clean up failed output
		os.RemoveAll(*outputPath)
		os.Exit(1)
	}

	duration := time.Since(startTime)

	// Analyze compacted database
	compactedSize, _, err := analyzeDatabase(*outputPath, label, log)
	if err != nil {
		log.Warn("Failed to analyze compacted database", "error", err)
		compactedSize = 0
	}

	spaceSaved := originalSize - compactedSize
	spaceSavedPercent := float64(spaceSaved) / float64(originalSize) * 100

	fmt.Printf("\n=== Compaction Results ===\n")
	fmt.Printf("Duration:            %v\n", duration)
	fmt.Printf("Original Size:       %s\n", datasize.ByteSize(originalSize).HumanReadable())
	if compactedSize > 0 {
		fmt.Printf("Compacted Size:      %s\n", datasize.ByteSize(compactedSize).HumanReadable())
		fmt.Printf("Space Saved:         %s (%.1f%%)\n", datasize.ByteSize(spaceSaved).HumanReadable(), spaceSavedPercent)
	}
	fmt.Printf("Status:              ✅ Success\n")

	fmt.Printf("\n=== Next Steps ===\n")
	fmt.Printf("1. Stop your Erigon node\n")
	fmt.Printf("2. Backup original: mv %s %s.backup\n", *sourceDBPath, *sourceDBPath)
	fmt.Printf("3. Replace with compacted: mv %s %s\n", *outputPath, *sourceDBPath)
	fmt.Printf("4. Start your Erigon node\n")
	fmt.Printf("5. If everything works, remove backup: rm -rf %s.backup\n", *sourceDBPath)
}

// analyzeDatabase returns total database size and table data size
func analyzeDatabase(dbPath string, label kv.Label, logger logv3.Logger) (uint64, uint64, error) {
	// Open database for analysis
	db := mdbx2.NewMDBX(logger).Path(dbPath).
		Label(label).
		WithTableCfg(func(_ kv.TableCfg) kv.TableCfg { return kv.TablesCfgByLabel(label) }).
		Readonly().
		MustOpen()
	defer db.Close()

	ctx := context.Background()
	tx, err := db.BeginRo(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// Get actual database size
	if mdbxTx, ok := tx.(*mdbx2.MdbxTx); ok {
		totalSize, err := mdbxTx.DBSize()
		if err != nil {
			return 0, 0, err
		}

		// Calculate table data size
		var tableSize uint64
		tables, err := tx.ListBuckets()
		if err != nil {
			return 0, 0, err
		}

		pageSize := db.PageSize()
		for _, tableName := range tables {
			stat, err := mdbxTx.BucketStat(tableName)
			if err != nil {
				continue // Skip failed tables
			}
			totalPages := stat.LeafPages + stat.BranchPages + stat.OverflowPages
			tableSize += totalPages * pageSize
		}

		return totalSize, tableSize, nil
	}

	return 0, 0, fmt.Errorf("not MDBX transaction")
}
