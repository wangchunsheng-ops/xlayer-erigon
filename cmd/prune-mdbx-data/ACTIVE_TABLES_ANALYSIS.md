# Active Tables Analysis for X Layer zkEVM

This document analyzes all tables with non-zero size in the X Layer zkEVM database and explains how each is handled during the pruning process.

## Database Overview

Based on real mainnet data analysis:
- **Chaindata Database**: 70.0 GB table data, 75.7 GB actual size (5.7 GB difference)
- **SMT Database**: 57.0 GB table data, 105.5 GB actual size (48.5 GB difference)

## Chaindata Database - Active Tables Analysis

**Column Legend:**
- **Moderate**: ✅ = Fully deleted, 🔄 = Batch-based partial pruning, 🛡️ = Protected  
- **Aggressive**: ✅ = Fully deleted, 🔄 = Batch-based partial pruning, ✅* = Historical data only, 🛡️ = Protected

**🔄 Important Note**: Batch-based pruning (🔄) means the table is NOT fully deleted, but rather **partially cleaned** by removing old batch data while preserving recent batches.

**💡 Safe Alternative**: For users seeking zero-risk cleanup, use `compact-db -in-place` which provides 30-35% space savings without any data deletion.

### 🔥 Large Tables (>1GB) - Primary Targets

| Table Name | Size | Description | Moderate | Aggressive |
|------------|------|-------------|----------|------------|
| **Header** | 17.1 GB | Block headers with full metadata | 🔄 | 🔄 |
| **StorageChangeSet** | 10.0 GB | Historical storage state changes | 🛡️ | ✅* |
| **StorageHistory** | 6.6 GB | Storage change history index | 🛡️ | 🛡️ |
| **BlockTransaction** | 5.2 GB | Complete transaction RLP data | ✅ | ✅ |
| **TransactionLog** | 4.8 GB | Transaction execution logs | 🔄 | 🔄 |
| **PlainState** | 4.4 GB | Current account/storage state | 🛡️ | 🛡️ |
| **HashedStorage** | 4.0 GB | Hashed storage keys/values | 🛡️ | 🛡️ |
| **AccountChangeSet** | 2.5 GB | Historical account state changes | 🛡️ | ✅* |
| **HeaderNumber** | 2.1 GB | Block number to header hash mapping | 🔄 | 🔄 |
| **hermez_intermediate_tx_stateRoots** | 1.7 GB | zkEVM intermediate state roots | 🔄 | 🔄 |
| **BlockBody** | 1.7 GB | Block body data (uncle hashes, etc) | 🔄 | 🔄 |
| **TxSender** | 1.7 GB | Transaction sender addresses | 🔄 | 🔄 |
| **block_info_roots** | 1.5 GB | zkEVM block info roots | 🔄 | 🔄 |
| **CanonicalHeader** | 1.5 GB | Canonical chain headers | 🛡️ | 🛡️ |

### 🟡 Medium Tables (100MB-1GB) - Secondary Targets

| Table Name | Size | Description | Moderate | Aggressive |
|------------|------|-------------|----------|------------|
| **BlockTransactionLookup** | 896.0 MB | Transaction hash to block mapping | ✅ | ✅ |
| **hermez_txPricePercentage** | 854.5 MB | Transaction price percentages | ✅ | ✅ |
| **hermez_blockBatches** | 779.6 MB | Block to batch mappings | 🛡️ | 🛡️ |
| **Receipt** | 711.0 MB | Transaction receipts | 🔄 | 🔄 |
| **LogTopicIndex** | 600.6 MB | Event log topic index | ✅ | ✅ |
| **batch_blocks** | 303.6 MB | Batch to block relationships | 🛡️ | 🛡️ |
| **hermez_stateRoots** | 252.8 MB | zkEVM state roots | 🛡️ | 🛡️ |
| **AccountHistory** | 155.8 MB | Account change history | ✅ | ✅ |
| **CallFromIndex** | 142.8 MB | Contract call source index | ✅ | ✅ |
| **CallToIndex** | 130.4 MB | Contract call destination index | ✅ | ✅ |

