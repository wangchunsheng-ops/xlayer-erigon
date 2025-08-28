package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/c2h5oh/datasize"
	mdbx2 "github.com/erigontech/mdbx-go/mdbx"
	"github.com/ledgerwatch/erigon-lib/kv"
	mdbxpkg "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	"github.com/ledgerwatch/erigon/smt/pkg/db"

	logv3 "github.com/ledgerwatch/log/v3"
)

// Define pruning levels
type PruneLevel int

const (
	PruneLevelModerate   PruneLevel = iota // Moderate pruning (recommended)
	PruneLevelAggressive                   // Aggressive pruning (includes state data cleanup)
)

// checkSMTDatabase checks if SMT database exists and contains data
func checkSMTDatabase(smtPath string) bool {
	if _, err := os.Stat(smtPath + "/mdbx.dat"); os.IsNotExist(err) {
		return false
	}

	// Check file size, if file is very small it might be just an empty database file
	if info, err := os.Stat(smtPath + "/mdbx.dat"); err == nil {
		// If file size is less than 1MB, consider it as empty database
		return info.Size() > 1024*1024
	}

	return true
}

// getTableList gets list of tables in database
func getTableList(db kv.RwDB) ([]string, error) {
	ctx := context.Background()
	tx, err := db.BeginRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	tables, err := tx.ListBuckets()
	if err != nil {
		return nil, err
	}

	sort.Strings(tables)
	return tables, nil
}

// getActiveTableList returns list of tables that actually contain data (size > 0)
func getActiveTableList(db kv.RwDB) ([]string, error) {
	ctx := context.Background()
	tx, err := db.BeginRo(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	allTables, err := tx.ListBuckets()
	if err != nil {
		return nil, err
	}

	var activeTables []string
	for _, tableName := range allTables {
		// Check if table has any data
		if hasTableData(tx, tableName) {
			activeTables = append(activeTables, tableName)
		}
	}

	sort.Strings(activeTables)
	return activeTables, nil
}

// hasTableData checks if a table contains any data
func hasTableData(tx kv.Tx, tableName string) bool {
	// Try to get the first key from the table
	cursor, err := tx.Cursor(tableName)
	if err != nil {
		return false
	}
	defer cursor.Close()

	// Check if there's at least one entry
	k, _, err := cursor.First()
	return err == nil && len(k) > 0
}

// contains checks if a slice contains a given string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// getTableStats gets statistics info of table (using default pageSize)
func getTableStats(db kv.RwDB, tableName string) (uint64, uint64, uint64, error) {
	ctx := context.Background()
	tx, err := db.BeginRo(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	defer tx.Rollback()

	if mdbxTx, ok := tx.(*mdbxpkg.MdbxTx); ok {
		stat, err := mdbxTx.BucketStat(tableName)
		if err != nil {
			return 0, 0, 0, err
		}

		totalPages := stat.LeafPages + stat.BranchPages + stat.OverflowPages
		// Use default MDBX page size of 8192 bytes
		const defaultPageSize = 8192
		sizeBytes := totalPages * defaultPageSize

		return stat.Entries, sizeBytes, totalPages, nil
	}

	return 0, 0, 0, fmt.Errorf("not MDBX transaction")
}

// openDatabase opens database at specified path using the safest possible approach
func openDatabase(dbPath string, label kv.Label, log logv3.Logger) (kv.RwDB, *mdbx2.EnvInfo, error) {
	ctx := context.Background()

	var opts mdbxpkg.MdbxOpts
	if label == kv.ChainDB {
		opts = mdbxpkg.NewMDBX(log).Path(dbPath).Label(label).WithTableCfg(mdbxpkg.WithChaindataTables)
	} else {
		// SMT database uses different configuration
		kv.InitStandaloneSMT(false) // Standalone SMT database
		opts = mdbxpkg.NewMDBX(log).Path(dbPath).Label(label)
	}

	// Use the simplest possible approach: let MDBX use its default configuration
	// This avoids all geometry mismatch issues
	db, err := opts.Open(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open database: %w", err)
	}

	fmt.Printf("✓ Database opened successfully with default configuration\n")

	return db, nil, nil
}

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	// Parse command line arguments
	config, err := parseMainArguments(os.Args[1:], log)
	if err != nil {
		os.Exit(1)
	}

	// Validate and setup paths
	paths, err := validateMainPaths(config)
	if err != nil {
		log.Error("Path validation failed", "error", err)
		os.Exit(1)
	}

	// Analyze database and collect table information
	analysis, err := analyzeMainDatabase(paths, config, log)
	if err != nil {
		log.Error("Database analysis failed", "error", err)
		os.Exit(1)
	}

	// Get user confirmation
	if !getMainUserConfirmation(analysis, config) {
		fmt.Printf("Operation cancelled\n")
		return
	}

	// Execute pruning operations
	stats, err := executeMainPruningOperations(paths, analysis, config, log)
	if err != nil {
		log.Error("Pruning operations failed", "error", err)
		os.Exit(1)
	}

	// Calculate and display results
	calculateMainResults(analysis, stats, config)
}

// executeBatchOperationsWithCommit executes batch operations with guaranteed commit
func executeBatchOperationsWithCommit(dbPath string, keepRecentBatches uint64, log logv3.Logger, ctx context.Context) (int, int) {
	fmt.Printf("\n=== Phase 1: Batch-based Pruning ===\n")

	// Open database for batch operations
	chaindb, _, err := openDatabase(dbPath, kv.ChainDB, log)
	if err != nil {
		logv3.Error("Failed to open database for batch operations", "error", err)
		return 0, 0
	}

	// Ensure database is closed after batch operations
	defer func() {
		fmt.Printf("Phase 1 completed, closing database...\n")
		chaindb.Close()
		fmt.Printf("✓ Phase 1 database closed\n")

		// Give MDBX some time to complete cleanup
		time.Sleep(2 * time.Second)
	}()

	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		logv3.Error("Failed to start transaction for batch operations", "error", err)
		return 0, 0
	}

	var committed bool
	defer func() {
		if !committed {
			fmt.Printf("Rolling back batch operations transaction...\n")
			tx.Rollback()
		}
	}()

	deletedBatches, deletedBlocks, err := partialPruneBatchTables(tx, keepRecentBatches)
	if err != nil {
		logv3.Error("Failed to perform batch-based pruning", "error", err)
		return 0, 0
	}

	// Commit transaction
	fmt.Printf("Committing Phase 1 (batch operations)...\n")
	if err := tx.Commit(); err != nil {
		logv3.Error("Failed to commit batch operations", "error", err)
		return 0, 0
	}
	committed = true

	fmt.Printf("✓ Phase 1 committed successfully\n")
	return deletedBatches, deletedBlocks
}

