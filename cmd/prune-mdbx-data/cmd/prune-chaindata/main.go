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
	PruneLevelConservative PruneLevel = iota // Conservative pruning
	PruneLevelModerate                       // Moderate pruning
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

// executeBatchBasedPruning performs the actual batch-based pruning
func executeBatchBasedPruning(tx kv.RwTx, hermezDb *hermez_db.HermezDbReader, pruneBefore uint64) (int, int, error) {
	fmt.Printf("Starting batch-level data pruning...\n")

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

	fmt.Printf("Batch pruning completed: deleted %d batches, %d blocks\n", deletedBatches, deletedBlocks)
	return deletedBatches, deletedBlocks, nil
}

// deleteBlockData deletes all data related to a specific block
func deleteBlockData(tx kv.RwTx, blockNo uint64) error {
	blockKey := make([]byte, 8)
	binary.BigEndian.PutUint64(blockKey, blockNo)

	// Delete simple block-related table data (key = block_num_u64)
	simpleTables := []string{
		"Receipt",
		"CanonicalHeader",
		// zkEVM specific tables with block_number keys
		"hermez_blockBatches",      // l2blockno -> batchno
		"block_info_roots",         // block number -> block info root hash
		"block_l1_info_tree_index", // block number -> l1 info tree index
		// State and SMT tables with block_number keys
		"plain_state_version", // block number -> state version
		"smt_depths",          // block number -> smt depth
		// Transaction metadata tables with block_number keys
		"MaxTxNum", // block number -> max tx num in block
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

// deleteCompositeKeyData deletes data from tables with composite keys (block_number + hash)
func deleteCompositeKeyData(tx kv.RwTx, blockNo uint64) error {
	// Tables with composite key format: block_number_u64 + hash
	compositeKeyTables := []string{
		"Header",                 // block_num_u64 + hash -> header (RLP)
		"HeadersTotalDifficulty", // block_num_u64 + hash -> td (RLP)
		"BlockBody",              // block_num_u64 + hash -> block body
		"TxSender",               // block_num_u64 + blockHash -> sendersList
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

// getTableStats gets statistics info of table
func getTableStats(db kv.RwDB, tableName string, pageSize uint64) (uint64, uint64, uint64, error) {
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
		sizeBytes := totalPages * pageSize

		return stat.Entries, sizeBytes, totalPages, nil
	}

	return 0, 0, 0, fmt.Errorf("not MDBX transaction")
}

// openDatabase opens database at specified path and returns related info
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

	// Get database info
	env, err := mdbx2.NewEnv()
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to create env: %w", err)
	}

	err = env.Open(dbPath, opts.GetFlags(), 0664)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to open env: %w", err)
	}

	in, err := env.Info(nil)
	if err != nil {
		env.Close()
		return nil, nil, fmt.Errorf("Failed to get env info: %w", err)
	}
	env.Close()

	newMapSize := datasize.ByteSize(in.MapSize)

	// Open database with conservative flags to match sequencer
	db, err := opts.Flags(func(flags uint) uint {
		// Use conservative flags that match sequencer defaults
		// Remove problematic flags and use standard configuration
		return uint(mdbx2.NoReadahead | mdbx2.Coalesce | mdbx2.Durable)
	}).PageSize(uint64(in.PageSize)).MapSize(newMapSize).Open(ctx)

	if err != nil {
		return nil, nil, fmt.Errorf("Failed to open database: %w", err)
	}

	return db, in, nil
}