### 🟢 Small Tables (1MB-100MB) - Utility Data

| Table Name | Size | Description | Moderate | Aggressive |
|------------|------|-------------|----------|------------|
| **Code** | 75.2 MB | Smart contract bytecode | 🛡️ | 🛡️ |
| **CallTraceSet** | 60.6 MB | Contract call traces | ✅ | ✅ |
| **HashedAccount** | 48.2 MB | Hashed account addresses | 🛡️ | 🛡️ |
| **LogAddressIndex** | 32.6 MB | Event log address index | ✅ | ✅ |
| **l1_info_tree_updates_by_ger** | 18.5 MB | L1 info tree updates by GER | 🛡️ | 🛡️ |
| **HashedCodeHash** | 11.3 MB | Hashed contract code hashes | 🛡️ | 🛡️ |
| **l1_info_tree_updates** | 11.0 MB | L1 info tree updates | 🛡️ | 🛡️ |
| **PlainCodeHash** | 9.3 MB | Plain contract code hashes | 🛡️ | 🛡️ |
| **hermez_forkIds** | 6.4 MB | zkEVM fork identifiers | 🛡️ | 🛡️ |
| **l1_info_roots** | 4.8 MB | L1 information roots | 🛡️ | 🛡️ |
| **l1_info_leaves** | 3.2 MB | L1 information leaves | 🛡️ | 🛡️ |
| **batch_ends** | 3.2 MB | Batch ending markers | 🛡️ | 🛡️ |
| **hermez_globalExitRootsSaved** | 3.0 MB | Saved global exit roots | 🛡️ | 🛡️ |
| **InnerTx** | 2.9 MB | Inner transaction data | 🛡️ | 🛡️ |
| **HermezSmtLastRoot** | 2.9 MB | Last SMT root hashes | 🛡️ | 🛡️ |
| **hermez_l1Sequences** | 2.6 MB | L1 sequence data | 🛡️ | 🛡️ |
| **block_l1_block_hashes** | 2.3 MB | L1 block hash references | 🛡️ | 🛡️ |
| **hermez_globalExitRoots** | 2.3 MB | Global exit roots | 🛡️ | 🛡️ |
| **latest_used_ger** | 2.3 MB | Latest used global exit roots | 🛡️ | 🛡️ |
| **hermez_l1Verifications** | 2.0 MB | L1 verification data | 🛡️ | 🛡️ |

### 🔧 System Tables (<1MB) - Configuration & Metadata

| Table Name | Size | Description | Moderate | Aggressive |
|------------|------|-------------|----------|------------|
| **block_l1_info_tree_index** | 1.2 MB | L1 info tree index | 🛡️ | 🛡️ |
| **Config** | 8.0 KB | Node configuration | 🛡️ | 🛡️ |
| **DbInfo** | 8.0 KB | Database metadata | 🛡️ | 🛡️ |
| **SyncStage** | 8.0 KB | Synchronization stages | 🛡️ | 🛡️ |
| **plain_state_version** | 8.0 KB | State version tracking | 🛡️ | 🛡️ |
| **smt_depths** | 8.0 KB | SMT tree depth info | 🛡️ | 🛡️ |
| **HeadersTotalDifficulty** | 8.0 KB | Chain total difficulty | 🛡️ | 🛡️ |
| **IncarnationMap** | 8.0 KB | Account incarnation mapping | 🛡️ | 🛡️ |
| **LastBlock** | 8.0 KB | Last processed block info | 🛡️ | 🛡️ |
| **LastHeader** | 8.0 KB | Last header info | 🛡️ | 🛡️ |
| **MaxTxNum** | 8.0 KB | Maximum transaction number | 🛡️ | 🛡️ |
| **Sequence** | 8.0 KB | Database sequence numbers | 🛡️ | 🛡️ |
| **Migration** | 8.0 KB | Database migration info | 🛡️ | 🛡️ |
| **Issuance** | 8.0 KB | Token issuance tracking | 🛡️ | 🛡️ |

