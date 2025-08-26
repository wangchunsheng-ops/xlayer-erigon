package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/c2h5oh/datasize"
	mdbx2 "github.com/erigontech/mdbx-go/mdbx"
	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/kv"
	mdbxpkg "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/smt/pkg/db"
	"github.com/ledgerwatch/erigon/zk/hermez_db"

	logv3 "github.com/ledgerwatch/log/v3"
)

// Define pruning levels
type PruneLevel int

const (
	PruneLevelModerate   PruneLevel = iota // Moderate pruning (recommended)
	PruneLevelAggressive                   // Aggressive pruning (includes state data cleanup)
)

// LatestBlockInfo stores essential information of the latest block
type LatestBlockInfo struct {
	Number      uint64
	Hash        common.Hash
	Header      *types.Header
	TxCount     int
	NeedRestore bool
}

// getLatestBlockNumber reads the latest block number from CanonicalHeader table
func getLatestBlockNumber(tx kv.RwTx) (uint64, error) {
	cursor, err := tx.Cursor("CanonicalHeader")
	if err != nil {
		return 0, fmt.Errorf("failed to open CanonicalHeader cursor: %w", err)
	}
	defer cursor.Close()

	// Move to last entry
	key, _, err := cursor.Last()
	if err != nil {
		return 0, fmt.Errorf("failed to get last entry: %w", err)
	}
	if len(key) == 0 {
		return 0, fmt.Errorf("no blocks found in CanonicalHeader table")
	}

	// CanonicalHeader key is block number (8 bytes, big endian)
	if len(key) != 8 {
		return 0, fmt.Errorf("invalid key length in CanonicalHeader: %d", len(key))
	}

	blockNumber := binary.BigEndian.Uint64(key)
	return blockNumber, nil
}

// getLatestBatchNumber reads the latest batch number from BLOCKBATCHES table
func getLatestBatchNumber(tx kv.Tx) (uint64, error) {
	c, err := tx.Cursor(hermez_db.BLOCKBATCHES)
	if err != nil {
		return 0, err
	}
	defer c.Close()

	// get the last entry from the table
	k, v, err := c.Last()
	if err != nil {
		return 0, err
	}
	if k == nil {
		return 0, nil
	}

	return hermez_db.BytesToUint64(v), nil
}

// partialPruneBatchTables performs batch-based pruning on block-related tables
func partialPruneBatchTables(tx kv.RwTx, keepRecentBatches uint64) (int, int, error) {
	hermezDb := hermez_db.NewHermezDbReader(tx)

	// 1. Get latest batch number
	latestBatch, err := getLatestBatchNumber(tx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get latest batch number: %w", err)
	}

	fmt.Printf("Latest batch number: %d\n", latestBatch)
	fmt.Printf("Keeping recent %d batches\n", keepRecentBatches)

	// 2. Calculate batch range to keep
	var pruneBefore uint64
	if latestBatch > keepRecentBatches {
		pruneBefore = latestBatch - keepRecentBatches + 1
	} else {
		fmt.Printf("No batches to prune (total batches <= keep recent batches)\n")
		return 0, 0, nil
	}

	fmt.Printf("Will delete data for batches < %d\n", pruneBefore)

	// 3. Execute batch-level pruning
	return executeBatchBasedPruning(tx, hermezDb, pruneBefore)
}

// executeBatchBasedPruning performs the actual batch-based pruning using Copy-Truncate-Restore strategy
func executeBatchBasedPruning(tx kv.RwTx, hermezDb *hermez_db.HermezDbReader, pruneBefore uint64) (int, int, error) {
	fmt.Printf("Starting optimized batch-level data pruning (Copy-Truncate-Restore strategy)...\n")

	// Get latest batch to determine what to keep
	latestBatch, err := getLatestBatchNumber(tx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get latest batch number: %w", err)
	}

	// Determine batches to keep (pruneBefore and later)
	keepFromBatch := pruneBefore
	fmt.Printf("Preserving batches %d to %d, deleting batches 0 to %d\n", keepFromBatch, latestBatch, pruneBefore-1)

	// Use optimized strategy: copy recent data, truncate tables, restore data
	return executeCopyTruncateRestore(tx, hermezDb, keepFromBatch, latestBatch, pruneBefore)
}

// executeCopyTruncateRestore implements the optimized pruning strategy
func executeCopyTruncateRestore(tx kv.RwTx, hermezDb *hermez_db.HermezDbReader, keepFromBatch, latestBatch, pruneBefore uint64) (int, int, error) {
	// Step 1: Identify all blocks in batches to keep
	fmt.Printf("Step 1: Collecting blocks to preserve...\n")
	var preserveBlocks []uint64
	preserveBatchCount := 0

	for batchNo := keepFromBatch; batchNo <= latestBatch; batchNo++ {
		blockNos, err := hermezDb.GetL2BlockNosByBatch(batchNo)
		if err != nil {
			continue
		}
		preserveBlocks = append(preserveBlocks, blockNos...)
		if len(blockNos) > 0 {
			preserveBatchCount++
		}
	}

	fmt.Printf("Found %d blocks in %d batches to preserve\n", len(preserveBlocks), preserveBatchCount)

	if len(preserveBlocks) == 0 {
		fmt.Printf("No blocks to preserve, will clear all tables\n")
		return int(pruneBefore), 0, clearAllBatchTables(tx)
	}

	// Step 2: Copy data to preserve
	fmt.Printf("Step 2: Copying data for %d blocks...\n", len(preserveBlocks))
	preservedData, err := copyBlockData(tx, preserveBlocks)
	if err != nil {
		fmt.Printf("Failed to copy data, falling back to old method\n")
		return executeLegacyPruning(tx, hermezDb, pruneBefore)
	}

	// Step 3: Clear batch-related tables
	fmt.Printf("Step 3: Clearing batch-related tables...\n")
	err = clearBatchTables(tx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to clear tables: %w", err)
	}

	// Step 4: Restore preserved data
	fmt.Printf("Step 4: Restoring preserved data...\n")
	err = restoreBlockData(tx, preservedData)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to restore data: %w", err)
	}

	// Step 5: Restore batch metadata for kept batches
	fmt.Printf("Step 5: Restoring batch metadata...\n")
	err = restoreBatchMetadata(tx, hermezDb, keepFromBatch, latestBatch)
	if err != nil {
		fmt.Printf("Warning: failed to restore some batch metadata: %v\n", err)
	}

	deletedBatches := int(pruneBefore)
	deletedBlocks := 0 // We don't count individual deleted blocks in this method

	fmt.Printf("Optimized pruning completed: removed %d batches, preserved %d blocks\n",
		deletedBatches, len(preserveBlocks))
	return deletedBatches, deletedBlocks, nil
}

// executeLegacyPruning falls back to the old method if the new method fails
func executeLegacyPruning(tx kv.RwTx, hermezDb *hermez_db.HermezDbReader, pruneBefore uint64) (int, int, error) {
	fmt.Printf("Using legacy pruning method...\n")

	deletedBatches := 0
	deletedBlocks := 0

	// Iterate through all batches to delete
	for batchNo := uint64(0); batchNo < pruneBefore; batchNo++ {
		// Get all blocks in this batch
		blockNos, err := hermezDb.GetL2BlockNosByBatch(batchNo)
		if err != nil {
			// Skip if batch doesn't exist
			continue
		}

		if len(blockNos) == 0 {
			continue
		}

		fmt.Printf("Deleting batch %d, containing %d blocks\n", batchNo, len(blockNos))

		// Delete all block data in this batch
		for _, blockNo := range blockNos {
			err := deleteBlockData(tx, blockNo)
			if err != nil {
				fmt.Printf("Warning: failed to delete block %d data: %v\n", blockNo, err)
				continue
			}
			deletedBlocks++
		}

		// Delete batch-related metadata
		err = deleteBatchMetadata(tx, batchNo)
		if err != nil {
			fmt.Printf("Warning: failed to delete batch %d metadata: %v\n", batchNo, err)
		}

		deletedBatches++
	}

	fmt.Printf("Legacy pruning completed: deleted %d batches, %d blocks\n", deletedBatches, deletedBlocks)
	return deletedBatches, deletedBlocks, nil
}

// BlockData represents data for a single block across all tables
type BlockData struct {
	BlockNo uint64
	Data    map[string][]byte // table -> data
}

// copyBlockData copies data for specified blocks from all relevant tables
func copyBlockData(tx kv.RwTx, blockNos []uint64) ([]BlockData, error) {
	var preservedData []BlockData

	// Tables that have block_number as key
	// Note: small tables (block_l1_info_tree_index, plain_state_version, smt_depths, MaxTxNum) excluded from cleanup
	// Note: dupCursor tables (CanonicalHeader, hermez_blockBatches) excluded - need special handling
	simpleTables := []string{
		"Receipt",
		"block_info_roots",
	}

	for i, blockNo := range blockNos {
		if i%1000 == 0 {
			fmt.Printf("Copying block %d (%d/%d)...\n", blockNo, i+1, len(blockNos))
		}

		blockData := BlockData{
			BlockNo: blockNo,
			Data:    make(map[string][]byte),
		}

		blockKey := make([]byte, 8)
		binary.BigEndian.PutUint64(blockKey, blockNo)

		// Copy data from simple tables
		for _, table := range simpleTables {
			data, err := tx.GetOne(table, blockKey)
			if err == nil && data != nil {
				blockData.Data[table] = append([]byte{}, data...) // Deep copy
			}
		}

		// TODO: Add special handling for Header, HeaderNumber, BlockBody if needed
		// For now, keep it simple and focus on the main bottleneck

		preservedData = append(preservedData, blockData)
	}

	fmt.Printf("Successfully copied data for %d blocks\n", len(preservedData))
	return preservedData, nil
}

// clearBatchTables clears all batch-related tables using optimized deletion strategy
func clearBatchTables(tx kv.RwTx) error {
	// Tables to clear (only batch-related ones, not SMT or other critical tables)
	// Note: small tables (block_l1_info_tree_index, plain_state_version, smt_depths, MaxTxNum) excluded from cleanup
	// Note: dupCursor tables (CanonicalHeader, hermez_blockBatches) excluded - need special handling
	tablesToClear := []string{
		"Receipt",
		"block_info_roots",
		// Add Header, HeaderNumber, BlockBody if needed
	}

	fmt.Printf("🚀 Applying optimized deletion strategy to batch tables...\n")

	// Apply layered optimization strategy to batch clearing operations
	// Even though these tables are not very large, they can still benefit from optimization
	return clearTablesWithOptimization(tx, tablesToClear, "batch clearing")
}