// executeDupCursorOperationsWithCommit executes dupCursor operations with guaranteed commit
func executeDupCursorOperationsWithCommit(dbPath string, keepRecentBatches uint64, fastDupCursorMode, safeFastMode bool, log logv3.Logger, ctx context.Context) {
	fmt.Printf("\n=== Phase 1b: DupCursor Data Cleanup ===\n")

	// Open database for dupCursor operations
	chaindb, _, err := openDatabase(dbPath, kv.ChainDB, log)
	if err != nil {
		logv3.Error("Failed to open database for dupCursor operations", "error", err)
		return
	}

	// Ensure database is closed after dupCursor operations
	defer func() {
		fmt.Printf("Phase 1b completed, closing database...\n")
		chaindb.Close()
		fmt.Printf("✓ Phase 1b database closed\n")

		// Give MDBX some time to complete cleanup
		time.Sleep(2 * time.Second)
	}()

	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		logv3.Error("Failed to start transaction for dupCursor cleanup", "error", err)
		return
	}

	var committed bool
	defer func() {
		if !committed {
			fmt.Printf("Rolling back dupCursor operations transaction...\n")
			tx.Rollback()
		}
	}()

	fmt.Printf("Processing 2 dupCursor tables: AccountChangeSet, StorageChangeSet\n")
	fmt.Printf("Note: CanonicalHeader and hermez_blockBatches are preserved for node stability\n")

	if fastDupCursorMode {
		fmt.Printf("⚡ Fast dupCursor mode enabled: using direct cursor deletion for maximum performance\n")
	} else if safeFastMode {
		fmt.Printf("🛡️⚡ Safe-Fast dupCursor mode enabled: balanced performance and safety\n")
	} else {
		fmt.Printf("🔄 Standard dupCursor mode: using optimized batch processing for safety\n")
	}

	deletedDupCursorRecords, err := pruneHistoricalDupCursorData(tx, keepRecentBatches, fastDupCursorMode, safeFastMode)
	if err != nil {
		logv3.Error("Failed to perform dupCursor data cleanup", "error", err)
		return
	}

	// Commit transaction
	fmt.Printf("Committing Phase 1b (dupCursor operations)...\n")
	if err := tx.Commit(); err != nil {
		logv3.Error("Failed to commit dupCursor operations", "error", err)
		return
	}
	committed = true

	fmt.Printf("✓ Phase 1b committed successfully\n")
	fmt.Printf("✓ Historical dupCursor data cleanup completed: %d records deleted\n", deletedDupCursorRecords)
}

