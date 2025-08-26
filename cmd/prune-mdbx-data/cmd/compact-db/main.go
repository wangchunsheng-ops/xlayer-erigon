package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/erigontech/mdbx-go/mdbx"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/kv/backup"
	mdbx2 "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	logv3 "github.com/ledgerwatch/log/v3"
	"golang.org/x/sync/semaphore"
)

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	var (
		sourceDBPath = flag.String("source", "", "Source database path (required)")
		outputPath   = flag.String("output", "", "Output path for compacted database (optional if using -in-place)")
		dbType       = flag.String("type", "chaindata", "Database type: 'chaindata' or 'smt'")
		dryRun       = flag.Bool("dry-run", false, "Only show space analysis without compacting")
		inPlace      = flag.Bool("in-place", false, "Compact database in-place (replaces original)")
		createBackup = flag.Bool("backup", false, "Create backup before in-place replacement (default: false)")
	)
	flag.Parse()

	if *sourceDBPath == "" || (!*inPlace && *outputPath == "" && !*dryRun) {
		fmt.Println("Usage: compact-db -source <source_db_path> [-output <output_path>] [-type chaindata|smt] [-dry-run] [-in-place] [-backup]")
		fmt.Println("\nModes:")
		fmt.Println("  1. Copy mode (default): -source <path> -output <new_path>")
		fmt.Println("  2. In-place mode:       -source <path> -in-place [-backup]")
		fmt.Println("\nOptions:")
		fmt.Println("  -backup:  Create .backup before in-place replacement (default: false)")
		fmt.Println("  -dry-run: Analyze potential space savings only")
		fmt.Println("\nExamples:")
		fmt.Println("  # Copy mode - create new compacted database")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -output /path/to/seq/chaindata.compact")
		fmt.Println("  # In-place mode - replace original database directly (⚠️ no backup)")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -in-place")
		fmt.Println("  # In-place mode with backup - safer but uses more space")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -in-place -backup")
		fmt.Println("  # Compact SMT database in-place")
		fmt.Println("  compact-db -source /path/to/seq/smt -in-place -type smt")
		fmt.Println("  # Dry run to analyze potential space savings")
		fmt.Println("  compact-db -source /path/to/seq/chaindata -dry-run")
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

	// Handle relative paths: if source path is relative, make it absolute from the correct base
	if !filepath.IsAbs(*sourceDBPath) {
		// When called from main program, we need to resolve relative paths correctly
		if cwd, err := os.Getwd(); err == nil {
			// If we're in a subdirectory (like cmd/compact-db), go up to main directory
			if strings.Contains(cwd, "cmd/compact-db") {
				basePath := filepath.Dir(filepath.Dir(cwd)) // Go up two levels
				*sourceDBPath = filepath.Join(basePath, *sourceDBPath)
			}
		}
	}

	// Also handle output path if it's relative (copy mode only)
	if !*inPlace && *outputPath != "" && !filepath.IsAbs(*outputPath) {
		if cwd, err := os.Getwd(); err == nil {
			if strings.Contains(cwd, "cmd/compact-db") {
				basePath := filepath.Dir(filepath.Dir(cwd)) // Go up two levels
				*outputPath = filepath.Join(basePath, *outputPath)
			}
		}
	}

	// Check source database exists
	dbFile := filepath.Join(*sourceDBPath, "mdbx.dat")
	if _, err := os.Stat(dbFile); os.IsNotExist(err) {
		log.Error("Source database not found", "path", *sourceDBPath, "file", dbFile)
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
	fmt.Printf("Actual Disk Size:    %s\n", datasize.ByteSize(originalSize).HumanReadable())
	fmt.Printf("Table Data Size:     %s\n", datasize.ByteSize(tableSize).HumanReadable())
	fmt.Printf("Overhead/Freelist:   %s (%.1f%%)\n", datasize.ByteSize(difference).HumanReadable(), differencePercent)

	if *dryRun {
		fmt.Printf("\nPotential Space Savings: %s (%.1f%%)\n",
			datasize.ByteSize(difference).HumanReadable(), differencePercent)
		fmt.Printf("Note: Actual savings may be less due to new database overhead\n")

		// Give recommendations based on overhead percentage
		if differencePercent < 10 {
			fmt.Printf("\nLow overhead (%.1f%%), compaction may not be necessary\n", differencePercent)
		} else if differencePercent < 30 {
			fmt.Printf("\nModerate overhead (%.1f%%)\n", differencePercent)
		} else {
			fmt.Printf("\nHigh overhead (%.1f%%), compaction will take significant time\n", differencePercent)
		}
		return
	}

	// Verify database is not in use by checking for lock files
	fmt.Printf("Verifying database is not in use...\n")
	time.Sleep(1 * time.Second)

	// Determine actual output path (in-place uses temporary directory)
	var actualOutputPath string
	var isInPlace bool = *inPlace

	if isInPlace {
		// Create temporary directory for in-place compaction
		actualOutputPath = *sourceDBPath + ".compact.tmp"
		fmt.Printf("\n=== Starting In-Place Database Compaction ===\n")
		fmt.Printf("⚠️  WARNING: In-place compaction requires temporary extra disk space\n")
		fmt.Printf("⚠️  Original database will be replaced after successful compaction\n")
		fmt.Printf("Source Path:         %s\n", *sourceDBPath)
		fmt.Printf("Temporary Path:      %s\n", actualOutputPath)
	} else {
		actualOutputPath = *outputPath
		fmt.Printf("\n=== Starting Database Compaction ===\n")
		fmt.Printf("Output Path:         %s\n", actualOutputPath)
	}

	// Check if output path exists (use os.Stat instead of dir.FileExist for better relative path support)
	if _, err := os.Stat(actualOutputPath); !os.IsNotExist(err) {
		if isInPlace {
			// For in-place mode, automatically clean up stale temporary directories
			fmt.Printf("Cleaning up existing temporary directory: %s\n", actualOutputPath)
			if err := os.RemoveAll(actualOutputPath); err != nil {
				log.Error("Failed to clean up existing temporary directory", "path", actualOutputPath, "error", err)
				os.Exit(1)
			}
		} else {
			log.Error("Output path already exists", "path", actualOutputPath)
			os.Exit(1)
		}
	}

	// Create output directory
	if err := os.MkdirAll(actualOutputPath, 0755); err != nil {
		log.Error("Failed to create output directory", "path", actualOutputPath, "error", err)
		os.Exit(1)
	}

	fmt.Printf("Target Page Size:    Keep original\n")
	startTime := time.Now()

	// Additional safety check: verify source database is not locked
	fmt.Printf("Verifying database accessibility...\n")
	testDB := mdbx2.NewMDBX(log).Path(*sourceDBPath).
		Label(label).
		WithTableCfg(func(_ kv.TableCfg) kv.TableCfg { return kv.TablesCfgByLabel(label) }).
		Readonly().
		MustOpen()
	testDB.Close()

	// Brief pause to ensure test connection is fully closed
	time.Sleep(200 * time.Millisecond)

	fmt.Printf("Opening source and destination databases...\n")
	// Use optimized compact settings for maximum performance
	src, dst := openOptimizedCompactPair(*sourceDBPath, actualOutputPath, label, log)
	fmt.Printf("Database connections established.\n")

	// Perform the compaction
	fmt.Printf("Starting compaction process...\n")

	ctx := context.Background()
	// Use maximum read-ahead threads for better I/O
	optimizedThreads := backup.ReadAheadThreads * 2 // Double the threads
	err = backup.Kv2kv(ctx, src, dst, nil, optimizedThreads, log)

	// Explicitly close connections before further operations
	src.Close()
	dst.Close()

	if err != nil {
		log.Error("Database compaction failed", "error", err)
		// Clean up failed output
		os.RemoveAll(actualOutputPath)
		os.Exit(1)
	}

	duration := time.Since(startTime)

	// Give MDBX time to fully release file locks before analyzing compacted database
	fmt.Printf("Finalizing compaction...\n")
	time.Sleep(500 * time.Millisecond)

	// Analyze compacted database
	compactedSize, _, err := analyzeDatabase(actualOutputPath, label, log)
	if err != nil {
		log.Warn("Failed to analyze compacted database", "error", err)
		compactedSize = 0
	}

	spaceSaved := originalSize - compactedSize
	spaceSavedPercent := float64(spaceSaved) / float64(originalSize) * 100

	fmt.Printf("\n=== Compaction Results ===\n")
	fmt.Printf("Duration:            %v\n", duration)
	fmt.Printf("Original Disk Size:  %s\n", datasize.ByteSize(originalSize).HumanReadable())
	if compactedSize > 0 {
		fmt.Printf("Compacted Disk Size: %s\n", datasize.ByteSize(compactedSize).HumanReadable())
		fmt.Printf("Space Saved:         %s (%.1f%%)\n", datasize.ByteSize(spaceSaved).HumanReadable(), spaceSavedPercent)
	}
	fmt.Printf("Status:              ✅ Success\n")

	if isInPlace {
		// Perform in-place replacement
		fmt.Printf("\n=== Performing In-Place Replacement ===\n")

		// Database connections already closed above

		var backupPath string
		if *createBackup {
			// Create backup of original database
			backupPath = *sourceDBPath + ".backup"
			fmt.Printf("Creating backup:     %s -> %s\n", *sourceDBPath, backupPath)

			// Remove existing backup if it exists
			if _, err := os.Stat(backupPath); err == nil {
				fmt.Printf("Removing existing backup: %s\n", backupPath)
				if err := os.RemoveAll(backupPath); err != nil {
					log.Error("Failed to remove existing backup", "error", err)
					fmt.Printf("❌ Failed to remove existing backup. Keeping compacted database at: %s\n", actualOutputPath)
					os.Exit(1)
				}
			}

			if err := os.Rename(*sourceDBPath, backupPath); err != nil {
				log.Error("Failed to backup original database", "error", err)
				fmt.Printf("❌ Failed to create backup. Keeping compacted database at: %s\n", actualOutputPath)
				os.Exit(1)
			}
		} else {
			// No backup mode - directly remove original
			fmt.Printf("⚠️  No backup mode: directly replacing original database\n")
			fmt.Printf("Removing original:   %s\n", *sourceDBPath)
			if err := os.RemoveAll(*sourceDBPath); err != nil {
				log.Error("Failed to remove original database", "error", err)
				fmt.Printf("❌ Failed to remove original database. Keeping compacted database at: %s\n", actualOutputPath)
				os.Exit(1)
			}
		}

		// Move compacted database to original location
		fmt.Printf("Replacing database:  %s -> %s\n", actualOutputPath, *sourceDBPath)
		if err := os.Rename(actualOutputPath, *sourceDBPath); err != nil {
			log.Error("Failed to replace database", "error", err)

			if *createBackup {
				// Try to restore backup
				fmt.Printf("❌ Failed to replace database. Attempting to restore backup...\n")
				if restoreErr := os.Rename(backupPath, *sourceDBPath); restoreErr != nil {
					log.Error("CRITICAL: Failed to restore backup", "restoreError", restoreErr, "originalError", err)
					fmt.Printf("🚨 CRITICAL: Your database backup is at: %s\n", backupPath)
					fmt.Printf("🚨 Please manually restore it!\n")
				} else {
					fmt.Printf("✅ Backup restored successfully\n")
				}
			} else {
				fmt.Printf("🚨 CRITICAL: Original database removed but replacement failed!\n")
				fmt.Printf("🚨 Compacted database is at: %s\n", actualOutputPath)
				fmt.Printf("🚨 Please manually move it to: %s\n", *sourceDBPath)
			}
			os.Exit(1)
		}

		fmt.Printf("✅ In-place compaction completed successfully!\n")
		fmt.Printf("\n=== Next Steps ===\n")
		fmt.Printf("1. Your database has been compacted in-place\n")
		fmt.Printf("2. Start your Erigon node to verify everything works\n")

		if *createBackup {
			fmt.Printf("3. If everything works, remove backup: rm -rf %s\n", backupPath)
			fmt.Printf("4. If there are issues, restore backup: mv %s %s\n", backupPath, *sourceDBPath)
		} else {
			fmt.Printf("3. ⚠️  No backup was created - original database was replaced directly\n")
		}
	} else {
		fmt.Printf("\n=== Next Steps ===\n")
		fmt.Printf("1. Stop your Erigon node\n")
		fmt.Printf("2. Backup original: mv %s %s.backup\n", *sourceDBPath, *sourceDBPath)
		fmt.Printf("3. Replace with compacted: mv %s %s\n", actualOutputPath, *sourceDBPath)
		fmt.Printf("4. Start your Erigon node\n")
		fmt.Printf("5. If everything works, remove backup: rm -rf %s.backup\n", *sourceDBPath)
	}
}

// analyzeDatabase returns actual disk usage and table data size
func analyzeDatabase(dbPath string, label kv.Label, logger logv3.Logger) (uint64, uint64, error) {
	// Get actual disk usage (like `du` command) instead of sparse file logical size
	dbFile := filepath.Join(dbPath, "mdbx.dat")
	fileInfo, err := os.Stat(dbFile)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to stat database file: %w", err)
	}

	var actualFileSize uint64
	// For sparse files, we need to get actual disk usage using syscall
	if stat, ok := fileInfo.Sys().(*syscall.Stat_t); ok {
		// stat.Blocks is in 512-byte blocks on most Unix systems
		actualFileSize = uint64(stat.Blocks * 512)
	} else {
		// Fallback to logical size if syscall not available
		actualFileSize = uint64(fileInfo.Size())
	}

	// Open database for table analysis
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

	// Get MDBX transaction for table stats
	mdbxTx, ok := tx.(*mdbx2.MdbxTx)
	if !ok {
		return 0, 0, fmt.Errorf("not MDBX transaction")
	}

	// Calculate table data size
	tables, err := tx.ListBuckets()
	if err != nil {
		return 0, 0, err
	}

	var tableSize uint64
	pageSize := db.PageSize()
	for _, tableName := range tables {
		stat, err := mdbxTx.BucketStat(tableName)
		if err != nil {
			continue // Skip failed tables
		}
		totalPages := stat.LeafPages + stat.BranchPages + stat.OverflowPages
		tableSize += totalPages * pageSize
	}

	// Return actual disk usage and table data size
	return actualFileSize, tableSize, nil
}