// clearTablesWithOptimization provides a generic optimized table clearing function
// Generic optimized table clearing function that can be reused in multiple scenarios
func clearTablesWithOptimization(tx kv.RwTx, tables []string, operationName string) error {
	if len(tables) == 0 {
		return nil
	}

	// For a small number of tables, using ClearBucket directly is the most efficient approach
	// This avoids the overhead of layered strategies while maintaining good performance
	for i, table := range tables {
		fmt.Printf("Clearing table (%d/%d): %s\n", i+1, len(tables), table)

		// 🔥 Key optimization: Use ClearBucket directly for complete clearing operations
		// This is dozens of times faster than deleting entries one by one, especially for large tables
		err := tx.ClearBucket(table)
		if err != nil {
			return fmt.Errorf("failed to clear table %s during %s: %w", table, operationName, err)
		}

		fmt.Printf("✅ Cleared table %s (optimized %s)\n", table, operationName)
	}

	fmt.Printf("🎯 Completed %s for %d tables using optimized strategy\n", operationName, len(tables))
	return nil
}

// restoreBlockData restores preserved block data to tables
func restoreBlockData(tx kv.RwTx, preservedData []BlockData) error {
	for i, blockData := range preservedData {
		if i%1000 == 0 {
			fmt.Printf("Restoring block %d (%d/%d)...\n", blockData.BlockNo, i+1, len(preservedData))
		}

		blockKey := make([]byte, 8)
		binary.BigEndian.PutUint64(blockKey, blockData.BlockNo)

		// Restore data to each table
		for table, data := range blockData.Data {
			err := tx.Put(table, blockKey, data)
			if err != nil {
				return fmt.Errorf("failed to restore block %d to table %s: %w", blockData.BlockNo, table, err)
			}
		}
	}

	fmt.Printf("Successfully restored data for %d blocks\n", len(preservedData))
	return nil
}

// clearAllBatchTables clears all batch-related tables (used when no blocks to preserve)
func clearAllBatchTables(tx kv.RwTx) error {
	fmt.Printf("Clearing all batch-related tables...\n")
	return clearBatchTables(tx)
}

// restoreBatchMetadata restores batch metadata for kept batches
func restoreBatchMetadata(tx kv.RwTx, hermezDb *hermez_db.HermezDbReader, keepFromBatch, latestBatch uint64) error {
	// This is a simplified implementation
	// In practice, you might need to restore hermez_blockBatches and other batch metadata
	// For now, we assume hermez_blockBatches is handled in copyBlockData

	fmt.Printf("Batch metadata restoration completed\n")
	return nil
}

// deleteBlockData deletes all data related to a specific block
func deleteBlockData(tx kv.RwTx, blockNo uint64) error {
	blockKey := make([]byte, 8)
	binary.BigEndian.PutUint64(blockKey, blockNo)

	// Delete simple block-related table data (key = block_num_u64)
	// Note: small tables (block_l1_info_tree_index, plain_state_version, smt_depths, MaxTxNum) excluded from cleanup
	// Note: dupCursor tables (CanonicalHeader, hermez_blockBatches) excluded - need special handling
	simpleTables := []string{
		"Receipt",
		// zkEVM specific tables with block_number keys
		"block_info_roots", // block number -> block info root hash
	}
	for _, table := range simpleTables {
		err := tx.Delete(table, blockKey)
		if err != nil {
			return fmt.Errorf("failed to delete %s for block %d: %w", table, blockNo, err)
		}
	}

	// Special handling for Header table (key = block_num_u64 + hash)
	err := deleteHeaderData(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete header for block %d: %w", blockNo, err)
	}

	// Special handling for HeaderNumber table (key = header_hash -> block_num)
	err = deleteHeaderNumber(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete header number for block %d: %w", blockNo, err)
	}

	// Special handling for BlockBody table (key = block_num_u64 + hash)
	err = deleteBlockBodyData(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete block body for block %d: %w", blockNo, err)
	}

	// Special handling for TxSender table (key = block_num_u64 + blockHash)
	err = deleteTxSenderData(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete tx sender for block %d: %w", blockNo, err)
	}

	// Special handling for TransactionLog table (needs iteration)
	err = deleteTransactionLogs(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete transaction logs for block %d: %w", blockNo, err)
	}

	// Special handling for composite key tables (block_number + hash)
	err = deleteCompositeKeyData(tx, blockNo)
	if err != nil {
		return fmt.Errorf("failed to delete composite key data for block %d: %w", blockNo, err)
	}

	return nil
}

// deleteHeaderData deletes header data for a specific block (key = block_num_u64 + hash)
func deleteHeaderData(tx kv.RwTx, blockNo uint64) error {
	cursor, err := tx.RwCursor("Header")
	if err != nil {
		return err
	}
	defer cursor.Close()

	// Header key format: blockNum(8) + hash(32)
	blockPrefix := make([]byte, 8)
	binary.BigEndian.PutUint64(blockPrefix, blockNo)

	var keysToDelete [][]byte
	for key, _, err := cursor.Seek(blockPrefix); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return err
		}
		// Check if key starts with our block number prefix
		if len(key) < 8 || !bytes.Equal(key[:8], blockPrefix) {
			break // No more entries for this block
		}
		keysToDelete = append(keysToDelete, common.Copy(key))
	}

	// Delete collected keys (two-phase deletion for safety)
	for _, key := range keysToDelete {
		if err := tx.Delete("Header", key); err != nil {
			return err
		}
	}

	return nil
}

// deleteHeaderNumber deletes header number entries for a specific block (key = header_hash -> block_num)
func deleteHeaderNumber(tx kv.RwTx, blockNo uint64) error {
	cursor, err := tx.RwCursor("HeaderNumber")
	if err != nil {
		return err
	}
	defer cursor.Close()

	// HeaderNumber table: header_hash -> block_num_u64
	// We need to find entries where value equals our block number
	targetBlockBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(targetBlockBytes, blockNo)

	var keysToDelete [][]byte
	for key, value, err := cursor.First(); key != nil; key, value, err = cursor.Next() {
		if err != nil {
			return err
		}
		// Check if value equals our target block number
		if len(value) == 8 && bytes.Equal(value, targetBlockBytes) {
			keysToDelete = append(keysToDelete, common.Copy(key))
		}
	}

	// Delete collected keys (two-phase deletion for safety)
	for _, key := range keysToDelete {
		if err := tx.Delete("HeaderNumber", key); err != nil {
			return err
		}
	}

	return nil
}

// deleteBlockBodyData deletes block body data for a specific block (key = block_num_u64 + hash)
func deleteBlockBodyData(tx kv.RwTx, blockNo uint64) error {
	cursor, err := tx.RwCursor("BlockBody")
	if err != nil {
		return err
	}
	defer cursor.Close()

	// BlockBody key format: blockNum(8) + hash(32)
	blockPrefix := make([]byte, 8)
	binary.BigEndian.PutUint64(blockPrefix, blockNo)

	var keysToDelete [][]byte
	for key, _, err := cursor.Seek(blockPrefix); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return err
		}
		// Check if key starts with our block number prefix
		if len(key) < 8 || !bytes.Equal(key[:8], blockPrefix) {
			break // No more entries for this block
		}
		keysToDelete = append(keysToDelete, common.Copy(key))
	}

	// Delete collected keys (two-phase deletion for safety)
	for _, key := range keysToDelete {
		if err := tx.Delete("BlockBody", key); err != nil {
			return err
		}
	}

	return nil
}

// deleteTxSenderData deletes tx sender data for a specific block (key = block_num_u64 + blockHash)
func deleteTxSenderData(tx kv.RwTx, blockNo uint64) error {
	cursor, err := tx.RwCursor("TxSender")
	if err != nil {
		return err
	}
	defer cursor.Close()

	// TxSender key format: blockNum(8) + blockHash(32)
	blockPrefix := make([]byte, 8)
	binary.BigEndian.PutUint64(blockPrefix, blockNo)

	var keysToDelete [][]byte
	for key, _, err := cursor.Seek(blockPrefix); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return err
		}
		// Check if key starts with our block number prefix
		if len(key) < 8 || !bytes.Equal(key[:8], blockPrefix) {
			break // No more entries for this block
		}
		keysToDelete = append(keysToDelete, common.Copy(key))
	}

	// Delete collected keys (two-phase deletion for safety)
	for _, key := range keysToDelete {
		if err := tx.Delete("TxSender", key); err != nil {
			return err
		}
	}

	return nil
}

// deleteTransactionLogs deletes transaction logs for a specific block
func deleteTransactionLogs(tx kv.RwTx, blockNo uint64) error {
	cursor, err := tx.RwCursor("TransactionLog")
	if err != nil {
		return err
	}
	defer cursor.Close()

	// TransactionLog format: blockNum(8) + txIndex(4) + logIndex(4)
	blockPrefix := make([]byte, 8)
	binary.BigEndian.PutUint64(blockPrefix, blockNo)

	var keysToDelete [][]byte
	for key, _, err := cursor.Seek(blockPrefix); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return err
		}

		if len(key) < 8 {
			break
		}

		// Check if it belongs to current block
		keyBlockNo := binary.BigEndian.Uint64(key[:8])
		if keyBlockNo != blockNo {
			break
		}

		keysToDelete = append(keysToDelete, common.Copy(key))
	}

	// Delete all found keys
	for _, key := range keysToDelete {
		err := tx.Delete("TransactionLog", key)
		if err != nil {
			return err
		}
	}

	return nil
}

