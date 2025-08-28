package main

import (
	"context"
	"fmt"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/ledgerwatch/erigon-lib/kv"
	mdbxpkg "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	logv3 "github.com/ledgerwatch/log/v3"
)

// Staged deletion flow - User suggested optimization
// Stage 1: Delete small tables → Commit → Close DB
// Stage 2: Reopen DB → Delete each large table individually → Commit → Close DB

const (
	// Large table threshold: tables larger than 1GB are considered large tables
	LARGE_TABLE_THRESHOLD = 1 * 1024 * 1024 * 1024 // 1GB
)

// executeStagedTableDeletion executes staged table deletion
func executeStagedTableDeletion(
	dbPath string,
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
		fmt.Printf("\n=== No tables to delete ===\n")
		return 0, 0, 0
	}

	// Classify tables: small tables and large tables
	var smallTables, largeTables []tableSizeInfo
	for _, table := range sortedTables {
		if table.size >= LARGE_TABLE_THRESHOLD {
			largeTables = append(largeTables, table)
		} else {
			smallTables = append(smallTables, table)
		}
	}

	fmt.Printf("\n=== Staged Deletion Strategy ===\n")
	fmt.Printf("Small tables (<%s): %d tables\n", datasize.ByteSize(LARGE_TABLE_THRESHOLD).HumanReadable(), len(smallTables))
	fmt.Printf("Large tables (>=%s): %d tables\n", datasize.ByteSize(LARGE_TABLE_THRESHOLD).HumanReadable(), len(largeTables))

	var totalDeletedCount, totalActuallyDeletedTables int
	var totalActualDeletedSize uint64

	// === Stage 1: Delete small tables ===
	if len(smallTables) > 0 {
		deletedCount, actuallyDeletedTables, actualDeletedSize := executeSmallTableDeletion(
			dbPath, smallTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log, ctx)

		totalDeletedCount += deletedCount
		totalActuallyDeletedTables += actuallyDeletedTables
		totalActualDeletedSize += actualDeletedSize
	}

	// === Stage 2: Delete large tables individually ===
	if len(largeTables) > 0 {
		deletedCount, actuallyDeletedTables, actualDeletedSize := executeLargeTablesDeletionIndividually(
			dbPath, largeTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log, ctx)

		totalDeletedCount += deletedCount
		totalActuallyDeletedTables += actuallyDeletedTables
		totalActualDeletedSize += actualDeletedSize
	}

	fmt.Printf("\n=== Staged Deletion Completed ===\n")
	fmt.Printf("Total tables deleted: %d (with actual data: %d)\n", totalDeletedCount, totalActuallyDeletedTables)
	fmt.Printf("Total space freed: %s\n", datasize.ByteSize(totalActualDeletedSize).HumanReadable())

	return totalDeletedCount, totalActuallyDeletedTables, totalActualDeletedSize
}