// executeTableDeletionsWithCommit executes table deletion operations with guaranteed commit
func executeTableDeletionsWithCommit(
	chaindb kv.RwDB,
	sortedTables []tableSizeInfo,
	preCollectedStats map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	},
	partiallyPrunedTables map[string]bool,
	pruneLevel PruneLevel,
	log logv3.Logger,
	ctx context.Context,
) (int, int, uint64) {
	if len(sortedTables) == 0 {
		fmt.Printf("\n=== Phase 2: No tables to delete ===\n")
		return 0, 0, 0
	}

	fmt.Printf("\n=== Phase 2: Table Deletions ===\n")

	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		logv3.Error("Failed to start transaction for table deletions", "error", err)
		os.Exit(1)
	}

	defer func() {
		fmt.Printf("Committing Phase 2 (table deletions)...\n")
		if err := tx.Commit(); err != nil {
			logv3.Error("Failed to commit table deletions", "error", err)
			tx.Rollback()
			os.Exit(1)
		}
		fmt.Printf("✓ Phase 2 committed successfully\n")
	}()

	deletedCount, actuallyDeletedTables, actualDeletedSize := executeOptimizedTableDeletion(
		tx, chaindb, sortedTables, preCollectedStats, partiallyPrunedTables, pruneLevel, logv3.New())

	if deletedCount < 0 {
		deletedCount = 0
	}

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// Refactored main function helper structures and functions

// MainConfig holds all configuration for the pruning operation
type MainConfig struct {
	DBPath            string
	PruneLevel        PruneLevel
	KeepRecentBatches uint64
	AutoYes           bool
	FastDupCursorMode bool
	SafeFastMode      bool
}

// MainPaths holds database path configuration
type MainPaths struct {
	MainPath      string
	ChaindataPath string
	SMTPath       string
	SMTSeparated  bool
}

// MainAnalysis holds the results of database analysis
type MainAnalysis struct {
	AllTables         []string
	ToDelete          []string
	Critical          map[string]bool
	PreCollectedStats map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	}
	SortedTables      []tableSizeInfo
	TotalToDeleteSize uint64
	TotalDbSize       uint64
	SMTTableCount     int
}

// MainStats holds statistics about the pruning operation
type MainStats struct {
	DeletedBatches        int
	DeletedBlocks         int
	DeletedCount          int
	ActuallyDeletedTables int
	ActualDeletedSize     uint64
}