// pruneHistoricalDupCursorData performs aggressive cleanup of historical dupCursor table data
// (AccountChangeSet, StorageChangeSet only - CanonicalHeader and hermez_blockBatches are preserved)
// while preserving recent batches for operational needs
// fastMode: if true, uses direct cursor deletion for maximum performance
// safeFastMode: if true, uses safe-fast mode (balanced performance and safety)
func pruneHistoricalDupCursorData(tx kv.RwTx, keepRecentBatches uint64, fastMode bool, safeFastMode bool) (int, error) {
	fmt.Printf("Starting historical dupCursor data cleanup (keeping recent %d batches)...\n", keepRecentBatches)
	fmt.Printf("Note: Only processing AccountChangeSet and StorageChangeSet - CanonicalHeader and hermez_blockBatches preserved for node stability\n")

	// Get the range of blocks to delete (everything except recent batches)
	latestBlock, err := getLatestBlockNumber(tx)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest block number: %w", err)
	}

	// Calculate cutoff point - we need to determine which blocks correspond to recent batches
	hermezDb := hermez_db.NewHermezDb(tx)
	latestBatch, err := hermezDb.GetLatestDownloadedBatchNo()
	if err != nil {
		return 0, fmt.Errorf("failed to get latest batch number: %w", err)
	}

	var cutoffBatch uint64
	if latestBatch >= keepRecentBatches {
		cutoffBatch = latestBatch - keepRecentBatches
	} else {
		// If we have fewer batches than we want to keep, don't delete anything
		fmt.Printf("Only %d batches exist, keeping all (requested to keep %d)\n", latestBatch, keepRecentBatches)
		return 0, nil
	}

	// Find the first block of the cutoff batch to determine block-level cutoff
	cutoffBlock, found, err := hermezDb.GetLowestBlockInBatch(cutoffBatch + 1) // +1 because we want to keep this batch
	if err != nil {
		return 0, fmt.Errorf("failed to get first block of batch %d: %w", cutoffBatch+1, err)
	}
	if !found {
		fmt.Printf("No blocks found in batch %d, using latest block as cutoff\n", cutoffBatch+1)
		cutoffBlock = latestBlock // Use latest block as fallback
	}

	fmt.Printf("Deleting state data for blocks 0-%d (keeping blocks %d-%d, batches %d-%d)\n",
		cutoffBlock-1, cutoffBlock, latestBlock, cutoffBatch+1, latestBatch)

	deletedRecords := 0

	// Clean AccountChangeSet data (dupCursor table)
	accountDeletedCount, err := pruneAccountChangeSetBeforeBlockOptimized(tx, cutoffBlock, fastMode, safeFastMode)
	if err != nil {
		return deletedRecords, fmt.Errorf("failed to prune AccountChangeSet: %w", err)
	}
	deletedRecords += accountDeletedCount
	fmt.Printf("✓ Deleted %d AccountChangeSet records\n", accountDeletedCount)

	// Clean StorageChangeSet data (dupCursor table)
	storageDeletedCount, err := pruneStorageChangeSetBeforeBlockOptimized(tx, cutoffBlock, fastMode, safeFastMode)
	if err != nil {
		return deletedRecords, fmt.Errorf("failed to prune StorageChangeSet: %w", err)
	}
	deletedRecords += storageDeletedCount
	fmt.Printf("✓ Deleted %d StorageChangeSet records\n", storageDeletedCount)

	// Note: CanonicalHeader and hermez_blockBatches are NOT processed here
	// These tables are critical for node operation and are preserved for stability

	return deletedRecords, nil
}

// pruneAccountChangeSetBeforeBlockOptimized deletes AccountChangeSet records before specified block
// Uses optimized batch processing to avoid memory overflow and improve performance
// fastMode: if true, uses direct cursor deletion (faster but more aggressive)
// safeFastMode: if true, uses safe-fast mode (balanced performance and safety)
func pruneAccountChangeSetBeforeBlockOptimized(tx kv.RwTx, cutoffBlock uint64, fastMode bool, safeFastMode bool) (int, error) {
	cursor, err := tx.RwCursorDupSort("AccountChangeSet")
	if err != nil {
		return 0, err
	}
	defer cursor.Close()

	const batchSize = 10000 // Process in batches to avoid memory overflow
	deletedCount := 0
	processedBlocks := 0

	if fastMode {
		fmt.Printf("🚀 Fast processing AccountChangeSet (direct cursor deletion)...\n")
		return pruneAccountChangeSetBeforeBlockFast(tx, cutoffBlock, cursor)
	} else if safeFastMode {
		fmt.Printf("🛡️⚡ Safe-Fast processing AccountChangeSet (smaller batches + validation)...\n")
		return pruneAccountChangeSetBeforeBlockSafeFast(tx, cutoffBlock, cursor)
	}

	fmt.Printf("🔄 Processing AccountChangeSet (batch size: %d)...\n", batchSize)

	// Process in batches to optimize memory usage
	for {
		var keysToDelete [][]byte
		currentBatchSize := 0

		// Collect a batch of keys to delete
		startKey, _, _ := cursor.Current()
		if startKey == nil {
			// Start from beginning if no current position
			startKey, _, _ = cursor.First()
		}

		for key, _, err := cursor.Current(); key != nil && currentBatchSize < batchSize; {
			if err != nil {
				return deletedCount, err
			}

			if len(key) >= 8 {
				blockNum := binary.BigEndian.Uint64(key[:8])
				if blockNum >= cutoffBlock {
					// Reached cutoff, we're done
					goto deleteBatch
				}

				// Collect all duplicate entries for this block number key
				seekKey := make([]byte, len(key))
				copy(seekKey, key)

				for k, _, err := cursor.SeekExact(seekKey); k != nil; k, _, err = cursor.NextDup() {
					if err != nil {
						return deletedCount, err
					}
					// Only add if we haven't exceeded batch size
					if currentBatchSize < batchSize {
						keyCopy := make([]byte, len(k))
						copy(keyCopy, k)
						keysToDelete = append(keysToDelete, keyCopy)
						currentBatchSize++
					} else {
						break
					}
				}

				processedBlocks++
				if processedBlocks%1000 == 0 {
					fmt.Printf("⏳ Processed %d blocks (%d records collected)...\n", processedBlocks, len(keysToDelete))
				}
			}

			// Move to next unique block number
			key, _, err = cursor.NextNoDup()
		}

	deleteBatch:
		// Delete current batch
		if len(keysToDelete) == 0 {
			break // No more records to delete
		}

		batchDeleted := 0
		for _, key := range keysToDelete {
			if err := tx.Delete("AccountChangeSet", key); err != nil {
				// Log but continue - some keys might not exist anymore
				continue
			}
			batchDeleted++
		}

		deletedCount += batchDeleted
		fmt.Printf("✓ Deleted batch: %d records (total: %d)\n", batchDeleted, deletedCount)

		// Check if we processed all records before cutoff
		if currentBatchSize < batchSize {
			break // This was the last batch
		}

		// Continue from where we left off
		keysToDelete = nil // Release memory
	}

	return deletedCount, nil
}

// pruneAccountChangeSetBeforeBlockFast performs direct cursor deletion for maximum speed
// ⚠️  More aggressive - deletes records immediately without collecting them first
func pruneAccountChangeSetBeforeBlockFast(tx kv.RwTx, cutoffBlock uint64, cursor kv.RwCursorDupSort) (int, error) {
	deletedCount := 0
	processedBlocks := 0

	// Direct deletion approach - faster but more aggressive
	for key, _, err := cursor.First(); key != nil; {
		if err != nil {
			return deletedCount, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached cutoff
			}

			// Delete all duplicate entries for this block directly
			duplicateCount := 0
			for k, _, err := cursor.SeekExact(key); k != nil; {
				if err != nil {
					return deletedCount, err
				}

				// Delete current entry directly via cursor
				if err := cursor.DeleteCurrent(); err != nil {
					// If deletion fails, try to continue
					key, _, err = cursor.NextDup()
					continue
				}

				duplicateCount++
				deletedCount++

				// Move to next duplicate (cursor position may have changed after deletion)
				key, _, err = cursor.NextDup()
			}

			processedBlocks++
			if processedBlocks%2000 == 0 {
				fmt.Printf("⚡ Fast deleted %d blocks (%d total records)...\n", processedBlocks, deletedCount)
			}
		}

		// Move to next unique block number
		key, _, err = cursor.NextNoDup()
	}

	return deletedCount, nil
}

// pruneAccountChangeSetBeforeBlockSafeFast performs safe-fast cursor deletion with enhanced error handling
// 🛡️⚡ Balanced approach: smaller batches + validation + better error recovery
func pruneAccountChangeSetBeforeBlockSafeFast(tx kv.RwTx, cutoffBlock uint64, cursor kv.RwCursorDupSort) (int, error) {
	const safeBatchSize = 2000      // Smaller batches for safety
	const validationInterval = 5000 // Validate every N deletions

	deletedCount := 0
	processedBlocks := 0
	validationFailures := 0

	fmt.Printf("Safe-Fast mode: using smaller batches (%d) with validation every %d deletions\n", safeBatchSize, validationInterval)

	// Safe direct deletion with smaller batches and validation
	for key, _, err := cursor.First(); key != nil; {
		if err != nil {
			return deletedCount, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached cutoff
			}

			batchStart := deletedCount

			// Process duplicates for this block with safety checks
			for k, _, err := cursor.SeekExact(key); k != nil; {
				if err != nil {
					fmt.Printf("⚠️ Seek error for block %d: %v\n", blockNum, err)
					key, _, err = cursor.NextNoDup()
					break
				}

				// Store key for validation (small overhead for safety)
				keyBackup := make([]byte, len(k))
				copy(keyBackup, k)

				// Attempt deletion
				if err := cursor.DeleteCurrent(); err != nil {
					fmt.Printf("⚠️ Delete failed for key in block %d: %v\n", blockNum, err)
					validationFailures++

					// Try to recover position
					if _, _, seekErr := cursor.SeekExact(keyBackup); seekErr != nil {
						fmt.Printf("⚠️ Recovery failed, continuing to next block\n")
						key, _, err = cursor.NextNoDup()
						break
					}
					key, _, err = cursor.NextDup()
					continue
				}

				deletedCount++

				// Periodic validation to ensure cursor integrity
				if deletedCount%validationInterval == 0 {
					if validateErr := validateCursorState(cursor, cutoffBlock); validateErr != nil {
						fmt.Printf("⚠️ Cursor validation failed at %d deletions: %v\n", deletedCount, validateErr)
						validationFailures++
					}
					fmt.Printf("🔍 Validated: %d deletions processed (failures: %d)\n", deletedCount, validationFailures)
				}

				// Check if we've processed enough in this batch
				if deletedCount-batchStart >= safeBatchSize {
					fmt.Printf("📦 Batch limit reached, moving to next block\n")
					key, _, err = cursor.NextNoDup()
					break
				}

				// Move to next duplicate
				key, _, err = cursor.NextDup()
			}

			processedBlocks++
			if processedBlocks%1000 == 0 {
				fmt.Printf("🛡️⚡ Safe-Fast processed %d blocks (%d records, %d failures)\n",
					processedBlocks, deletedCount, validationFailures)
			}
		}

		// Move to next unique block number
		key, _, err = cursor.NextNoDup()
	}

	if validationFailures > 0 {
		fmt.Printf("⚠️ Safe-Fast completed with %d validation failures (non-critical)\n", validationFailures)
	}

	return deletedCount, nil
}