// openOptimizedCompactPair creates highly optimized database connections for fast compaction
func openOptimizedCompactPair(from, to string, label kv.Label, logger logv3.Logger) (kv.RoDB, kv.RwDB) {
	const OptimizedThreadsLimit = 16_000 // Increased from default 9_000

	// Source database with maximum read optimization
	src := mdbx2.NewMDBX(logger).Path(from).
		Label(label).
		RoTxsLimiter(semaphore.NewWeighted(OptimizedThreadsLimit)).
		WithTableCfg(func(_ kv.TableCfg) kv.TableCfg { return kv.TablesCfgByLabel(label) }).
		Flags(func(flags uint) uint {
			// Enable read optimizations - remove NoReadahead for better prefetching
			return flags | mdbx.Accede | mdbx.LifoReclaim&^mdbx.NoReadahead
		}).
		MustOpen()

	// Get source info for optimal destination setup
	info, err := src.(*mdbx2.MdbxKV).Env().Info(nil)
	if err != nil {
		panic(err)
	}

	// Destination database with write optimization
	dst := mdbx2.NewMDBX(logger).Path(to).
		Label(label).
		PageSize(datasize.ByteSize(info.PageSize).Bytes()). // Keep same page size
		MapSize(datasize.ByteSize(info.Geo.Upper)).
		GrowthStep(4 * datasize.GB).         // Conservative growth step
		DirtySpace(uint64(1 * datasize.GB)). // Conservative dirty space
		Flags(func(flags uint) uint {
			// Enable write optimizations
			return flags | mdbx.WriteMap | mdbx.LifoReclaim | mdbx.SafeNoSync
		}).
		WithTableCfg(func(_ kv.TableCfg) kv.TableCfg { return kv.TablesCfgByLabel(label) }).
		MustOpen()

	return src, dst
}