## SMT Database - Active Tables Analysis

### 🔥 SMT Core Tables (Critical for zkEVM)

| Table Name | Size | Description | Pruning Strategy |
|------------|------|-------------|------------------|
| **HermezSmt** | 44.6 GB | Main SMT tree nodes | 🛡️ **Never Touched** - Core zkEVM proof data |
| **HermezSmtMetadata** | 6.8 GB | SMT node metadata | 🛡️ **Never Touched** - SMT structure info |
| **HermezSmtHashKey** | 5.2 GB | SMT hash to key mapping | 🛡️ **Never Touched** - SMT indexing |
| **HermezSmtAccountValues** | 446.4 MB | Account values in SMT | 🛡️ **Never Touched** - Current state in SMT |
| **HermezSmtStats** | 8.0 KB | SMT statistics | 🛡️ **Never Touched** - SMT performance data |

## Pruning Mode Comparison

### Moderate Mode (~52-57GB savings)  
**Strategy**: Remove unnecessary tables + batch-based pruning
- ✅ Removes: History tables, index tables, old batch data from critical tables
- ✅ Keeps: Recent 10 batches worth of data
- ✅ Protects: All SMT data, current state, essential indexes
- **Safe for**: Sequencer nodes, recent RPC queries

### Aggressive Mode (~52-57GB savings)
**Strategy**: Maximum cleanup including historical state data (with stability protection)
- ✅ Removes: Moderate targets + AccountChangeSet + StorageChangeSet history
- 🛡️ Preserves: CanonicalHeader + hermez_blockBatches for node stability
- ✅ Logic: SMT contains complete state, historical changesets redundant
- ⚠️ **Trade-off**: Some historical RPC queries may fail
- **Best for**: Space-constrained environments, non-archival nodes requiring stability

## Critical Protection Rules

### Always Protected Tables
1. **Current State**: `PlainState`, `HashedAccount`, `HashedStorage`
2. **zkEVM Core**: All `hermez_*` configuration and bridge tables
3. **SMT Data**: All `HermezSmt*` tables
4. **Node Operation**: `Config`, `DbInfo`, `SyncStage`, `LastBlock`
5. **Small Tables**: 5 tables with minimal data (user-specified protection)
   - `block_l1_info_tree_index`, `plain_state_version`, `smt_depths`
   - `HeadersTotalDifficulty`, `MaxTxNum`
   - **Rationale**: Data size < 10MB each, cleanup benefit negligible, safer to preserve

### Header Table Consistency Strategy
**Important Fix**: `Header`, `HeaderNumber`, and `CanonicalHeader` now use **consistent batch-based pruning**

**Problem Solved**: Previously these tables used different strategies:
- 🔄 `Header` - batch-based pruning 
- 🛡️ `HeaderNumber` - fully protected
- 🛡️ `CanonicalHeader` - fully protected

This inconsistency caused data integrity issues where header lookups could fail.

**Solution**: All three tables now use the same batch-based strategy:
- ✅ `Header` - keep recent batches, delete old data
- ✅ `HeaderNumber` - keep recent batches, delete old data  
- ✅ `CanonicalHeader` - keep recent batches, delete old data

**Combined Impact**: ~20.7 GB can now be pruned safely while maintaining data consistency.

## Pruning Mode Summary Statistics