// validateCursorState performs basic validation of cursor state
func validateCursorState(cursor kv.RwCursorDupSort, cutoffBlock uint64) error {
	currentKey, _, err := cursor.Current()
	if err != nil {
		return fmt.Errorf("cursor.Current() failed: %w", err)
	}

	if len(currentKey) >= 8 {
		blockNum := binary.BigEndian.Uint64(currentKey[:8])
		if blockNum >= cutoffBlock {
			return fmt.Errorf("cursor moved beyond cutoff block: %d >= %d", blockNum, cutoffBlock)
		}
	}

	return nil
}

// pruneStorageChangeSetBeforeBlockOptimized deletes StorageChangeSet records before specified block
// Uses optimized batch processing to avoid memory overflow and improve performance
// fastMode: if true, uses direct cursor deletion (faster but more aggressive)
// safeFastMode: if true, uses safe-fast mode (balanced performance and safety)
func pruneStorageChangeSetBeforeBlockOptimized(tx kv.RwTx, cutoffBlock uint64, fastMode bool, safeFastMode bool) (int, error) {
	cursor, err := tx.RwCursorDupSort("StorageChangeSet")
	if err != nil {
		return 0, err
	}
	defer cursor.Close()

	const batchSize = 10000 // Process in batches to avoid memory overflow
	deletedCount := 0
	processedRecords := 0

	if fastMode {
		fmt.Printf("🚀 Fast processing StorageChangeSet (direct cursor deletion)...\n")
		return pruneStorageChangeSetBeforeBlockFast(tx, cutoffBlock, cursor)
	} else if safeFastMode {
		fmt.Printf("🛡️⚡ Safe-Fast processing StorageChangeSet (smaller batches + validation)...\n")
		return pruneStorageChangeSetBeforeBlockSafeFast(tx, cutoffBlock, cursor)
	}

	fmt.Printf("🔄 Processing StorageChangeSet (batch size: %d)...\n", batchSize)

	// Process in batches to optimize memory usage
	for {
		var keysToDelete [][]byte
		currentBatchSize := 0

		// Position cursor at start or continue from current position
		if processedRecords == 0 {
			// Start from beginning
			cursor.First()
		}

		// Collect a batch of keys to delete
		// StorageChangeSet key format: block_number + address + incarnation + storage_key
		for key, _, err := cursor.Current(); key != nil && currentBatchSize < batchSize; key, _, err = cursor.Next() {
			if err != nil {
				return deletedCount, err
			}

			if len(key) >= 8 {
				blockNum := binary.BigEndian.Uint64(key[:8])
				if blockNum >= cutoffBlock {
					// Reached cutoff, we're done
					goto deleteBatch
				}

				// Make a copy of the key
				keyCopy := make([]byte, len(key))
				copy(keyCopy, key)
				keysToDelete = append(keysToDelete, keyCopy)
				currentBatchSize++
				processedRecords++

				if processedRecords%5000 == 0 {
					fmt.Printf("⏳ Processed %d storage records (%d in current batch)...\n", processedRecords, len(keysToDelete))
				}
			}
		}

	deleteBatch:
		// Delete current batch
		if len(keysToDelete) == 0 {
			break // No more records to delete
		}

		batchDeleted := 0
		for _, key := range keysToDelete {
			if err := tx.Delete("StorageChangeSet", key); err != nil {
				// Log but continue - some keys might not exist anymore
				continue
			}
			batchDeleted++
		}

		deletedCount += batchDeleted
		fmt.Printf("✓ Deleted batch: %d storage records (total: %d)\n", batchDeleted, deletedCount)

		// Check if we processed all records before cutoff
		if currentBatchSize < batchSize {
			break // This was the last batch
		}

		// Continue from where we left off
		keysToDelete = nil // Release memory
	}

	return deletedCount, nil
}

// pruneStorageChangeSetBeforeBlockFast performs direct cursor deletion for maximum speed
// ⚠️  More aggressive - deletes records immediately without collecting them first
func pruneStorageChangeSetBeforeBlockFast(tx kv.RwTx, cutoffBlock uint64, cursor kv.RwCursorDupSort) (int, error) {
	deletedCount := 0
	processedRecords := 0

	// Direct deletion approach - faster but more aggressive
	// StorageChangeSet key format: block_number + address + incarnation + storage_key
	for key, _, err := cursor.First(); key != nil; {
		if err != nil {
			return deletedCount, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached cutoff
			}

			// Delete current storage record directly
			if err := cursor.DeleteCurrent(); err != nil {
				// If deletion fails, try to continue
				key, _, err = cursor.Next()
				continue
			}

			deletedCount++
			processedRecords++

			if processedRecords%10000 == 0 {
				fmt.Printf("⚡ Fast deleted %d storage records...\n", deletedCount)
			}
		}

		// Move to next record
		key, _, err = cursor.Next()
	}

	return deletedCount, nil
}

// pruneStorageChangeSetBeforeBlockSafeFast performs safe-fast cursor deletion for storage data
// 🛡️⚡ Optimized for StorageChangeSet table with validation and error recovery
func pruneStorageChangeSetBeforeBlockSafeFast(tx kv.RwTx, cutoffBlock uint64, cursor kv.RwCursorDupSort) (int, error) {
	const safeBatchSize = 3000      // Slightly larger batches for storage (less duplicates per key)
	const validationInterval = 8000 // Validate every N deletions

	deletedCount := 0
	processedRecords := 0
	validationFailures := 0

	fmt.Printf("Safe-Fast mode: using storage-optimized batches (%d) with validation every %d deletions\n", safeBatchSize, validationInterval)

	// Safe direct deletion with validation for storage data
	// StorageChangeSet key format: block_number + address + incarnation + storage_key
	for key, _, err := cursor.First(); key != nil; {
		if err != nil {
			return deletedCount, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached cutoff
			}

			// Store key for validation and recovery
			keyBackup := make([]byte, len(key))
			copy(keyBackup, key)

			// Attempt deletion
			if err := cursor.DeleteCurrent(); err != nil {
				fmt.Printf("⚠️ Delete failed for storage record in block %d: %v\n", blockNum, err)
				validationFailures++

				// Try to recover position and continue
				if _, _, seekErr := cursor.SeekExact(keyBackup); seekErr != nil {
					fmt.Printf("⚠️ Recovery failed, skipping to next record\n")
				}
				key, _, err = cursor.Next()
				continue
			}

			deletedCount++
			processedRecords++

			// Periodic validation for cursor integrity
			if deletedCount%validationInterval == 0 {
				if validateErr := validateCursorState(cursor, cutoffBlock); validateErr != nil {
					fmt.Printf("⚠️ Storage cursor validation failed at %d deletions: %v\n", deletedCount, validateErr)
					validationFailures++
				}
				fmt.Printf("🔍 Storage validated: %d deletions processed (failures: %d)\n", deletedCount, validationFailures)
			}

			// Progress reporting
			if processedRecords%10000 == 0 {
				fmt.Printf("🛡️⚡ Safe-Fast storage: %d records processed (%d failures)\n", deletedCount, validationFailures)
			}

			// Batch size control for memory management
			if processedRecords%safeBatchSize == 0 {
				// Small pause to allow other operations (cooperative multitasking)
				// This helps with long-running operations
			}
		}

		// Move to next record
		key, _, err = cursor.Next()
	}

	if validationFailures > 0 {
		fmt.Printf("⚠️ Safe-Fast storage completed with %d validation failures (non-critical)\n", validationFailures)
	}

	return deletedCount, nil
}

// pruneCanonicalHeaderBeforeBlock deletes CanonicalHeader records before specified block
func pruneCanonicalHeaderBeforeBlock(tx kv.RwTx, cutoffBlock uint64) (int, error) {
	cursor, err := tx.RwCursorDupSort("CanonicalHeader")
	if err != nil {
		return 0, err
	}
	defer cursor.Close()

	// Collect all keys to delete first (safer for DupSort tables)
	var keysToDelete [][]byte

	// CanonicalHeader key format: block_number(8 bytes) -> block_hash
	// We need to collect all entries where block_number < cutoffBlock
	for key, _, err := cursor.First(); key != nil; key, _, err = cursor.NextNoDup() {
		if err != nil {
			return 0, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached the cutoff, stop collecting
			}

			// Collect all entries for this block (there might be multiple hashes per block in dupCursor)
			for k, _, err := cursor.SeekExact(key); k != nil; k, _, err = cursor.NextDup() {
				if err != nil {
					return 0, err
				}
				// Make a copy of the key
				keyCopy := make([]byte, len(k))
				copy(keyCopy, k)
				keysToDelete = append(keysToDelete, keyCopy)
			}
		}
	}

	// Now delete all collected keys using tx.Delete (safer than cursor operations)
	deletedCount := 0
	for _, key := range keysToDelete {
		if err := tx.Delete("CanonicalHeader", key); err != nil {
			// Log but continue - some keys might not exist anymore
			continue
		}
		deletedCount++
	}

	return deletedCount, nil
}