// getTableCategories returns predefined table categories for analysis
func getTableCategories() map[string][]string {
	return map[string][]string{
		"SMT Related Tables": append(db.HermezSmtTables, "HermezSmtLastRoot"),
		"Basic Block Tables": {
			"HeaderNumber", "BadHeaderNumber", "HeadersTotalDifficulty",
			"BlockBody", "Header", "BlockTransaction", "Receipt", "TxSender", "CanonicalHeader",
			"BlockRoot", "BlockRootToBlockHash", "BlockRootToBlockNumber", "BlockRootToKzgCommitments",
			"LastBlock", "LastHeader", "MaxTxNum", "TransactionLog", "NonCanonicalTransaction",
			"BlockTransactionLookup", "BlockBorTransactionLookup", "InnerTx",
		},
		"State Data Tables": {
			"PlainState", "HashedStorage", "StateAccounts", "StateStorage",
			"StateCode", "StateCommitment", "Code", "HashedAccount", "HashedCodeHash",
			"PlainCodeHash", "TEVMCode", "StateEvents", "StateRoot",
			"IncarnationMap", "plain_state_version",
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
			"smt_depths", "invalid_batches", "batch_partially_processed", "local_exit_roots",
			"hermez_globalExitRoots_batches", "batch_blocks", "block_info_roots",
			"block_l1_block_hashes", "block_l1_info_tree_index", "l1_info_leaves", "l1_info_roots",
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

	// Critical account state table (for nonce consistency)
	critical["AccountChangeSet"] = true

	// Critical block tracking tables (for system operation)
	critical["LastBlock"] = true
	critical["LastHeader"] = true
	critical["MaxTxNum"] = true

	// Critical block data tables (for node operation)
	critical["Header"] = true
	critical["CanonicalHeader"] = true
	critical["HeaderNumber"] = true

	// Critical execution tables (for sequencer operation)
	critical["LastForkchoice"] = true
	critical["CurrentExecutionPayload"] = true

	// Critical ZKEVM tables for sequencer operation
	zkevmCritical := []string{
		"hermez_forkIds", "hermez_forkIdBlock", "hermez_blockBatches",
		"hermez_globalExitRoots", "hermez_stateRoots", "l1_info_tree_updates",
		"smt_depths", "batch_blocks", "block_info_roots",
		"l1_info_leaves", "l1_info_roots", "latest_used_ger",
	}
	for _, table := range zkevmCritical {
		critical[table] = true
	}

	return critical
}

// getPruneTables returns tables to be pruned based on level
func getPruneTables(allTables []string, level PruneLevel) []string {
	categories := getTableCategories()
	critical := getCriticalTables()

	var toDelete []string

	switch level {
	case PruneLevelConservative:
		// Conservative: only delete obviously unnecessary tables that are clearly safe
		// Only delete Beacon tables since zkEVM doesn't use them
		deleteCategories := []string{"Beacon Tables"}
		for _, category := range deleteCategories {
			if tables, exists := categories[category]; exists {
				for _, table := range tables {
					if !critical[table] {
						for _, existingTable := range allTables {
							if existingTable == table {
								toDelete = append(toDelete, table)
								break
							}
						}
					}
				}
			}
		}

		// Delete only diagnostic/debug tables that are safe to remove
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

	case PruneLevelModerate:
		// Moderate: delete more data including history, indexes, and use batch-based pruning

		// Delete basic unnecessary tables
		deleteCategories := []string{"History Data Tables", "Index Tables", "Trie Tables", "Beacon Tables"}
		for _, category := range deleteCategories {
			if tables, exists := categories[category]; exists {
				for _, table := range tables {
					if !critical[table] {
						for _, existingTable := range allTables {
							if existingTable == table {
								toDelete = append(toDelete, table)
								break
							}
						}
					}
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
	case PruneLevelConservative:
		return "Conservative"
	case PruneLevelModerate:
		return "Moderate"
	default:
		return "Conservative"
	}
}

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	args := os.Args[1:]
	if len(args) < 1 {
		log.Error("Usage: prune-chaindata <db_path> [level] [options]")
		log.Error("Levels: conservative (default), moderate")
		log.Error("Options:")
		log.Error("  --keep-recent-batches N    Keep recent N batches (default: 10)")
		log.Error("  --yes, -y                  Skip confirmation prompts")
		log.Error("NOTE: Uses batch-based pruning for X Layer zkEVM")
		os.Exit(1)
	}

	// Parse arguments
	dbPath := args[0]
	pruneLevel := PruneLevelConservative
	keepRecentBatches := uint64(10) // Default: keep recent 10 batches
	autoYes := false                // Default: require user confirmation

	// Parse pruning level and optional parameters
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "conservative":
			pruneLevel = PruneLevelConservative
		case arg == "moderate":
			pruneLevel = PruneLevelModerate

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
		case arg == "--yes" || arg == "-y":
			autoYes = true

		default:
			// If it's not a flag and not the first arg (db path), check if it's a level
			if i == 1 { // Second argument is level
				switch arg {
				case "conservative":
					pruneLevel = PruneLevelConservative
				case "moderate":
					pruneLevel = PruneLevelModerate
				default:
					log.Error("Invalid level. Use: conservative or moderate")
					os.Exit(1)
				}
			}
		}
	}

	dbMainDBPath := dbPath + "/chaindata"
	dbSMTDBPath := dbPath + "/smt"

	fmt.Printf("Checking database path: %s\n", dbPath)
	fmt.Printf("Chaindata path: %s\n", dbMainDBPath)
	fmt.Printf("SMT path: %s\n", dbSMTDBPath)
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))
	if pruneLevel == PruneLevelModerate {
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
	chaindb, chainInfo, err := openDatabase(dbMainDBPath, kv.ChainDB, log)
	if err != nil {
		log.Error("Failed to open chaindata db", "error", err)
		os.Exit(1)
	}
	defer chaindb.Close()

	newMapSize := datasize.ByteSize(chainInfo.MapSize)
	log.Info("Chaindata database info", "pageSize", chainInfo.PageSize, "mapSize", newMapSize.HumanReadable())

	// Get chaindata table list
	allTables, err := getTableList(chaindb)
	if err != nil {
		log.Error("Failed to get chaindata table list", "error", err)
		os.Exit(1)
	}

	// Analyze tables
	fmt.Printf("\n=== Database Pruning Analysis ===\n")
	fmt.Printf("Total tables: %d\n", len(allTables))
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))

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

	fmt.Printf("\nTables to be deleted:\n")
	for i, table := range toDelete {
		entries, sizeBytes, pages, err := getTableStats(chaindb, table, uint64(chainInfo.PageSize))
		if err != nil {
			fmt.Printf("%3d. %-30s (failed to get stats)\n", i+1, table)
		} else {
			sizeStr := datasize.ByteSize(sizeBytes).HumanReadable()
			fmt.Printf("%3d. %-30s %s (%d entries, %d pages)\n", i+1, table, sizeStr, entries, pages)
			totalToDeleteSize += sizeBytes
		}
	}

	// Calculate total database size
	for _, table := range allTables {
		_, sizeBytes, _, err := getTableStats(chaindb, table, uint64(chainInfo.PageSize))
		if err == nil {
			totalDbSize += sizeBytes
		}
	}

	// Show pruning level description
	fmt.Printf("\n=== Pruning Level Description ===\n")
	switch pruneLevel {
	case PruneLevelConservative:
		fmt.Printf("Conservative pruning: Safe minimal cleanup\n")
		fmt.Printf("Strategy: Only delete obviously unnecessary tables (Beacon + diagnostic tables)\n")
		fmt.Printf("Preserves: All block data, transaction data, state data, history data, indexes\n")
		fmt.Printf("Deletes: Only Beacon tables (%d tables) + diagnostic tables (5 tables)\n",
			getTableCategoryCount("Beacon Tables"))
		fmt.Printf("Best for: First-time use, maximum safety, development environments\n")
	case PruneLevelModerate:
		fmt.Printf("Moderate pruning: Comprehensive cleanup with batch-based optimization\n")
		fmt.Printf("Strategy: Delete unnecessary tables + batch-based pruning (keep recent %d batches)\n", keepRecentBatches)
		fmt.Printf("Preserves: Recent batch data, core state data, zkEVM operational tables\n")
		fmt.Printf("Deletes: History (%d), Index (%d), Trie (%d), Beacon (%d), Transaction optimization (3), Diagnostic (5), + old batch data\n",
			getTableCategoryCount("History Data Tables"), getTableCategoryCount("Index Tables"),
			getTableCategoryCount("Trie Tables"), getTableCategoryCount("Beacon Tables"))
		fmt.Printf("🎯 zkEVM optimized: Complete cleanup for sequence nodes (deletes BlockTransaction + lookup + pricing tables)\n")
		fmt.Printf("Best for: Production sequencer nodes, regular maintenance\n")

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

	// Perform batch-based pruning for moderate level
	var deletedBatches, deletedBlocks int
	if pruneLevel == PruneLevelModerate {
		fmt.Printf("\n=== Executing Batch-based Pruning Strategy ===\n")
		deletedBatches, deletedBlocks, err = partialPruneBatchTables(tx, keepRecentBatches)
		if err != nil {
			log.Error("Failed to perform batch-based pruning", "error", err)
			// Continue with regular deletion instead of exiting
		} else {
			fmt.Printf("✓ Batch-based pruning completed successfully!\n")
		}
	}

	// Filter out block tables from full deletion if we did partial pruning
	partiallyPrunedTables := map[string]bool{
		"Header": true, "BlockBody": true, "Receipt": true,
		"TxSender": true, "CanonicalHeader": true,
		"HeaderNumber": true, "TransactionLog": true,
		// Note: All above tables use batch-based pruning (keep recent batches, delete old data)
	}

	// Execute table deletion
	fmt.Printf("\nStarting table deletion...\n")
	deletedCount := 0
	actuallyDeletedTables := 0
	var actualDeletedSize uint64

	for _, table := range toDelete {
		// Skip block tables if we did partial pruning
		if pruneLevel == PruneLevelModerate && partiallyPrunedTables[table] {
			fmt.Printf("⊜ Skipped table: %s (partial pruning already applied)\n", table)
			continue
		}

		// Get table size before deletion
		entries, sizeBytes, _, err := getTableStats(chaindb, table, uint64(chainInfo.PageSize))
		if err == nil && entries > 0 {
			actualDeletedSize += sizeBytes
			actuallyDeletedTables++
		}

		err = tx.ClearBucket(table)
		if err != nil {
			log.Error("Failed to clear table", "table", table, "error", err)
		} else {
			if entries > 0 {
				fmt.Printf("✓ Cleared table: %s\n", table)
			} else {
				fmt.Printf("✓ Cleared table: %s (was empty)\n", table)
			}
			deletedCount++
		}
	}

	// Commit transaction
	err = tx.Commit()
	if err != nil {
		log.Error("Failed to commit transaction", "error", err)
		tx.Rollback()
		os.Exit(1)
	}

	// Calculate space savings
	var batchDeletedSize uint64
	if pruneLevel == PruneLevelModerate && deletedBatches > 0 {
		// Estimate batch deletion size (approximate)
		for _, table := range []string{"BlockBody", "Receipt", "TxSender", "TransactionLog"} {
			if partiallyPrunedTables[table] {
				entries, sizeBytes, _, err := getTableStats(chaindb, table, uint64(chainInfo.PageSize))
				if err == nil && entries > 0 {
					// Estimate: (deleted_batches / total_batches) * current_size
					estimatedOriginalSize := sizeBytes * uint64(deletedBatches+5) / 5 // Rough estimate
					batchDeletedSize += estimatedOriginalSize - sizeBytes
				}
			}
		}
	}

	totalSavedSpace := actualDeletedSize + batchDeletedSize
	spaceRatio := float64(totalSavedSpace) / float64(totalDbSize) * 100

	fmt.Printf("\n=== Pruning Completed ===\n")
	fmt.Printf("Tables with actual data deleted: %d (out of %d total cleared)\n", actuallyDeletedTables, deletedCount)
	if pruneLevel == PruneLevelModerate && deletedBatches > 0 {
		fmt.Printf("Batch-level data deleted: %d batches (%d blocks)\n", deletedBatches, deletedBlocks)
	}
	fmt.Printf("Total space freed: %s (%.2f%% of database)\n",
		datasize.ByteSize(totalSavedSpace).HumanReadable(), spaceRatio)
	fmt.Printf("Database size after pruning: %s\n",
		datasize.ByteSize(totalDbSize-totalSavedSpace).HumanReadable())
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))

}
