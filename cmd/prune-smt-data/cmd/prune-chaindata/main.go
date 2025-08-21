package main

import (
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
	"github.com/ledgerwatch/erigon-lib/kv/mdbx"
	mdbxpkg "github.com/ledgerwatch/erigon-lib/kv/mdbx"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/smt/pkg/db"

	logv3 "github.com/ledgerwatch/log/v3"
)

// Define pruning levels
type PruneLevel int

const (
	PruneLevelConservative PruneLevel = iota // Conservative pruning
	PruneLevelModerate                       // Moderate pruning
	PruneLevelAggressive                     // Aggressive pruning
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

// extractBlockNumberFromKey extracts block number from table key based on table structure
func extractBlockNumberFromKey(key []byte, tableName string) (uint64, error) {
	switch tableName {
	case "Header", "BlockBody", "Receipt", "TxSender", "CanonicalHeader":
		// These tables use block number (8 bytes, big endian) as key
		if len(key) < 8 {
			return 0, fmt.Errorf("key too short for %s: %d bytes", tableName, len(key))
		}
		return binary.BigEndian.Uint64(key[:8]), nil

	case "TransactionLog":
		// Format: blockNum(8) + txIndex(4) + logIndex(4)
		if len(key) < 8 {
			return 0, fmt.Errorf("key too short for %s: %d bytes", tableName, len(key))
		}
		return binary.BigEndian.Uint64(key[:8]), nil

	default:
		return 0, fmt.Errorf("unsupported table for block number extraction: %s", tableName)
	}
}

// partialPruneHeaderNumberTable handles HeaderNumber table which has value-based block numbers
func partialPruneHeaderNumberTable(tx kv.RwTx, pruneBeforeBlock uint64) error {
	cursor, err := tx.RwCursor("HeaderNumber")
	if err != nil {
		return fmt.Errorf("failed to open HeaderNumber cursor: %w", err)
	}
	defer cursor.Close()

	var keysToDelete [][]byte

	for key, value, err := cursor.First(); key != nil; key, value, err = cursor.Next() {
		if err != nil {
			return fmt.Errorf("failed to iterate HeaderNumber: %w", err)
		}

		// HeaderNumber value is block number (8 bytes, big endian)
		if len(value) >= 8 {
			blockNumber := binary.BigEndian.Uint64(value[:8])
			if blockNumber < pruneBeforeBlock {
				// Make a copy of the key
				keysCopy := make([]byte, len(key))
				copy(keysCopy, key)
				keysToDelete = append(keysToDelete, keysCopy)
			}
		}
	}

	// Delete collected keys
	for _, key := range keysToDelete {
		key2, _, err := cursor.SeekExact(key)
		if err != nil || key2 == nil {
			return fmt.Errorf("failed to seek to key for deletion: %w", err)
		}
		if err := cursor.DeleteCurrent(); err != nil {
			return fmt.Errorf("failed to delete HeaderNumber entry: %w", err)
		}
	}

	fmt.Printf("    HeaderNumber: deleted %d entries before block %d\n", len(keysToDelete), pruneBeforeBlock)
	return nil
}

// partialPruneTable performs partial pruning on a table, keeping recent blocks
func partialPruneTable(tx kv.RwTx, tableName string, pruneBeforeBlock uint64) error {
	cursor, err := tx.RwCursor(tableName)
	if err != nil {
		return fmt.Errorf("failed to open %s cursor: %w", tableName, err)
	}
	defer cursor.Close()

	var keysToDelete [][]byte

	// First pass: collect keys to delete
	for key, _, err := cursor.First(); key != nil; key, _, err = cursor.Next() {
		if err != nil {
			return fmt.Errorf("failed to iterate %s: %w", tableName, err)
		}

		blockNumber, err := extractBlockNumberFromKey(key, tableName)
		if err != nil {
			// Skip entries we can't parse
			continue
		}

		if blockNumber < pruneBeforeBlock {
			// Make a copy of the key
			keysCopy := make([]byte, len(key))
			copy(keysCopy, key)
			keysToDelete = append(keysToDelete, keysCopy)
		}
	}

	// Second pass: delete collected keys
	for _, key := range keysToDelete {
		key2, _, err := cursor.SeekExact(key)
		if err != nil || key2 == nil {
			return fmt.Errorf("failed to seek to key for deletion: %w", err)
		}
		if err := cursor.DeleteCurrent(); err != nil {
			return fmt.Errorf("failed to delete %s entry: %w", tableName, err)
		}
	}

	fmt.Printf("    %s: deleted %d entries before block %d\n", tableName, len(keysToDelete), pruneBeforeBlock)
	return nil
}

// partialPruneBlockTables performs partial pruning on block-related tables
func partialPruneBlockTables(tx kv.RwTx, keepRecentBlocks uint64) error {
	latestBlock, err := getLatestBlockNumber(tx)
	if err != nil {
		return fmt.Errorf("failed to get latest block number: %w", err)
	}

	fmt.Printf("Latest block number: %d\n", latestBlock)
	fmt.Printf("Keeping recent %d blocks\n", keepRecentBlocks)

	var pruneBeforeBlock uint64
	if latestBlock > keepRecentBlocks {
		pruneBeforeBlock = latestBlock - keepRecentBlocks + 1
	} else {
		pruneBeforeBlock = 0
	}

	fmt.Printf("Will delete data for blocks < %d\n", pruneBeforeBlock)

	if pruneBeforeBlock == 0 {
		fmt.Printf("No blocks to prune (total blocks <= keep recent blocks)\n")
		return nil
	}

	// Tables that support partial pruning (key-based block number)
	partialPruneTables := []string{
		"Header", "BlockBody", "Receipt", "TxSender", "CanonicalHeader", "TransactionLog",
	}

	// Process regular tables
	for _, tableName := range partialPruneTables {
		err := partialPruneTable(tx, tableName, pruneBeforeBlock)
		if err != nil {
			fmt.Printf("    Warning: failed to partially prune %s: %v\n", tableName, err)
			continue
		}
	}

	// Special handling for HeaderNumber (value-based block number)
	err = partialPruneHeaderNumberTable(tx, pruneBeforeBlock)
	if err != nil {
		fmt.Printf("    Warning: failed to partially prune HeaderNumber: %v\n", err)
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

	var opts mdbx.MdbxOpts
	if label == kv.ChainDB {
		opts = mdbx.NewMDBX(log).Path(dbPath).Label(label).WithTableCfg(mdbx.WithChaindataTables)
	} else {
		// SMT database uses different configuration
		kv.InitStandaloneSMT(false) // Standalone SMT database
		opts = mdbx.NewMDBX(log).Path(dbPath).Label(label)
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

	// Open database
	db, err := opts.Flags(func(flags uint) uint {
		newFlags := int(in.Flags)
		newFlags &= ^mdbx2.Readonly
		newFlags |= mdbx2.WriteMap
		return uint(newFlags)
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
		// Conservative: only delete obviously unnecessary tables
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

	case PruneLevelModerate:
		// Moderate: delete more tables but keep block data for node operation
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

		// Also delete some state data tables
		additionalDeletes := []string{
			"HashedStorage", "StateAccounts", "StateStorage", "StateCode", "StateCommitment",
			"HashedAccount", "HashedCodeHash", "PlainCodeHash", "TEVMCode",
		}

		// Delete non-critical block data tables (方案3)
		blockDataDeletes := []string{
			"BlockBody", "Receipt", "TxSender", "TransactionLog",
			"BlockTransaction", "BlockTransactionLookup",
		}
		for _, table := range additionalDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}
		for _, table := range blockDataDeletes {
			if !critical[table] {
				for _, existingTable := range allTables {
					if existingTable == table {
						toDelete = append(toDelete, table)
						break
					}
				}
			}
		}

	case PruneLevelAggressive:
		// Aggressive: delete all non-critical tables
		for _, table := range allTables {
			if !critical[table] {
				toDelete = append(toDelete, table)
			}
		}
	}

	return toDelete
}

func getPruneLevelName(level PruneLevel) string {
	switch level {
	case PruneLevelConservative:
		return "Conservative"
	case PruneLevelModerate:
		return "Moderate"
	case PruneLevelAggressive:
		return "Aggressive"
	default:
		return "Conservative"
	}
}

func main() {
	log := logv3.New()
	log.SetHandler(logv3.LvlFilterHandler(logv3.LvlInfo, logv3.StdoutHandler))

	args := os.Args[1:]
	if len(args) < 1 {
		log.Error("Usage: prune-chaindata <db_path> [level] [--keep-recent-blocks N]")
		log.Error("Levels: conservative (default), moderate, aggressive")
		log.Error("Options: --keep-recent-blocks N (default: 100, for moderate/aggressive)")
		os.Exit(1)
	}

	// Parse arguments
	dbPath := args[0]
	pruneLevel := PruneLevelConservative
	keepRecentBlocks := uint64(100) // Default: keep recent 100 blocks

	// Parse pruning level and optional parameters
	for i := 1; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "conservative":
			pruneLevel = PruneLevelConservative
		case arg == "moderate":
			pruneLevel = PruneLevelModerate
		case arg == "aggressive":
			pruneLevel = PruneLevelAggressive
		case strings.HasPrefix(arg, "--keep-recent-blocks"):
			if strings.Contains(arg, "=") {
				// Format: --keep-recent-blocks=N
				parts := strings.Split(arg, "=")
				if len(parts) == 2 {
					if blocks, err := strconv.ParseUint(parts[1], 10, 64); err == nil {
						keepRecentBlocks = blocks
					} else {
						log.Error("Invalid number for --keep-recent-blocks: %s", parts[1])
						os.Exit(1)
					}
				}
			} else {
				// Format: --keep-recent-blocks N
				if i+1 < len(args) {
					if blocks, err := strconv.ParseUint(args[i+1], 10, 64); err == nil {
						keepRecentBlocks = blocks
						i++ // Skip next argument
					} else {
						log.Error("Invalid number for --keep-recent-blocks: %s", args[i+1])
						os.Exit(1)
					}
				} else {
					log.Error("--keep-recent-blocks requires a number")
					os.Exit(1)
				}
			}
		default:
			// If it's not a flag and not the first arg (db path), check if it's a level
			if i == 1 { // Second argument is level
				switch arg {
				case "conservative":
					pruneLevel = PruneLevelConservative
				case "moderate":
					pruneLevel = PruneLevelModerate
				case "aggressive":
					pruneLevel = PruneLevelAggressive
				default:
					log.Error("Invalid level. Use: conservative, moderate, or aggressive")
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
	if pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive {
		fmt.Printf("Keep recent blocks: %d\n", keepRecentBlocks)
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

	fmt.Printf("\nTables to be deleted:\n")
	for i, table := range toDelete {
		entries, sizeBytes, pages, err := getTableStats(chaindb, table, uint64(chainInfo.PageSize))
		if err != nil {
			fmt.Printf("%3d. %-30s (failed to get stats)\n", i+1, table)
		} else {
			sizeStr := datasize.ByteSize(sizeBytes).HumanReadable()
			fmt.Printf("%3d. %-30s %s (%d entries, %d pages)\n", i+1, table, sizeStr, entries, pages)
		}
	}

	// Show pruning level description
	fmt.Printf("\n=== Pruning Level Description ===\n")
	switch pruneLevel {
	case PruneLevelConservative:
		fmt.Printf("Conservative pruning: Only delete obviously unnecessary tables\n")
		fmt.Printf("Preserves: Block data, transaction data, state data (for debugging and compatibility)\n")
		fmt.Printf("Deletes: History data, indexes, Trie, Beacon tables\n")
	case PruneLevelModerate:
		fmt.Printf("Moderate pruning: Delete more unnecessary tables but keep essential data\n")
		fmt.Printf("Preserves: Core state data, sync progress, ZKEVM data, some block data\n")
		fmt.Printf("Deletes: History data, indexes, some state tables\n")
	case PruneLevelAggressive:
		fmt.Printf("Aggressive pruning: Delete almost all tables, keep only operational necessities\n")
		fmt.Printf("Preserves: PlainState (current state), sync progress, critical ZKEVM data\n")
		fmt.Printf("Deletes: All block data, transaction data, most state data\n")
		fmt.Printf("⚠️  WARNING: Aggressive pruning will delete all block and transaction data!\n")
	}

	// Ask for user confirmation
	fmt.Printf("\n⚠️  WARNING: This operation will permanently delete the above table data!\n")
	if pruneLevel == PruneLevelAggressive {
		fmt.Printf("⚠️  Aggressive pruning will delete all block, transaction, and state data - cannot be recovered!\n")
	}
	fmt.Printf("Please enter 'yes' to confirm deletion: ")

	var confirm string
	fmt.Scanln(&confirm)

	if confirm != "yes" {
		fmt.Printf("Operation cancelled\n")
		return
	}

	// Begin write transaction
	ctx := context.Background()
	tx, err := chaindb.BeginRw(ctx)
	if err != nil {
		log.Error("Failed to start write transaction", "error", err)
		os.Exit(1)
	}
	defer tx.Rollback()

	// Perform partial pruning for moderate/aggressive levels first
	if pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive {
		fmt.Printf("\nPerforming partial pruning of block tables...\n")
		err = partialPruneBlockTables(tx, keepRecentBlocks)
		if err != nil {
			log.Error("Failed to perform partial block pruning", "error", err)
			// Continue with regular deletion instead of exiting
		}
	}

	// Filter out block tables from full deletion if we did partial pruning
	partiallyPrunedTables := map[string]bool{
		"Header": true, "BlockBody": true, "Receipt": true,
		"TxSender": true, "CanonicalHeader": true,
		"TransactionLog": true, "HeaderNumber": true,
	}

	// Execute table deletion
	fmt.Printf("\nStarting table deletion...\n")
	deletedCount := 0

	for _, table := range toDelete {
		// Skip block tables if we did partial pruning
		if (pruneLevel == PruneLevelModerate || pruneLevel == PruneLevelAggressive) && partiallyPrunedTables[table] {
			fmt.Printf("⊜ Skipped table: %s (partial pruning already applied)\n", table)
			continue
		}

		err = tx.ClearBucket(table)
		if err != nil {
			log.Error("Failed to clear table", "table", table, "error", err)
		} else {
			fmt.Printf("✓ Cleared table: %s\n", table)
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

	fmt.Printf("\n=== Pruning Completed ===\n")
	fmt.Printf("Successfully cleared %d tables\n", deletedCount)
	fmt.Printf("Remaining tables: %d\n", len(allTables)-deletedCount)
	fmt.Printf("Pruning level: %s\n", getPruneLevelName(pruneLevel))

	if pruneLevel == PruneLevelAggressive {
		fmt.Printf("\nNote: Aggressive pruning has cleared all block, transaction, and state data!\n")
		fmt.Printf("Only essential operational data remains for sequencer operation.\n")
	}
}