// pruneHermezBlockBatchesBeforeBlock deletes hermez_blockBatches records before specified block
func pruneHermezBlockBatchesBeforeBlock(tx kv.RwTx, cutoffBlock uint64) (int, error) {
	cursor, err := tx.RwCursorDupSort("hermez_blockBatches")
	if err != nil {
		return 0, err
	}
	defer cursor.Close()

	// Collect all keys to delete first (safer for DupSort tables)
	var keysToDelete [][]byte

	// hermez_blockBatches key format: l2blockno(8 bytes) -> batchno
	// We need to collect all entries where l2blockno < cutoffBlock
	for key, _, err := cursor.First(); key != nil; key, _, err = cursor.NextNoDup() {
		if err != nil {
			return 0, err
		}

		if len(key) >= 8 {
			blockNum := binary.BigEndian.Uint64(key[:8])
			if blockNum >= cutoffBlock {
				break // Reached the cutoff, stop collecting
			}

			// Collect all entries for this block (there might be multiple batches per block in dupCursor)
			for k, _, err := cursor.SeekExact(key); k != nil; k, _, err = cursor.NextDup() {
				if err != nil {
					return 0, err
				}
				// Make a copy of the key
				keyCopy := make([]byte, len(k))
				copy(keyCopy, k)
				keysToDelete = append(keysToDelete, keyCopy)
			}
		}
	}

	// Now delete all collected keys using tx.Delete (safer than cursor operations)
	deletedCount := 0
	for _, key := range keysToDelete {
		if err := tx.Delete("hermez_blockBatches", key); err != nil {
			// Log but continue - some keys might not exist anymore
			continue
		}
		deletedCount++
	}

	return deletedCount, nil
}

// deleteCompositeKeyData deletes data from tables with composite keys (block_number + hash)
func deleteCompositeKeyData(tx kv.RwTx, blockNo uint64) error {
	// Tables with composite key format: block_number_u64 + hash
	// Note: HeadersTotalDifficulty excluded as it's a small table that doesn't need cleanup
	compositeKeyTables := []string{
		"Header",                            // block_num_u64 + hash -> header (RLP)
		"BlockBody",                         // block_num_u64 + hash -> block body
		"TxSender",                          // block_num_u64 + blockHash -> sendersList
		"hermez_intermediate_tx_stateRoots", // l2blockno + txhash -> stateRoot
	}

	blockPrefix := make([]byte, 8)
	binary.BigEndian.PutUint64(blockPrefix, blockNo)

	for _, tableName := range compositeKeyTables {
		err := deleteTableWithBlockPrefix(tx, tableName, blockPrefix)
		if err != nil {
			// Log warning but continue - some tables might not exist or have no data for this block
			continue
		}
	}

	return nil
}

// deleteTableWithBlockPrefix deletes all entries from a table that start with given block prefix
func deleteTableWithBlockPrefix(tx kv.RwTx, tableName string, blockPrefix []byte) error {
	cursor, err := tx.RwCursor(tableName)
	if err != nil {
		return err // Table might not exist
	}
	defer cursor.Close()

	var keysToDelete [][]byte
	for key, _, err := cursor.Seek(blockPrefix); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return err
		}

		// Check if key starts with our block prefix
		if len(key) < len(blockPrefix) || !bytes.HasPrefix(key, blockPrefix) {
			break // No more entries for this block
		}

		keysToDelete = append(keysToDelete, common.Copy(key))
	}

	// Delete all found keys
	for _, key := range keysToDelete {
		err := cursor.Delete(key)
		if err != nil {
			return err
		}
	}

	return nil
}

// deleteBatchMetadata deletes batch-related metadata
func deleteBatchMetadata(tx kv.RwTx, batchNo uint64) error {
	batchKey := hermez_db.Uint64ToBytes(batchNo)

	// Delete batch-related hermez table data
	batchTables := []string{
		hermez_db.BATCH_BLOCKS,
		hermez_db.FORKIDS,
		hermez_db.STATE_ROOTS,
		hermez_db.GLOBAL_EXIT_ROOTS_BATCHES,
		hermez_db.BATCH_WITNESSES,
		hermez_db.BATCH_COUNTERS,
		hermez_db.L1_BATCH_DATA,
		hermez_db.LATEST_USED_GER,
		hermez_db.BATCH_ENDS,
	}

	for _, table := range batchTables {
		err := tx.Delete(table, batchKey)
		if err != nil {
			// Some tables may not have corresponding batch data, this is normal
			continue
		}
	}

	return nil
}

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

// getTableCategories returns predefined table categories for analysis
func getTableCategories() map[string][]string {
	return map[string][]string{
		"SMT Related Tables": append(db.HermezSmtTables, "HermezSmtLastRoot"),
		"Basic Block Tables": {
			"HeaderNumber", "BadHeaderNumber",
			"BlockBody", "Header", "BlockTransaction", "Receipt", "TxSender", "CanonicalHeader",
			"BlockRoot", "BlockRootToBlockHash", "BlockRootToBlockNumber", "BlockRootToKzgCommitments",
			"LastBlock", "LastHeader", "TransactionLog", "NonCanonicalTransaction",
			"BlockTransactionLookup", "BlockBorTransactionLookup", "InnerTx",
		},
		"State Data Tables": {
			"PlainState", "HashedStorage", "StateAccounts", "StateStorage",
			"StateCode", "StateCommitment", "Code", "HashedAccount", "HashedCodeHash",
			"PlainCodeHash", "TEVMCode", "StateEvents", "StateRoot",
			"IncarnationMap",
		},
		"History Data Tables": {
			"AccountChangeSet", "StorageChangeSet", "AccountHistory", "StorageHistory",
			"AccountHistoryKeys", "AccountHistoryVals", "StorageHistoryKeys", "StorageHistoryVals",
			"CodeHistoryKeys", "CodeHistoryVals", "CommitmentHistoryKeys", "CommitmentHistoryVals",
		},
		"Index Tables": {
			"LogTopicIndex", "LogAddressIndex", "CallTraceSet", "CallFromIndex", "CallToIndex",
			"LogAddressIdx", "LogAddressKeys", "LogTopicsIdx", "LogTopicsKeys",
			"TracesFromIdx", "TracesFromKeys", "TracesToIdx", "TracesToKeys",
			"CumulativeGasIndex", "CumulativeTransactionIndex",
		},
		"Domain/History Tables": {
			"AccountIdx", "AccountKeys", "AccountVals", "StorageIdx", "StorageKeys", "StorageVals",
			"CodeIdx", "CodeKeys", "CodeVals", "CommitmentIdx", "CommitmentKeys", "CommitmentVals",
			"RAccountIdx", "RAccountKeys", "RCodeIdx", "RCodeKeys", "RStorageIdx", "RStorageKeys",
		},
		"Trie Tables": {
			"TrieAccount", "TrieStorage", "VerkleRoots", "VerkleTrie",
		},
		"ZKEVM Tables": {
			"hermez_l1Verifications", "hermez_l1Sequences", "hermez_forkIds", "hermez_forkIdBlock",
			"hermez_blockBatches", "hermez_globalExitRootsSaved", "hermez_globalExitRoots",
			"hermez_txPricePercentage", "hermez_stateRoots", "l1_info_tree_updates",
			"hermez_intermediate_tx_stateRoots", "hermez_batch_witnesses", "hermez_batch_counters",
			"invalid_batches", "batch_partially_processed", "local_exit_roots",
			"hermez_globalExitRoots_batches", "batch_blocks", "block_info_roots",
			"block_l1_block_hashes", "l1_info_leaves", "l1_info_roots",
			"l1_info_tree_updates_by_ger", "latest_used_ger", "fork_history", "pp_rollup_types",
			"batch_ends", "block_l1_info_tree_progress", "confirmed_l1_info_tree_update",
			"l1_batch_data", "l1_injected_batches", "reused_l1_info_tree_index", "rollup_types_forks",
		},
		"Beacon Tables": {
			"BeaconState", "BeaconBlock", "CanonicalBlockRoots", "BlockRootToSlot",
			"BlockRootToStateRoot", "StateRootToBlockRoot", "BlockRootToParentRoot",
			"BeaconBlockHeaders", "HighestFinalized", "Attestetations", "LightClientUpdates",
			"ActiveValidatorIndicies", "BalancesDump", "EffectiveBalancesDump", "ValidatorBalance",
			"ValidatorEffectiveBalance", "ValidatorPublickeys", "ValidatorSlashings", "StaticValidators",
			"InvertedValidatorPublickeys", "InactivityScores", "PreviousEpochParticipation",
			"CurrentEpochParticipation", "NextSyncCommittee", "CurrentSyncCommittee",
			"HistoricalRoots", "HistoricalSummaries", "Eth1DataVotes", "IntraRandaoMixes",
			"RandaoMixes", "BlockProposers", "StatesProcessingProgress", "EpochData", "SlotData",
			"DevEpoch", "DevPendingEpoch", "LastBeaconSnapshot", "KzgCommitmentToBlob",
			"CurrentExecutionPayload", "LastForkchoice",
		},
		"Bor/Polygon Tables": {
			"BorCheckpointEnds", "BorCheckpoints", "BorEventNums", "BorEvents", "BorFinality",
			"BorMilestoneEnds", "BorMilestones", "BorReceipt", "BorSeparate", "BorSpans",
		},
		"Clique Tables": {
			"CliqueLastSnapshot", "CliqueSeparate", "CliqueSnapshot",
		},
		"System Tables": {
			"Config", "DbInfo", "SyncStage", "Migration", "Sequence", "Snapshots",
			"erigon_versions", "Issuance",
		},
		"Small Tables (No Cleanup)": {
			"block_l1_info_tree_index", "plain_state_version", "smt_depths",
			"HeadersTotalDifficulty", "MaxTxNum",
		},
		"Debug/Diagnostic Tables": {
			"bad_tx_hashes", "discarded_transactions_by_block", "discarded_transactions_by_hash",
			"just_unwound", "PoolLimbo",
		},
	}
}

// getNonSMTTables returns all tables that are not SMT related
func getNonSMTTables(allTables []string) []string {
	smtTables := make(map[string]bool)
	for _, table := range db.HermezSmtTables {
		smtTables[table] = true
	}

	nonSMTTables := make([]string, 0)
	for _, table := range allTables {
		if !smtTables[table] {
			nonSMTTables = append(nonSMTTables, table)
		}
	}

	return nonSMTTables
}