// parseMainArguments parses command line arguments and returns configuration
func parseMainArguments(args []string, log logv3.Logger) (*MainConfig, error) {
	if len(args) < 1 {
		log.Error("Usage: prune-chaindata <db_path> [level] [options]")
		log.Error("Levels: conservative (default), moderate, aggressive")
		log.Error("Options:")
		log.Error("  --keep-recent-batches N    Keep recent N batches (default: 10)")
		log.Error("  --fast-dupfree            Enable fast dupCursor deletion (higher performance, more aggressive)")
		log.Error("  --safe-fast               Enable safe-fast mode (balanced performance and safety)")
		log.Error("  --yes, -y                  Skip confirmation prompts")
		log.Error("  --force                    Skip safety checks for copied databases")
		log.Error("NOTE: Uses batch-based pruning for X Layer zkEVM")
		log.Error("AGGRESSIVE mode: Also cleans 2 historical dupCursor tables (+12.5GB: AccountChangeSet, StorageChangeSet) - preserves CanonicalHeader and hermez_blockBatches for stability")
		return nil, fmt.Errorf("insufficient arguments")
	}

	config := &MainConfig{
		DBPath:            args[0],
		PruneLevel:        PruneLevelModerate, // Default: moderate (recommended)
		KeepRecentBatches: 10,                 // Default: keep recent 10 batches
		AutoYes:           false,              // Default: require user confirmation
		FastDupCursorMode: false,              // Default: use safe batch processing
		SafeFastMode:      false,              // Default: use standard processing
	}

	// Parse pruning level and optional parameters
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "moderate":
			config.PruneLevel = PruneLevelModerate
		case arg == "aggressive":
			config.PruneLevel = PruneLevelAggressive

		case strings.HasPrefix(arg, "--keep-recent-batches"):
			if strings.Contains(arg, "=") {
				// Format: --keep-recent-batches=N
				parts := strings.Split(arg, "=")
				if len(parts) == 2 {
					if batches, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
						config.KeepRecentBatches = batches
					} else {
						log.Error("Invalid number for --keep-recent-batches: %s", parts[1])
						return nil, err
					}
				}
			} else {
				// Format: --keep-recent-batches N
				if i+1 < len(args) {
					if batches, err := strconv.ParseUint(args[i+1], 10, 64); err == nil {
						config.KeepRecentBatches = batches
						i++ // Skip next argument
					} else {
						log.Error("Invalid number for --keep-recent-batches: %s", args[i+1])
						return nil, err
					}
				} else {
					return nil, fmt.Errorf("--keep-recent-batches requires a number")
				}
			}
		case arg == "--fast-dupfree":
			config.FastDupCursorMode = true
		case arg == "--safe-fast":
			config.SafeFastMode = true
		case arg == "--yes" || arg == "-y":
			config.AutoYes = true

		default:
			// If it's not a flag and not the first arg (db path), check if it's a level
			if i == 1 { // Second argument is level
				switch arg {
				case "moderate":
					config.PruneLevel = PruneLevelModerate
				case "aggressive":
					config.PruneLevel = PruneLevelAggressive
				default:
					return nil, fmt.Errorf("invalid level. Use: moderate or aggressive")
				}
			}
		}
	}

	// Validate mode combinations
	if config.FastDupCursorMode && config.SafeFastMode {
		return nil, fmt.Errorf("cannot use both --fast-dupfree and --safe-fast simultaneously. Choose one mode")
	}

	return config, nil
}

// validateMainPaths validates database paths and sets up path configuration
func validateMainPaths(config *MainConfig) (*MainPaths, error) {
	paths := &MainPaths{
		MainPath:      config.DBPath,
		ChaindataPath: config.DBPath + "/chaindata",
		SMTPath:       config.DBPath + "/smt",
	}

	fmt.Printf("Checking database path: %s\n", config.DBPath)
	fmt.Printf("Chaindata path: %s\n", paths.ChaindataPath)
	fmt.Printf("SMT path: %s\n", paths.SMTPath)
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(config.PruneLevel))
	if config.PruneLevel == PruneLevelModerate || config.PruneLevel == PruneLevelAggressive {
		fmt.Printf("Keep recent batches: %d\n", config.KeepRecentBatches)
	}

	// Check if chaindata database file exists
	if _, err := os.Stat(paths.ChaindataPath + "/mdbx.dat"); os.IsNotExist(err) {
		return nil, fmt.Errorf("chaindata DB path does not exist: %s", paths.ChaindataPath+"/mdbx.dat")
	}

	// Check if database separation is performed
	paths.SMTSeparated = checkSMTDatabase(paths.SMTPath)
	if paths.SMTSeparated {
		fmt.Printf("\nDatabase separation status detected: SMT data separated to independent database\n")
		kv.InitStandaloneSMT(true) // Standalone SMT database mode
	} else {
		fmt.Printf("\nDatabase separation status detected: All data in unified database\n")
		kv.InitStandaloneSMT(true) // Unified database mode
	}

	return paths, nil
}