### Moderate Mode (Default Recommended)  
- **Tables Deleted**: 
  - ✅ **Direct Deletion** (9 tables, ~8.5 GB): BlockTransaction, BlockTransactionLookup, hermez_txPricePercentage, LogTopicIndex, AccountHistory, CallFromIndex, CallToIndex, CallTraceSet, LogAddressIndex
  - 🔄 **Batch-Based Pruning** (10 tables, ~47+ GB): Header, HeaderNumber, CanonicalHeader, Receipt, hermez_blockBatches, BlockBody, TxSender, block_info_roots, TransactionLog, hermez_intermediate_tx_stateRoots
  - 🛡️ **Small Tables Protected** (5 tables, <10MB): block_l1_info_tree_index, plain_state_version, smt_depths, HeadersTotalDifficulty, MaxTxNum
- **Space Saved**: ~52-57 GB
- **Strategy**: Production sequencer nodes, recent data preserved

### Aggressive Mode
- **Tables Deleted**: All Moderate mode deletions PLUS:
  - ✅* **Historical State Cleanup** (2 tables, ~12.5 GB): AccountChangeSet, StorageChangeSet (historical data beyond recent batches)
  - 🛡️ **Preserved for Stability** (2 tables, ~2.3 GB): CanonicalHeader, hermez_blockBatches (critical for node operation)
- **Space Saved**: ~52-57 GB  
- **Strategy**: Maximum space optimization with stability protection, some historical queries may fail

### Conditionally Pruned Tables
1. **Large Block Data**: Pruned by batch, keeping recent data (moderate+)
2. **Historical Changes**: Removed in aggressive mode only
3. **Indexes**: Removed in all pruning modes
4. **Logs**: Pruned by batch in all modes

## Compaction Potential Analysis

### Chaindata Database
- **Current Difference**: 5.7 GB (7.5%)
- **Compaction Potential**: ~4-5 GB recovery
- **Recommended**: After pruning for maximum efficiency

### SMT Database  
- **Current Difference**: 48.5 GB (46.0%!) 
- **Analysis**: Extremely high fragmentation, likely from:
  - SMT tree node updates and reorganization
  - Batch processing creating many temporary entries
  - Historical SMT operations leaving large freelist
- **Compaction Potential**: ~30-45 GB recovery
- **Highly Recommended**: SMT database is prime candidate for compaction

## Optimal Operation Sequence

```bash
# 1. Analyze current state
./prune-tool list-tables /path/to/datadir

# 2. Prune unnecessary data first
./prune-tool prune-chaindata /path/to/datadir moderate --keep-recent-batches=10

# 3. Compact both databases for maximum space recovery (in-place mode)
./prune-tool compact-db -source /path/to/datadir/chaindata -in-place
./prune-tool compact-db -source /path/to/datadir/smt -in-place -type smt

# Expected total savings: ~107-112 GB (52-57GB from pruning + 55GB from compaction)
```

## Key Insights

1. **SMT Database** has massive compaction potential (46% fragmentation)
2. **Header table** (17.1GB) is now prunable in batch-based mode  
3. **Historical ChangeSets** (12.5GB total) can be safely removed in aggressive mode
4. **Combined approach** (prune + compact) can save 107-112GB from original 182GB (59-62%)
5. **zkEVM tables** require special protection but many are small

## Quick Reference Table

| Mode | Direct Deletions | Batch-Based Pruning (🔄) | Historical Cleanup | Total Space Saved |
|------|------------------|--------------------------|-------------------|-------------------|
| **Moderate (Recommended)** | 9 tables (~8.5 GB) | 10 tables (~45 GB) | None | ~52-57 GB |
| **Aggressive** | 9 tables (~8.5 GB) | 10 tables (~45 GB) | 2 tables* (~12.5 GB) | ~52-57 GB |

**Notes:**
- *Historical cleanup = only removes historical data beyond recent batches
- Batch-based pruning preserves recent N batches (default: 10)  
- All modes preserve SMT data and critical zkEVM operational tables
- **⚠️ Important**: Table deletion does NOT immediately reduce file size - requires `compact-db` to reclaim space
- **Real-world impact**: Pruning alone = ~29-31% savings, Pruning + Compaction = ~59-62% savings (from 182GB total)

This analysis enables targeted, safe database optimization while preserving zkEVM functionality.