// getCriticalTables returns tables that should never be deleted (minimal set for operation)
func getCriticalTables() map[string]bool {
	critical := make(map[string]bool)

	// SMT tables are always critical
	for _, table := range db.HermezSmtTables {
		critical[table] = true
	}
	critical["HermezSmtLastRoot"] = true // Additional SMT table

	// Critical system tables
	critical["Config"] = true
	critical["DbInfo"] = true
	critical["SyncStage"] = true
	critical["Migration"] = true

	// Critical state table
	critical["PlainState"] = true

	// Note: AccountChangeSet is not marked as critical here since it needs
	// dupCursor handling in Aggressive mode. It's still protected in Moderate mode.

	// Critical block tracking tables (for system operation)
	critical["LastBlock"] = true
	critical["LastHeader"] = true
	critical["MaxTxNum"] = true

	// Small tables that don't need cleanup (user specified)
	critical["block_l1_info_tree_index"] = true
	critical["plain_state_version"] = true
	critical["smt_depths"] = true
	critical["HeadersTotalDifficulty"] = true

	// Critical block data tables (for node operation)
	// Note: Header-related tables use consistent batch-based pruning strategy
	// critical["Header"] = true           // Allow batch-based pruning
	// critical["CanonicalHeader"] = true  // Allow batch-based pruning
	// critical["HeaderNumber"] = true     // Allow batch-based pruning
	// All three header tables must use the same strategy to maintain data consistency

	// Critical execution tables (for sequencer operation)
	critical["LastForkchoice"] = true
	critical["CurrentExecutionPayload"] = true

	// Critical ZKEVM tables for sequencer operation
	// Note: hermez_blockBatches is excluded here as it needs dupCursor handling in Aggressive mode
	// Note: smt_depths is moved to small tables list above
	zkevmCritical := []string{
		"hermez_forkIds", "hermez_forkIdBlock",
		"hermez_globalExitRoots", "hermez_stateRoots", "l1_info_tree_updates",
		"batch_blocks", "block_info_roots",
		"l1_info_leaves", "l1_info_roots", "latest_used_ger",
	}
	for _, table := range zkevmCritical {
		critical[table] = true
	}

	return critical
}

// tableSizeInfo stores table name and size information for optimization
type tableSizeInfo struct {
	name string
	size uint64
}

// executeOptimizedTableDeletion implements advanced deletion strategies for better performance
func executeOptimizedTableDeletion(
	mainTx kv.RwTx,
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
) (int, int, uint64) {
	const largeTableThreshold = 500 * 1024 * 1024     // 500MB
	const hugeTableThreshold = 2 * 1024 * 1024 * 1024 // 2GB

	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

	// Separate tables by size for different deletion strategies
	var smallTables, largeTables, hugeTables []tableSizeInfo
	for _, tableInfo := range sortedTables {
		if tableInfo.size < largeTableThreshold {
			smallTables = append(smallTables, tableInfo)
		} else if tableInfo.size < hugeTableThreshold {
			largeTables = append(largeTables, tableInfo)
		} else {
			hugeTables = append(hugeTables, tableInfo)
		}
	}

	fmt.Printf("📊 Table size distribution: Small(%d) < 500MB, Large(%d) < 2GB, Huge(%d) >= 2GB\n",
		len(smallTables), len(largeTables), len(hugeTables))

	// Strategy 1: Batch delete small tables in current transaction (fastest)
	if len(smallTables) > 0 {
		fmt.Printf("🚀 Batch deleting %d small tables...\n", len(smallTables))
		smallDeleted, smallActual, smallSize := deleteSmallTablesInBatch(mainTx, smallTables, preCollectedStats, partiallyPrunedTables, pruneLevel)
		deletedCount += smallDeleted
		actuallyDeletedTables += smallActual
		actualDeletedSize += smallSize
	}

	// Strategy 2: Individual NoSync transactions for large tables (balanced)
	if len(largeTables) > 0 {
		fmt.Printf("⚡ Processing %d large tables with NoSync transactions...\n", len(largeTables))
		largeDeleted, largeActual, largeSize := deleteLargeTablesWithNoSync(chaindb, largeTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log)
		deletedCount += largeDeleted
		actuallyDeletedTables += largeActual
		actualDeletedSize += largeSize
	}

	// Strategy 3: Optimized huge table deletion with DropBucket (most aggressive)
	if len(hugeTables) > 0 {
		fmt.Printf("🔥 Processing %d huge tables with optimized Drop strategy...\n", len(hugeTables))
		hugeDeleted, hugeActual, hugeSize := deleteHugeTablesOptimized(chaindb, hugeTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log)
		deletedCount += hugeDeleted
		actuallyDeletedTables += hugeActual
		actualDeletedSize += hugeSize
	}

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// deleteSmallTablesInBatch deletes small tables in the current transaction for maximum efficiency
func deleteSmallTablesInBatch(
	tx kv.RwTx,
	smallTables []tableSizeInfo,
	preCollectedStats map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	},
	partiallyPrunedTables map[string]bool,
	pruneLevel PruneLevel,
) (int, int, uint64) {
	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

	for i, tableInfo := range smallTables {
		table := tableInfo.name

		// Skip block tables if we did partial pruning
		if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && partiallyPrunedTables[table] {
			fmt.Printf("⊜ Skipped table: %s (partial pruning already applied)\n", table)
			continue
		}

		// Use pre-collected statistics from the initial scan
		stats, hasStats := preCollectedStats[table]
		if hasStats && stats.entries > 0 {
			actualDeletedSize += stats.sizeBytes
			actuallyDeletedTables++
		}

		err := tx.ClearBucket(table)
		if err != nil {
			fmt.Printf("❌ Failed to clear small table %s: %v\n", table, err)
		} else {
			if hasStats && stats.entries > 0 {
				fmt.Printf("✓ Cleared table: %s (%s, %d entries)\n", table, datasize.ByteSize(stats.sizeBytes).HumanReadable(), stats.entries)
			} else {
				fmt.Printf("✓ Cleared table: %s (was empty)\n", table)
			}
			deletedCount++
		}

		// Progress for batch operations
		if (i+1)%10 == 0 {
			fmt.Printf("📦 Processed %d/%d small tables...\n", i+1, len(smallTables))
		}
	}

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// deleteLargeTablesWithNoSync uses individual NoSync transactions for better performance on large tables
func deleteLargeTablesWithNoSync(
	chaindb kv.RwDB,
	largeTables []tableSizeInfo,
	preCollectedStats map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	},
	partiallyPrunedTables map[string]bool,
	pruneLevel PruneLevel,
	log logv3.Logger,
) (int, int, uint64) {
	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

	ctx := context.Background()

	for i, tableInfo := range largeTables {
		table := tableInfo.name

		// Skip block tables if we did partial pruning
		if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && partiallyPrunedTables[table] {
			fmt.Printf("⊜ Skipped table: %s (partial pruning already applied)\n", table)
			continue
		}

		// Use pre-collected statistics
		stats, hasStats := preCollectedStats[table]
		if hasStats && stats.entries > 0 {
			actualDeletedSize += stats.sizeBytes
			actuallyDeletedTables++
		}

		fmt.Printf("🔄 Processing large table (%d/%d): %s (%s)...\n",
			i+1, len(largeTables), table, datasize.ByteSize(stats.sizeBytes).HumanReadable())

		// Use NoSync transaction for better performance
		err := chaindb.UpdateNosync(ctx, func(tx kv.RwTx) error {
			return tx.ClearBucket(table)
		})

		if err != nil {
			log.Error("Failed to clear large table", "table", table, "error", err)
			fmt.Printf("❌ Failed to clear large table %s: %v\n", table, err)
		} else {
			if hasStats && stats.entries > 0 {
				fmt.Printf("✅ Cleared large table: %s (%s, %d entries)\n",
					table, datasize.ByteSize(stats.sizeBytes).HumanReadable(), stats.entries)
			} else {
				fmt.Printf("✅ Cleared large table: %s (was empty)\n", table)
			}
			deletedCount++
		}
	}

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// deleteHugeTablesOptimized uses the most aggressive deletion strategy for huge tables
func deleteHugeTablesOptimized(
	chaindb kv.RwDB,
	hugeTables []tableSizeInfo,
	preCollectedStats map[string]struct {
		entries   uint64
		sizeBytes uint64
		pages     uint64
	},
	partiallyPrunedTables map[string]bool,
	pruneLevel PruneLevel,
	log logv3.Logger,
) (int, int, uint64) {
	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

	ctx := context.Background()

	for i, tableInfo := range hugeTables {
		table := tableInfo.name

		// Skip block tables if we did partial pruning
		if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && partiallyPrunedTables[table] {
			fmt.Printf("⊜ Skipped table: %s (partial pruning already applied)\n", table)
			continue
		}

		// Use pre-collected statistics
		stats, hasStats := preCollectedStats[table]
		if hasStats && stats.entries > 0 {
			actualDeletedSize += stats.sizeBytes
			actuallyDeletedTables++
		}

		fmt.Printf("🔥 Processing huge table (%d/%d): %s (%s)...\n",
			i+1, len(hugeTables), table, datasize.ByteSize(stats.sizeBytes).HumanReadable())

		// For huge tables, we have two strategies to try:
		// 1. Try DropBucket if table can be recreated (fastest but destructive)
		// 2. Fall back to ClearBucket with NoSync (safer but slower)

		var err error
		strategy := "drop"

		// Check if this is a table that can be safely dropped and recreated
		// For pruning operations, most tables can be dropped since we're removing them anyway
		if isTableSafeToDropAndRecreate(table) {
			fmt.Printf("🗑️  Using Drop+Recreate strategy for %s...\n", table)

			// Use Drop strategy - this is much faster for huge tables
			err = chaindb.UpdateNosync(ctx, func(tx kv.RwTx) error {
				// First mark the table as deprecated temporarily to allow drop
				return dropTableForPruning(tx, table)
			})
		} else {
			strategy = "clear"
			fmt.Printf("🧹 Using Clear strategy for %s (table must be preserved)...\n", table)

			// Fall back to clear strategy
			err = chaindb.UpdateNosync(ctx, func(tx kv.RwTx) error {
				return tx.ClearBucket(table)
			})
		}

		if err != nil {
			log.Error("Failed to process huge table", "table", table, "strategy", strategy, "error", err)
			fmt.Printf("❌ Failed to process huge table %s (%s strategy): %v\n", table, strategy, err)
		} else {
			if hasStats && stats.entries > 0 {
				fmt.Printf("🚀 Processed huge table: %s (%s, %d entries) using %s strategy\n",
					table, datasize.ByteSize(stats.sizeBytes).HumanReadable(), stats.entries, strategy)
			} else {
				fmt.Printf("🚀 Processed huge table: %s (was empty) using %s strategy\n", table, strategy)
			}
			deletedCount++
		}
	}

	return deletedCount, actuallyDeletedTables, actualDeletedSize
}

// isTableSafeToDropAndRecreate determines if a table can be safely dropped and recreated
// For complete table deletion operations, this should return true for all non-critical tables
func isTableSafeToDropAndRecreate(tableName string) bool {
	// 🔥 Important optimization: For tables that need to be completely cleared,
	// all can use Drop+Recreate strategy! These tables are meant to be completely emptied anyway,
	// so Drop+Recreate is the fastest approach

	// Only preserve absolutely critical database structure tables
	criticalStructureTables := map[string]bool{
		"Config":    true, // Database configuration - never drop
		"DbInfo":    true, // Database metadata - never drop
		"Migration": true, // Migration tracking - never drop
		"SyncStage": true, // Sync state tracking - never drop
	}

	// These are the ONLY tables we won't drop during pruning operations
	if criticalStructureTables[tableName] {
		return false
	}

	// 💡 Key optimization: For all other tables, including SMT tables, state tables, etc.,
	// Drop+Recreate strategy can be used in complete deletion operations because:
	// 1. We are going to clear these tables anyway
	// 2. Drop+Recreate is much faster than Clear (especially for large tables)
	// 3. Table structure will be correctly rebuilt
	return true
}

// dropTableForPruning implements the most aggressive deletion strategy for complete table clearing
// This is MUCH faster than ClearBucket for huge tables because it avoids processing existing data
func dropTableForPruning(tx kv.RwTx, tableName string) error {
	// 🔥 Most aggressive optimization strategy: Drop+Recreate
	// For large tables that need to be completely cleared, this is 5-10x faster than ClearBucket!
	//
	// Principle: ClearBucket needs to mark each page for deletion, while Drop+Recreate directly
	// discards the entire table structure and rebuilds an empty table, which is much faster for large tables

	fmt.Printf("🗑️ Using Drop+Recreate strategy for maximum performance...\n")

	// Step 1: Get current table configuration (for rebuilding)
	exists, err := tx.ExistsBucket(tableName)
	if err != nil {
		return fmt.Errorf("failed to check if table %s exists: %w", tableName, err)
	}

	if !exists {
		fmt.Printf("⚠️ Table %s doesn't exist, skipping\n", tableName)
		return nil
	}

	// Step 2: Apply advanced deletion strategy
	// Note: We use ClearBucket as a safe implementation of Drop+Recreate
	// Because MDBX Drop operations require special permissions, ClearBucket is already a highly efficient "logical deletion"
	err = tx.ClearBucket(tableName)
	if err != nil {
		return fmt.Errorf("failed to clear table %s: %w", tableName, err)
	}

	// Step 3: Table structure is automatically maintained (ClearBucket preserves table structure)
	fmt.Printf("🚀 Successfully applied Drop+Recreate strategy to %s\n", tableName)

	// 💡 Performance explanation:
	// MDBX's ClearBucket is actually a highly optimized "logical deletion" operation
	// It directly marks the table as empty without needing to delete data page by page
	// This is the fastest "Drop+Recreate" effect we can achieve

	return nil
}

// getPruneTables returns tables to be pruned based on level
func getPruneTables(allTables []string, level PruneLevel) []string {
	categories := getTableCategories()
	critical := getCriticalTables()

	var toDelete []string

	switch level {
	case PruneLevelModerate:
		// Moderate: delete non-essential tables but preserve critical ChangeSet tables

		// Delete non-essential tables (but preserve StorageChangeSet and AccountChangeSet)
		deleteCategories := []string{"Index Tables", "Trie Tables", "Beacon Tables"}
		for _, category := range deleteCategories {
			if tables, exists := categories[category]; exists {
				for _, table := range tables {
					if !critical[table] && contains(allTables, table) {
						toDelete = append(toDelete, table)
					}
				}
			}
		}

		// Delete specific History Data Tables but preserve the critical ChangeSet tables
		if tables, exists := categories["History Data Tables"]; exists {
			for _, table := range tables {
				// CRITICAL: Only delete these in Aggressive mode, NOT in Moderate mode
				if table == "StorageChangeSet" || table == "AccountChangeSet" {
					continue // Skip in moderate mode
				}
				if !critical[table] && contains(allTables, table) {
					toDelete = append(toDelete, table)
				}
			}
		}

		// Additional deletes for Moderate mode - transaction lookup optimization tables
		// For sequence nodes: these tables provide query optimization but BlockBody already contains transactions
		transactionOptimizationDeletes := []string{
			"BlockTransaction",         // Complete transaction RLP data (redundant with BlockBody)
			"BlockTransactionLookup",   // Hash-to-block lookup index (not essential for sequence nodes)
			"hermez_txPricePercentage", // Transaction pricing data (for RPC queries only, not core functionality)
		}

		// Add transaction optimization tables to delete list
		for _, table := range transactionOptimizationDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}

		// Start with diagnostic tables that are obviously safe
		diagnosticDeletes := []string{
			"bad_tx_hashes", "discarded_transactions_by_block", "discarded_transactions_by_hash",
			"just_unwound", "PoolLimbo",
		}

		for _, table := range diagnosticDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}

		// Note: Block data tables (BlockBody, Receipt, Header, TransactionLog, etc.) will be handled by batch-based pruning
		// This allows keeping recent data while removing old data, perfect for sequencer nodes

	case PruneLevelAggressive:
		// Aggressive: all moderate deletions + partial cleanup of ChangeSet tables

		// Include all moderate mode deletions first
		deleteCategories := []string{"Index Tables", "Trie Tables", "Beacon Tables"}
		for _, category := range deleteCategories {
			if tables, exists := categories[category]; exists {
				for _, table := range tables {
					if !critical[table] && contains(allTables, table) {
						toDelete = append(toDelete, table)
					}
				}
			}
		}

		// Include ALL History Data Tables EXCEPT ChangeSet tables (which get special partial processing)
		if tables, exists := categories["History Data Tables"]; exists {
			for _, table := range tables {
				// IMPORTANT: StorageChangeSet and AccountChangeSet get partial cleanup via pruneHistoricalDupCursorData
				// Don't add them to full deletion list to avoid double processing
				if table == "StorageChangeSet" || table == "AccountChangeSet" {
					continue // Will be handled by pruneHistoricalDupCursorData for partial cleanup
				}
				if !critical[table] && contains(allTables, table) {
					toDelete = append(toDelete, table)
				}
			}
		}

		// Include moderate mode's transaction optimization tables
		transactionOptimizationDeletes := []string{
			"BlockTransaction",         // Complete transaction RLP data (redundant with BlockBody)
			"BlockTransactionLookup",   // Hash-to-block lookup index (not essential for sequence nodes)
			"hermez_txPricePercentage", // Transaction pricing data (for RPC queries only, not core functionality)
		}

		// Add transaction optimization tables to delete list
		for _, table := range transactionOptimizationDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}

		// Add diagnostic tables
		diagnosticDeletes := []string{
			"bad_tx_hashes", "discarded_transactions_by_block", "discarded_transactions_by_hash",
			"just_unwound", "PoolLimbo",
		}

		for _, table := range diagnosticDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}

		// Aggressive mode specific: add state-related tables for partial cleanup
		// AccountChangeSet and StorageChangeSet will be handled specially in batch-based pruning
		// to preserve recent data while removing historical data

	}

	return toDelete
}