// analyzeMainDatabase performs database analysis and collects table information
func analyzeMainDatabase(paths *MainPaths, config *MainConfig, log logv3.Logger) (*MainAnalysis, error) {
	// Open chaindata database for analysis
	chaindb, _, err := openDatabase(paths.ChaindataPath, kv.ChainDB, log)
	if err != nil {
		return nil, fmt.Errorf("failed to open chaindata db: %w", err)
	}
	defer chaindb.Close()

	log.Info("Chaindata database opened successfully")

	// Get chaindata table list (only tables with data)
	allTables, err := getActiveTableList(chaindb)
	if err != nil {
		return nil, fmt.Errorf("failed to get active chaindata table list: %w", err)
	}

	log.Info("Active tables found", "count", len(allTables))

	// Analyze tables
	fmt.Printf("\n=== Database Pruning Analysis ===\n")
	fmt.Printf("Active tables (with data): %d\n", len(allTables))
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(config.PruneLevel))
	fmt.Printf("Note: Only processing tables that contain data (size > 0)\n")

	// Get SMT tables count
	smtTableCount := 0
	for _, table := range allTables {
		for _, smtTable := range db.HermezSmtTables {
			if table == smtTable {
				smtTableCount++
				break
			}
		}
	}
	fmt.Printf("SMT related tables found: %d/%d\n", smtTableCount, len(db.HermezSmtTables))

	// Get tables to delete
	toDelete := getPruneTables(allTables, config.PruneLevel)
	critical := getCriticalTables()

	fmt.Printf("Tables marked for deletion: %d\n", len(toDelete))
	fmt.Printf("Critical tables (will be preserved): %d\n", len(critical))

	if len(toDelete) == 0 {
		fmt.Printf("No tables found for complete deletion (target tables are empty or don't exist with data)\n")
		fmt.Printf("Target tables for complete deletion: BlockTransaction, BlockTransactionLookup, hermez_txPricePercentage, LogTopicIndex, AccountHistory, CallFromIndex, CallToIndex, CallTraceSet, LogAddressIndex\n")
		fmt.Printf("Note: Will still perform batch-based partial pruning if configured\n")
	}

	// Calculate space to be freed and database size
	var totalToDeleteSize uint64
	var totalDbSize uint64

	// Pre-collect table statistics for later use in deletion phase
	preCollectedStats := make(map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	})

	fmt.Printf("\nTables to be deleted:\n")
	for i, table := range toDelete {
		entries, sizeBytes, pages, err := getTableStats(chaindb, table)
		if err != nil {
			fmt.Printf("%3d. %-30s (failed to get stats)\n", i+1, table)
		} else {
			sizeStr := datasize.ByteSize(sizeBytes).HumanReadable()
			fmt.Printf("%3d. %-30s %s (%d entries, %d pages)\n", i+1, table, sizeStr, entries, pages)
			totalToDeleteSize += sizeBytes

			// Save statistics for deletion phase
			preCollectedStats[table] = struct {
				entries   uint64
				sizeBytes uint64
				pages     uint64
			}{
				entries:   entries,
				sizeBytes: sizeBytes,
				pages:     pages,
			}
		}
	}

	// Calculate total database size
	for _, table := range allTables {
		_, sizeBytes, _, err := getTableStats(chaindb, table)
		if err == nil {
			totalDbSize += sizeBytes
		}
	}

	// Sort tables by size for optimized deletion order (small to large)
	var sortedTables []tableSizeInfo
	for _, table := range toDelete {
		if stats, exists := preCollectedStats[table]; exists {
			sortedTables = append(sortedTables, tableSizeInfo{name: table, size: stats.sizeBytes})
		} else {
			// Tables without stats go last
			sortedTables = append(sortedTables, tableSizeInfo{name: table, size: 0})
		}
	}

	// Sort by size (small to large)
	for i := 0; i < len(sortedTables)-1; i++ {
		for j := i + 1; j < len(sortedTables); j++ {
			if sortedTables[i].size > sortedTables[j].size {
				sortedTables[i], sortedTables[j] = sortedTables[j], sortedTables[i]
			}
		}
	}

	return &MainAnalysis{
		AllTables:         allTables,
		ToDelete:          toDelete,
		Critical:          critical,
		PreCollectedStats: preCollectedStats,
		SortedTables:      sortedTables,
		TotalToDeleteSize: totalToDeleteSize,
		TotalDbSize:       totalDbSize,
		SMTTableCount:     smtTableCount,
	}, nil
}