// executeSmallTableDeletion executes small table deletion (Stage 1)
func executeSmallTableDeletion(
	dbPath string,
	smallTables []tableSizeInfo,
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

	fmt.Printf("\n=== Stage 1: Small Table Deletion (%d tables) ===\n", len(smallTables))

	// Open database
	chaindb, _, err := openDatabase(dbPath, kv.ChainDB, log)
	if err != nil {
		logv3.Error("Failed to open database for small table deletion", "error", err)
		return 0, 0, 0
	}

	// Ensure database is closed
	defer func() {
		fmt.Printf("Stage 1 completed, closing database...\n")
		chaindb.Close()
		fmt.Printf("✓ Stage 1 database closed\n")

		// Give MDBX some time to complete cleanup
		time.Sleep(2 * time.Second)
	}()

	// Start transaction
	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		logv3.Error("Failed to start transaction for small table deletion", "error", err)
		return 0, 0, 0
	}

	// Ensure transaction is properly handled
	var committed bool
	defer func() {
		if !committed {
			fmt.Printf("Rolling back small table deletion transaction...\n")
			tx.Rollback()
		}
	}()

	// Execute small table deletion
	deletedCount, actuallyDeletedTables, actualDeletedSize := executeOptimizedTableDeletion(
		tx, chaindb, smallTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log)

	// Commit transaction
	fmt.Printf("Committing Stage 1 transaction...\n")
	if err := tx.Commit(); err != nil {
		logv3.Error("Failed to commit small table deletions", "error", err)
		return 0, 0, 0
	}
	committed = true

	fmt.Printf("✓ Stage 1 completed: deleted %d small tables, freed %s\n",
		actuallyDeletedTables, datasize.ByteSize(actualDeletedSize).HumanReadable())

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// executeLargeTablesDeletionIndividually executes large table deletion individually (Stage 2)
// Each large table is processed in its own transaction to avoid large transaction timeout
func executeLargeTablesDeletionIndividually(
	dbPath string,
	largeTables []tableSizeInfo,
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

	fmt.Printf("\n=== Stage 2: Large Table Deletion (%d tables, processed individually) ===\n", len(largeTables))

	// Brief wait to ensure Stage 1 resources are fully released
	fmt.Printf("Waiting for database resources to be released...\n")
	time.Sleep(3 * time.Second)

	var totalDeletedCount, totalActuallyDeletedTables int
	var totalActualDeletedSize uint64

	// Process each large table individually
	for i, largeTable := range largeTables {
		fmt.Printf("\n--- Processing large table %d/%d: %s (%s) ---\n",
			i+1, len(largeTables), largeTable.name, datasize.ByteSize(largeTable.size).HumanReadable())

		deletedCount, actuallyDeletedTables, actualDeletedSize := executeSingleLargeTableDeletion(
			dbPath, largeTable, preCollectedStats, partiallyPrunedTables, pruneLevel, log, ctx)

		totalDeletedCount += deletedCount
		totalActuallyDeletedTables += actuallyDeletedTables
		totalActualDeletedSize += actualDeletedSize

		fmt.Printf("✓ Large table %s processed: freed %s\n",
			largeTable.name, datasize.ByteSize(actualDeletedSize).HumanReadable())

		// Brief pause between large tables to let MDBX clean up
		if i < len(largeTables)-1 {
			fmt.Printf("Pausing between large tables...\n")
			time.Sleep(2 * time.Second)
		}
	}

	fmt.Printf("\n✓ Stage 2 completed: deleted %d large tables, freed %s total\n",
		totalActuallyDeletedTables, datasize.ByteSize(totalActualDeletedSize).HumanReadable())

	return totalDeletedCount, totalActuallyDeletedTables, totalActualDeletedSize
}

// executeSingleLargeTableDeletion processes a single large table in its own transaction
func executeSingleLargeTableDeletion(
	dbPath string,
	largeTable tableSizeInfo,
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

	// Open database for this single table
	chaindb, _, err := openDatabase(dbPath, kv.ChainDB, log)
	if err != nil {
		logv3.Error("Failed to open database for large table deletion", "table", largeTable.name, "error", err)
		return 0, 0, 0
	}

	// Ensure database is closed after processing this table
	defer func() {
		fmt.Printf("Closing database after processing %s...\n", largeTable.name)
		chaindb.Close()
		fmt.Printf("✓ Database closed for %s\n", largeTable.name)
	}()

	// Start transaction for this single table
	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		logv3.Error("Failed to start transaction for large table deletion", "table", largeTable.name, "error", err)
		return 0, 0, 0
	}

	// Ensure transaction is properly handled
	var committed bool
	defer func() {
		if !committed {
			fmt.Printf("Rolling back transaction for %s...\n", largeTable.name)
			tx.Rollback()
		}
	}()

	// Process only this single table
	singleTableSlice := []tableSizeInfo{largeTable}
	deletedCount, actuallyDeletedTables, actualDeletedSize := executeOptimizedTableDeletion(
		tx, chaindb, singleTableSlice, preCollectedStats, partiallyPrunedTables, pruneLevel, log)

	// Commit transaction for this table
	fmt.Printf("Committing transaction for %s...\n", largeTable.name)
	if err := tx.Commit(); err != nil {
		logv3.Error("Failed to commit large table deletion", "table", largeTable.name, "error", err)
		return 0, 0, 0
	}
	committed = true

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// openDatabaseWithRetry improved database opening function with retry mechanism
func openDatabaseWithRetry(dbPath string, label kv.Label, log logv3.Logger, maxRetries int) (kv.RwDB, error) {
	ctx := context.Background()

	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			fmt.Printf("Retrying database open (%d/%d)...\n", i+1, maxRetries)
			time.Sleep(time.Duration(i*2) * time.Second) // Incremental wait time
		}

		var opts mdbxpkg.MdbxOpts
		if label == kv.ChainDB {
			opts = mdbxpkg.NewMDBX(log).Path(dbPath).Label(label).WithTableCfg(mdbxpkg.WithChaindataTables)
		} else {
			opts = mdbxpkg.NewMDBX(log).Path(dbPath).Label(label)
		}

		db, err := opts.Open(ctx)
		if err == nil {
			fmt.Printf("✓ Database opened successfully\n")
			return db, nil
		}

		fmt.Printf("⚠️ Database open failed (attempt %d/%d): %v\n", i+1, maxRetries, err)
	}

	return nil, fmt.Errorf("failed to open database after %d retries", maxRetries)
}