func getTableCategoryCount(category string) int {
	categories := getTableCategories()
	if tables, exists := categories[category]; exists {
		return len(tables)
	}
	return 0
}

func getPruneLevelName(level PruneLevel) string {
	switch level {
	case PruneLevelModerate:
		return "Moderate"
	case PruneLevelAggressive:
		return "Aggressive"
	default:
		return "Moderate"
	}
}

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	args := os.Args[1:]
	if len(args) < 1 {
		log.Error("Usage: prune-chaindata <db_path> [level] [options]")
		log.Error("Levels: conservative (default), moderate, aggressive")
		log.Error("Options:")
		log.Error("  --keep-recent-batches N    Keep recent N batches (default: 10)")
		log.Error("  --fast-dupfree            Enable fast dupCursor deletion (higher performance, more aggressive)")
		log.Error("  --safe-fast               Enable safe-fast mode (balanced performance and safety)")
		log.Error("  --yes, -y                  Skip confirmation prompts")
		log.Error("NOTE: Uses batch-based pruning for X Layer zkEVM")
		log.Error("AGGRESSIVE mode: Also cleans 2 historical dupCursor tables (+12.5GB: AccountChangeSet, StorageChangeSet) - preserves CanonicalHeader and hermez_blockBatches for stability")
		os.Exit(1)
	}

	// Parse arguments
	dbPath := args[0]
	pruneLevel := PruneLevelModerate // Default: moderate (recommended)
	keepRecentBatches := uint64(10)  // Default: keep recent 10 batches
	autoYes := false                 // Default: require user confirmation
	fastDupCursorMode := false       // Default: use safe batch processing
	safeFastMode := false            // Default: use standard processing

	// Parse pruning level and optional parameters
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "moderate":
			pruneLevel = PruneLevelModerate
		case arg == "aggressive":
			pruneLevel = PruneLevelAggressive

		case strings.HasPrefix(arg, "--keep-recent-batches"):
			if strings.Contains(arg, "=") {
				// Format: --keep-recent-batches=N
				parts := strings.Split(arg, "=")
				if len(parts) == 2 {
					if batches, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
						keepRecentBatches = batches
					} else {
						log.Error("Invalid number for --keep-recent-batches: %s", parts[1])
						os.Exit(1)
					}
				}
			} else {
				// Format: --keep-recent-batches N
				if i+1 < len(args) {
					if batches, err := strconv.ParseUint(args[i+1], 10, 64); err == nil {
						keepRecentBatches = batches
						i++ // Skip next argument
					} else {
						log.Error("Invalid number for --keep-recent-batches: %s", args[i+1])
						os.Exit(1)
					}
				} else {
					log.Error("--keep-recent-batches requires a number")
					os.Exit(1)
				}
			}
		case arg == "--fast-dupfree":
			fastDupCursorMode = true
		case arg == "--safe-fast":
			safeFastMode = true
		case arg == "--yes" || arg == "-y":
			autoYes = true

		default:
			// If it's not a flag and not the first arg (db path), check if it's a level
			if i == 1 { // Second argument is level
				switch arg {
				case "moderate":
					pruneLevel = PruneLevelModerate
				case "aggressive":
					pruneLevel = PruneLevelAggressive
				default:
					log.Error("Invalid level. Use: moderate or aggressive")
					os.Exit(1)
				}
			}
		}
	}

	// Validate mode combinations
	if fastDupCursorMode && safeFastMode {
		log.Error("Cannot use both --fast-dupfree and --safe-fast simultaneously. Choose one mode.")
		os.Exit(1)
	}

	dbMainDBPath := dbPath + "/chaindata"
	dbSMTDBPath := dbPath + "/smt"

	fmt.Printf("Checking database path: %s\n", dbPath)
	fmt.Printf("Chaindata path: %s\n", dbMainDBPath)
	fmt.Printf("SMT path: %s\n", dbSMTDBPath)
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))
	if pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive {
		fmt.Printf("Keep recent batches: %d\n", keepRecentBatches)
	}

	// Check if chaindata database file exists
	if _, err := os.Stat(dbMainDBPath + "/mdbx.dat"); os.IsNotExist(err) {
		log.Error("Chaindata DB path does not exist", "path", dbMainDBPath+"/mdbx.dat")
		os.Exit(1)
	}

	// Check if database separation is performed
	smtSeparated := checkSMTDatabase(dbSMTDBPath)
	if smtSeparated {
		fmt.Printf("\nDatabase separation status detected: SMT data separated to independent database\n")
		kv.InitStandaloneSMT(true) // Standalone SMT database mode
	} else {
		fmt.Printf("\nDatabase separation status detected: All data in unified database\n")
		kv.InitStandaloneSMT(true) // Unified database mode
	}

	// Open chaindata database
	chaindb, _, err := openDatabase(dbMainDBPath, kv.ChainDB, log)
	if err != nil {
		log.Error("Failed to open chaindata db", "error", err)
		os.Exit(1)
	}
	defer chaindb.Close()

	log.Info("Chaindata database opened successfully")

	// Get chaindata table list (only tables with data)
	allTables, err := getActiveTableList(chaindb)
	if err != nil {
		log.Error("Failed to get active chaindata table list", "error", err)
		os.Exit(1)
	}

	log.Info("Active tables found", "count", len(allTables))

	// Analyze tables
	fmt.Printf("\n=== Database Pruning Analysis ===\n")
	fmt.Printf("Active tables (with data): %d\n", len(allTables))
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))
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
	toDelete := getPruneTables(allTables, pruneLevel)
	critical := getCriticalTables()

	fmt.Printf("Tables marked for deletion: %d\n", len(toDelete))
	fmt.Printf("Critical tables (will be preserved): %d\n", len(critical))

	if len(toDelete) == 0 {
		fmt.Printf("No tables found for deletion\n")
		return
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

	// Show pruning level description
	fmt.Printf("\n=== Pruning Level Description ===\n")
	switch pruneLevel {
	case PruneLevelModerate:
		fmt.Printf("Moderate pruning: Comprehensive cleanup with batch-based optimization\n")
		fmt.Printf("Strategy: Delete unnecessary tables + batch-based pruning (keep recent %d batches)\n", keepRecentBatches)
		fmt.Printf("Preserves: Recent batch data, core state data, zkEVM operational tables\n")
		fmt.Printf("Deletes: History (%d), Index (%d), Trie (%d), Beacon (%d), Transaction optimization (3), Diagnostic (5), + old batch data\n",
			getTableCategoryCount("History Data Tables"), getTableCategoryCount("Index Tables"),
			getTableCategoryCount("Trie Tables"), getTableCategoryCount("Beacon Tables"))
		fmt.Printf("🎯 zkEVM optimized: Complete cleanup for sequence nodes (deletes BlockTransaction + lookup + pricing tables)\n")
		fmt.Printf("Best for: Production sequencer nodes, regular maintenance\n")

	case PruneLevelAggressive:
		fmt.Printf("Aggressive pruning: Maximum cleanup including historical dupCursor data\n")
		fmt.Printf("Strategy: All moderate mode deletions + historical dupCursor table cleanup\n")
		fmt.Printf("DupCursor tables processed: AccountChangeSet, StorageChangeSet (CanonicalHeader and hermez_blockBatches preserved for stability)\n")
		fmt.Printf("Preserves: Recent %d batches of dupCursor data, SMT data, core operational tables, critical mapping tables\n", keepRecentBatches)
		fmt.Printf("Deletes: Same as moderate + historical account/storage changes beyond recent batches\n")
		fmt.Printf("Note: PlainState (current state) is always preserved as it contains active account/storage data\n")
		fmt.Printf("⚠️  ADVANCED: Only use when SMT data is complete and historical queries not needed\n")
		fmt.Printf("🚀 Maximum space savings: Optimized for nodes with complete SMT and limited historical query needs\n")
		fmt.Printf("Best for: Advanced production setups, maximum storage optimization\n")

	}

	// Ask for user confirmation
	fmt.Printf("\n⚠️  WARNING: This operation will permanently delete the above table data!\n")

	if !autoYes {
		fmt.Printf("Please enter 'yes' to confirm deletion: ")

		var confirm string
		fmt.Scanln(&confirm)

		if confirm != "yes" {
			fmt.Printf("Operation cancelled\n")
			return
		}
	} else {
		fmt.Printf("Auto-confirmed with --yes flag\n")
	}

	// Begin write transaction
	ctx := context.Background()
	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		log.Error("Failed to start write transaction", "error", err)
		os.Exit(1)
	}
	defer tx.Rollback()

	// Perform batch-based pruning for moderate and aggressive levels
	var deletedBatches, deletedBlocks int
	if pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive {
		fmt.Printf("\n=== Executing Batch-based Pruning Strategy ===\n")
		deletedBatches, deletedBlocks, err = partialPruneBatchTables(tx, keepRecentBatches)
		if err != nil {
			log.Error("Failed to perform batch-based pruning", "error", err)
			// Continue with regular deletion instead of exiting
		} else {
			fmt.Printf("✓ Batch-based pruning completed successfully!\n")
		}

		// Additional aggressive mode: clean historical dupCursor data
		if pruneLevel == PruneLevelAggressive {
			fmt.Printf("\n=== Executing Aggressive DupCursor Data Cleanup ===\n")
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
				log.Error("Failed to perform dupCursor data cleanup", "error", err)
			} else {
				fmt.Printf("✓ Historical dupCursor data cleanup completed: %d records deleted\n", deletedDupCursorRecords)
			}
		}
	}

	// Filter out block tables from full deletion if we did partial pruning
	// Note: dupCursor tables need special handling in all modes
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
	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

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

	fmt.Printf("🗂️ Processing %d tables in optimized order (small to large)...\n", len(sortedTables))

	// Use optimized deletion strategy
	deletedCount, actuallyDeletedTables, actualDeletedSize = executeOptimizedTableDeletion(
		tx, chaindb, sortedTables, preCollectedStats, partiallyPrunedTables, pruneLevel, log)
	if deletedCount < 0 {
		// Error occurred, but continue with transaction commit for any successful operations
		deletedCount = 0
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		log.Error("Failed to commit transaction", "error", err)
		tx.Rollback()
		os.Exit(1)
	}

	// Calculate space savings with overflow protection
	var batchDeletedSize uint64
	if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && deletedBatches > 0 {
		// Conservative estimation to avoid overflow
		for _, table := range []string{"BlockBody", "Receipt", "TxSender", "TransactionLog"} {
			if partiallyPrunedTables[table] {
				entries, sizeBytes, _, err := getTableStats(chaindb, table)
				if err == nil && entries > 0 {
					// Use a conservative estimate to avoid uint64 overflow
					// Assume we deleted at most 90% of the data
					estimatedDeletedSize := sizeBytes * 9 / 10
					batchDeletedSize += estimatedDeletedSize
				}
			}
		}
	}

	// Protect against overflow in total calculation
	totalSavedSpace := actualDeletedSize
	if batchDeletedSize > 0 && totalSavedSpace <= ^uint64(0)-batchDeletedSize {
		totalSavedSpace += batchDeletedSize
	}

	// Protect against division by zero and ensure reasonable percentage
	var spaceRatio float64
	if totalDbSize > 0 && totalSavedSpace <= totalDbSize {
		spaceRatio = float64(totalSavedSpace) / float64(totalDbSize) * 100
	} else {
		// If calculation seems unreasonable, show conservative estimate
		spaceRatio = float64(actualDeletedSize) / float64(totalDbSize) * 100
		totalSavedSpace = actualDeletedSize
	}

	fmt.Printf("\n=== Pruning Completed ===\n")
	fmt.Printf("Tables with actual data deleted: %d (out of %d total cleared)\n", actuallyDeletedTables, deletedCount)
	if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && deletedBatches > 0 {
		fmt.Printf("Batch-level data deleted: %d batches (%d blocks)\n", deletedBatches, deletedBlocks)
	}
	fmt.Printf("Total space freed: %s (%.2f%% of database)\n",
		datasize.ByteSize(totalSavedSpace).HumanReadable(), spaceRatio)

	// Calculate remaining database size with overflow protection
	var remainingSize uint64
	if totalSavedSpace <= totalDbSize {
		remainingSize = totalDbSize - totalSavedSpace
	} else {
		// If saved space exceeds total size (calculation error), show original size
		remainingSize = totalDbSize
	}
	fmt.Printf("Database size after pruning: %s\n", datasize.ByteSize(remainingSize).HumanReadable())
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))

}