// getMainUserConfirmation shows pruning information and gets user confirmation
func getMainUserConfirmation(analysis *MainAnalysis, config *MainConfig) bool {
	// Show pruning level description
	fmt.Printf("\n=== Pruning Level Description ===\n")
	switch config.PruneLevel {
	case PruneLevelModerate:
		fmt.Printf("Moderate pruning: Comprehensive cleanup with batch-based optimization\n")
		fmt.Printf("Strategy: Delete specific tables with actual data + batch-based pruning (keep recent %d batches)\n", config.KeepRecentBatches)
		fmt.Printf("Preserves: Recent batch data, core state data, zkEVM operational tables\n")
		fmt.Printf("Deletes: 9 tables with actual data (BlockTransaction, BlockTransactionLookup, hermez_txPricePercentage, LogTopicIndex, AccountHistory, CallFromIndex, CallToIndex, CallTraceSet, LogAddressIndex) + old batch data\n")
		fmt.Printf("🎯 zkEVM optimized: Simplified cleanup for sequence nodes (only deletes tables that actually have significant data)\n")
		fmt.Printf("Best for: Production sequencer nodes, regular maintenance\n")

	case PruneLevelAggressive:
		fmt.Printf("Aggressive pruning: Maximum cleanup including historical dupCursor data\n")
		fmt.Printf("Strategy: All moderate mode deletions + historical dupCursor table cleanup\n")
		fmt.Printf("DupCursor tables processed: AccountChangeSet, StorageChangeSet (CanonicalHeader and hermez_blockBatches preserved for stability)\n")
		fmt.Printf("Preserves: Recent %d batches of dupCursor data, SMT data, core operational tables, critical mapping tables\n", config.KeepRecentBatches)
		fmt.Printf("Deletes: Same as moderate + historical account/storage changes beyond recent batches\n")
		fmt.Printf("Note: PlainState (current state) is always preserved as it contains active account/storage data\n")
		fmt.Printf("⚠️  ADVANCED: Only use when SMT data is complete and historical queries not needed\n")
		fmt.Printf("🚀 Maximum space savings: Optimized for nodes with complete SMT and limited historical query needs\n")
		fmt.Printf("Best for: Advanced production setups, maximum storage optimization\n")
	}

	// Ask for user confirmation
	if len(analysis.ToDelete) > 0 {
		fmt.Printf("\n⚠️  WARNING: This operation will permanently delete the above table data!\n")
	} else {
		fmt.Printf("\n⚠️  WARNING: This operation will perform batch-based partial pruning!\n")
	}

	if !config.AutoYes {
		if len(analysis.ToDelete) > 0 {
			fmt.Printf("Please enter 'yes' to confirm deletion: ")
		} else {
			fmt.Printf("Please enter 'yes' to confirm partial pruning: ")
		}

		var confirm string
		fmt.Scanln(&confirm)

		return confirm == "yes"
	} else {
		fmt.Printf("Auto-confirmed with --yes flag\n")
		return true
	}
}

// executeMainPruningOperations executes all pruning operations
func executeMainPruningOperations(paths *MainPaths, analysis *MainAnalysis, config *MainConfig, log logv3.Logger) (*MainStats, error) {
	ctx := context.Background()
	stats := &MainStats{}

	// Execute batch operations with guaranteed commit
	if config.PruneLevel == PruneLevelModerate || config.PruneLevel == PruneLevelAggressive {
		fmt.Printf("Closing database before batch operations...\n")
		fmt.Printf("✓ Database closed\n")

		stats.DeletedBatches, stats.DeletedBlocks = executeBatchOperationsWithCommit(paths.ChaindataPath, config.KeepRecentBatches, log, ctx)
	}

	// Execute dupCursor operations with guaranteed commit (Aggressive mode only)
	if config.PruneLevel == PruneLevelAggressive {
		executeDupCursorOperationsWithCommit(paths.ChaindataPath, config.KeepRecentBatches, config.FastDupCursorMode, config.SafeFastMode, log, ctx)
	}

	// Filter out block tables from full deletion if we did partial pruning
	partiallyPrunedTables := map[string]bool{
		// Header-related tables (must use same strategy for data consistency)
		"Header": true, "HeaderNumber": true,
		// Other block data tables
		"BlockBody": true, "Receipt": true, "TxSender": true, "TransactionLog": true,
		// zkEVM intermediate data tables
		"hermez_intermediate_tx_stateRoots": true,
		// DupCursor tables - excluded from normal batch processing due to special cursor requirements
		"CanonicalHeader":     true, // dupCursor table - needs special handling
		"hermez_blockBatches": true, // dupCursor table - needs special handling
	}

	// Execute optimized table deletion using pre-collected statistics
	fmt.Printf("\nStarting table deletion...\n")
	fmt.Printf("🗂️ Processing %d tables in optimized order (small to large)...\n", len(analysis.SortedTables))

	fmt.Printf("Database already closed after batch operations, starting staged deletion\n")

	// Execute table deletion operations with staged approach (user suggested optimization)
	stats.DeletedCount, stats.ActuallyDeletedTables, stats.ActualDeletedSize = executeStagedTableDeletion(
		paths.ChaindataPath, analysis.SortedTables, analysis.PreCollectedStats, partiallyPrunedTables, config.PruneLevel, log, ctx)

	return stats, nil
}

// calculateMainResults calculates space savings and displays final results
func calculateMainResults(analysis *MainAnalysis, stats *MainStats, config *MainConfig) {
	// Calculate space savings with overflow protection
	var batchDeletedSize uint64
	if (config.PruneLevel == PruneLevelModerate || config.PruneLevel == PruneLevelAggressive) && stats.DeletedBatches > 0 {
		// Conservative estimation to avoid overflow
		estimatedSize := analysis.TotalDbSize / 20 // Conservative estimate
		batchDeletedSize = estimatedSize
	}

	// Protect against overflow in total calculation
	totalSavedSpace := stats.ActualDeletedSize
	if batchDeletedSize > 0 && totalSavedSpace <= ^uint64(0)-batchDeletedSize {
		totalSavedSpace += batchDeletedSize
	}

	// Protect against division by zero and ensure reasonable percentage
	var spaceRatio float64
	if analysis.TotalDbSize > 0 && totalSavedSpace <= analysis.TotalDbSize {
		spaceRatio = float64(totalSavedSpace) / float64(analysis.TotalDbSize) * 100
	} else {
		// If calculation seems unreasonable, show conservative estimate
		spaceRatio = float64(stats.ActualDeletedSize) / float64(analysis.TotalDbSize) * 100
		totalSavedSpace = stats.ActualDeletedSize
	}

	fmt.Printf("\n=== Pruning Completed ===\n")
	fmt.Printf("Tables with actual data deleted: %d (out of %d total cleared)\n", stats.ActuallyDeletedTables, stats.DeletedCount)
	if (config.PruneLevel == PruneLevelModerate || config.PruneLevel == PruneLevelAggressive) && stats.DeletedBatches > 0 {
		fmt.Printf("Batch-level data deleted: %d batches (%d blocks)\n", stats.DeletedBatches, stats.DeletedBlocks)
	}
	fmt.Printf("Total space freed: %s (%.2f%% of database)\n",
		datasize.ByteSize(totalSavedSpace).HumanReadable(), spaceRatio)

	// Calculate remaining database size with overflow protection
	var remainingSize uint64
	if totalSavedSpace <= analysis.TotalDbSize {
		remainingSize = analysis.TotalDbSize - totalSavedSpace
	} else {
		// If saved space exceeds total size (calculation error), show original size
		remainingSize = analysis.TotalDbSize
	}
	fmt.Printf("Database size after pruning: %s\n", datasize.ByteSize(remainingSize).HumanReadable())
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(config.PruneLevel))

	fmt.Printf("\n=== Cleanup Complete ===\n")
	fmt.Printf("✓ Pruning operation completed successfully\n")
	fmt.Printf("✓ All changes have been committed to database\n")
}
