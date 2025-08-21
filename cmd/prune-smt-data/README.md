# SMT Data Pruning Tool

This directory contains tools for analyzing and pruning SMT (Sparse Merkle Tree) data.

## Tool List

### 1. Database Table Listing Tool

- **Command**: `list-tables`
- **Script**: `list-tables.sh`
- **Documentation**: `list-tables.md`

This tool helps you:
- View actual table structure in Erigon database
- Analyze table distribution before and after SMT database separation
- Perform categorical analysis of database tables

#### Usage

```bash
cd cmd/prune-smt-data

# Using main program
go run main.go list-tables /path/to/your/erigon/data

# Or using script
./list-tables.sh /path/to/your/erigon/data
```

### 2. Chaindata Pruning Tool

- **Command**: `prune-chaindata`
- **Script**: `prune-chaindata.sh`

This tool helps you:
- Delete unnecessary data during sequencer operation
- Save storage space
- Improve database performance

#### Pruning Levels

The tool provides three pruning levels:

1. **Conservative Pruning (conservative)** - Default level
   - Delete obviously unnecessary tables: history data, indexes, Trie, Beacon tables
   - Preserve: Block data, transaction data, state data (for debugging and compatibility)
   - Safest option, suitable for first-time use
   - **Deletes ~50 tables**, saves ~15-20% space

2. **Moderate Pruning (moderate)** - Recommended level ⭐
   - **Smart partial deletion**: Preserves recent block data, deletes historical data
   - Preserve: Core state data, sync progress, ZKEVM data, **recent 100 blocks** (7 tables)
   - Delete: History data, indexes, some state tables, old block data
   - **Uses partial pruning**: Keeps recent blocks to ensure node can restart
   - **Partial pruning**: Header, BlockBody, Receipt, TxSender, CanonicalHeader, TransactionLog, HeaderNumber
   - **Full deletion**: BlockTransaction, BlockTransactionLookup (excluded due to key format complexity)
   - Balanced safety and space saving, suitable for sequencer operation
   - **Deletes ~97 tables**, saves ~37% space

3. **Aggressive Pruning (aggressive)**
   - Delete almost all tables, keep only operational necessities
   - Preserve: PlainState (current state), sync progress, critical ZKEVM data, **recent 100 blocks** (7 tables)
   - Delete: Most block data, transaction data, state data (except recent blocks)
   - **Uses partial pruning**: Same 7 block tables as Moderate level preserve recent data
   - **Full deletion**: BlockTransaction, BlockTransactionLookup still fully deleted
   - Maximum space saving while maintaining node restart capability
   - ⚠️ **SAFER THAN BEFORE**: Recent block data preserved to allow node restart
   - **Deletes ~150+ tables**, saves ~50-60% space

## 🔧 Partial Pruning Feature

**NEW**: Smart partial deletion for block tables to prevent node startup failures.

### How It Works

Instead of completely deleting block tables (which would prevent node restart), the tools now use **partial pruning**:

1. **Analyze**: Detects the latest block number in the database
2. **Calculate**: Determines the cutoff point (latest block - keep count)
3. **Preserve**: Keeps recent N blocks of data in block tables
4. **Remove**: Deletes only historical block data older than the cutoff

### Affected Tables

The following **7 tables** use partial pruning in **Moderate** and **Aggressive** levels:

| Table Name | Key Format | Description | Default Retention |
|------------|------------|-------------|-------------------|
| `Header` | block_num + hash | Block headers data | **Recent 100 blocks** |
| `BlockBody` | block_num + hash | Block body content | **Recent 100 blocks** |
| `Receipt` | block_num | Transaction receipts | **Recent 100 blocks** |
| `TxSender` | block_num + blockHash | Transaction sender addresses | **Recent 100 blocks** |
| `CanonicalHeader` | block_num | Canonical block headers | **Recent 100 blocks** |
| `TransactionLog` | block_num + txId | Transaction logs/events | **Recent 100 blocks** |
| `HeaderNumber` | header_hash → block_num | Block number mappings | **Recent 100 blocks** |

### ❌ Excluded Tables (Full Deletion Still Applied)

| Table Name | Key Format | Reason for Exclusion |
|------------|------------|----------------------|
| `BlockTransaction` | tx_id | Uses transaction ID sequence, not block number |
| `BlockTransactionLookup` | tx_hash | Uses transaction hash, not block number |

🔄 **Behavior**: Partial pruning preserves the **most recent 100 blocks** of data (configurable via `--keep-recent-blocks`) and deletes older historical entries. Excluded tables still undergo complete deletion in their respective pruning levels.

### Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `--keep-recent-blocks` | 100 | Number of recent blocks to preserve |

### Benefits

✅ **Node can restart**: Preserves essential recent block data  
✅ **Space saving**: Removes historical data that's rarely accessed  
✅ **Configurable**: Adjust retention based on your needs  
✅ **Safe**: Never deletes SMT or critical system tables  
✅ **Well-documented**: Comprehensive analysis of all 192 table formats available

### 📚 Complete Documentation

For detailed analysis of all database tables:
- **[COMPLETE_TABLE_FORMATS.md](cmd/prune-chaindata/COMPLETE_TABLE_FORMATS.md)** - Analysis of all 192 tables
- **[TABLE_FORMATS.md](cmd/prune-chaindata/TABLE_FORMATS.md)** - Partial pruning specific analysis  
- **[MODERATE_CHANGES.md](cmd/prune-chaindata/MODERATE_CHANGES.md)** - Change history and rollback instructions  

## 🔒 Critical Tables (NEVER DELETED)

**Total: 28 tables that are ALWAYS preserved regardless of pruning level**

<details>
<summary>📋 Click to view all critical tables</summary>

### **SMT Related Tables (6 tables)**
```
✅ HermezSmt              # Main SMT data  
✅ HermezSmtStats         # SMT statistics
✅ HermezSmtAccountValues # Account value cache
✅ HermezSmtMetadata      # SMT metadata
✅ HermezSmtHashKey       # Hash key mapping
✅ HermezSmtLastRoot      # Last root state
```

### **Core System Tables (4 tables)**
```
✅ Config      # Database configuration
✅ DbInfo      # Database information  
✅ SyncStage   # Sync stage progress
✅ Migration   # Database migration records
```

### **Current State Table (1 table)**
```
✅ PlainState  # Current account state (MOST IMPORTANT!)
```

### **Critical Block Tracking Tables (5 tables)**
```
✅ LastBlock              # Latest block hash tracker
✅ LastHeader             # Latest header hash tracker  
✅ MaxTxNum               # Maximum transaction number tracker
✅ LastForkchoice         # Latest forkchoice state (CRITICAL for sequencer)
✅ CurrentExecutionPayload # Current execution payload (CRITICAL for sequencer)
```

### **Critical ZKEVM Tables (12 tables)**
```
✅ hermez_forkIds            # Fork ID management
✅ hermez_forkIdBlock        # Fork block mapping
✅ hermez_blockBatches       # Batch data
✅ hermez_globalExitRoots    # Global exit roots
✅ hermez_stateRoots         # State roots
✅ l1_info_tree_updates      # L1 info tree updates
✅ smt_depths                # SMT depth information
✅ batch_blocks              # Batch block mapping
✅ block_info_roots          # Block info roots
✅ l1_info_leaves            # L1 info leaves
✅ l1_info_roots             # L1 info roots
✅ latest_used_ger           # Latest used GER
```

</details>

## 🔥 Detailed Pruning Strategy

### **Conservative Pruning - Deletes ~50 tables**

**Deletes 4 categories of obviously unnecessary tables:**

<details>
<summary>📋 Conservative deletion list (50 tables)</summary>

#### **🔥 History Data Tables (12 tables)**
```
❌ AccountChangeSet          # Account change sets
❌ StorageChangeSet          # Storage change sets  
❌ AccountHistory            # Account history
❌ StorageHistory            # Storage history
❌ AccountHistoryKeys        # Account history keys
❌ AccountHistoryVals        # Account history values
❌ StorageHistoryKeys        # Storage history keys
❌ StorageHistoryVals        # Storage history values
❌ CodeHistoryKeys           # Code history keys
❌ CodeHistoryVals           # Code history values
❌ CommitmentHistoryKeys     # Commitment history keys
❌ CommitmentHistoryVals     # Commitment history values
```

#### **🔥 Index Tables (15 tables)**
```
❌ LogTopicIndex             # Log topic index
❌ LogAddressIndex           # Log address index
❌ CallTraceSet              # Call trace set
❌ CallFromIndex             # Call from index
❌ CallToIndex               # Call to index
❌ LogAddressIdx             # Log address index
❌ LogAddressKeys            # Log address keys
❌ LogTopicsIdx              # Log topics index
❌ LogTopicsKeys             # Log topics keys
❌ TracesFromIdx             # Traces from index
❌ TracesFromKeys            # Traces from keys
❌ TracesToIdx               # Traces to index
❌ TracesToKeys              # Traces to keys
❌ CumulativeGasIndex        # Cumulative gas index
❌ CumulativeTransactionIndex # Cumulative transaction index
```

#### **🔥 Trie Tables (4 tables)**
```
❌ TrieAccount               # Account Trie
❌ TrieStorage               # Storage Trie
❌ VerkleRoots               # Verkle roots
❌ VerkleTrie                # Verkle Trie
```

#### **🔥 Beacon Tables (40 tables)**
```
❌ BeaconState               # Beacon state
❌ BeaconBlock               # Beacon blocks
❌ CanonicalBlockRoots       # Canonical block roots
❌ BlockRootToSlot           # Block root to slot mapping
❌ BlockRootToStateRoot      # Block root to state root mapping
❌ StateRootToBlockRoot      # State root to block root mapping
❌ BlockRootToParentRoot     # Block root to parent root mapping
❌ BeaconBlockHeaders        # Beacon block headers
❌ HighestFinalized          # Highest finalized
❌ Attestetations            # Attestations
❌ LightClientUpdates        # Light client updates
❌ ActiveValidatorIndicies   # Active validator indices
❌ BalancesDump              # Balances dump
❌ EffectiveBalancesDump     # Effective balances dump
❌ ValidatorBalance          # Validator balance
❌ ValidatorEffectiveBalance # Validator effective balance
❌ ValidatorPublickeys       # Validator public keys
❌ ValidatorSlashings        # Validator slashings
❌ StaticValidators          # Static validators
❌ InvertedValidatorPublickeys # Inverted validator public keys
❌ InactivityScores          # Inactivity scores
❌ PreviousEpochParticipation # Previous epoch participation
❌ CurrentEpochParticipation # Current epoch participation
❌ NextSyncCommittee         # Next sync committee
❌ CurrentSyncCommittee      # Current sync committee
❌ HistoricalRoots           # Historical roots
❌ HistoricalSummaries       # Historical summaries
❌ Eth1DataVotes             # Eth1 data votes
❌ IntraRandaoMixes          # Intra RANDAO mixes
❌ RandaoMixes               # RANDAO mixes
❌ BlockProposers            # Block proposers
❌ StatesProcessingProgress  # States processing progress
❌ EpochData                 # Epoch data
❌ SlotData                  # Slot data
❌ DevEpoch                  # Dev epoch
❌ DevPendingEpoch           # Dev pending epoch
❌ LastBeaconSnapshot        # Last beacon snapshot
❌ KzgCommitmentToBlob       # KZG commitment to blob
❌ CurrentExecutionPayload   # Current execution payload
❌ LastForkchoice            # Last forkchoice
```

**✅ Conservative preserves:** Block data, transaction data, most state data, ZKEVM data

</details>

### **Moderate Pruning - Deletes ~100 tables** ⭐

**Includes all Conservative deletions + additional state tables:**

<details>
<summary>📋 Moderate additional deletions (50+ more tables)</summary>

#### **🔥 Additional State Data Tables (9 tables)**
```
❌ HashedStorage             # Hashed storage state
❌ StateAccounts             # State accounts
❌ StateStorage              # State storage  
❌ StateCode                 # State code
❌ StateCommitment           # State commitment
❌ HashedAccount             # Hashed accounts
❌ HashedCodeHash            # Hashed code hash
❌ PlainCodeHash             # Plain code hash
❌ TEVMCode                  # TEVM code
```

#### **🔥 Domain/History Tables (18 tables)**
```
❌ AccountIdx, AccountKeys, AccountVals       # Account domain tables
❌ StorageIdx, StorageKeys, StorageVals       # Storage domain tables
❌ CodeIdx, CodeKeys, CodeVals                # Code domain tables
❌ CommitmentIdx, CommitmentKeys, CommitmentVals # Commitment domain tables
❌ RAccountIdx, RAccountKeys                  # Reverse account tables
❌ RCodeIdx, RCodeKeys                        # Reverse code tables
❌ RStorageIdx, RStorageKeys                  # Reverse storage tables
```

#### **🔥 Bor/Polygon Tables (10 tables)**
```
❌ BorCheckpointEnds         # Bor checkpoint ends
❌ BorCheckpoints            # Bor checkpoints
❌ BorEventNums              # Bor event numbers
❌ BorEvents                 # Bor events
❌ BorFinality               # Bor finality
❌ BorMilestoneEnds          # Bor milestone ends
❌ BorMilestones             # Bor milestones
❌ BorReceipt                # Bor receipts
❌ BorSeparate               # Bor separate
❌ BorSpans                  # Bor spans
```

#### **🔥 Clique Tables (3 tables)**
```
❌ CliqueLastSnapshot        # Clique last snapshot
❌ CliqueSeparate            # Clique separate
❌ CliqueSnapshot            # Clique snapshot
```

#### **🔥 Debug/Diagnostic Tables (5 tables)**
```
❌ bad_tx_hashes                        # Bad transaction hashes
❌ discarded_transactions_by_block      # Discarded transactions by block
❌ discarded_transactions_by_hash       # Discarded transactions by hash
❌ just_unwound                         # Just unwound
❌ PoolLimbo                            # Pool limbo
```

**✅ Moderate preserves:** PlainState, sync progress, ZKEVM data, basic block data

</details>

### **Aggressive Pruning - Deletes ~150+ tables**

**Deletes ALL non-critical tables, keeps only operational necessities:**

<details>
<summary>📋 Aggressive additional deletions (50+ more tables)</summary>

#### **🔥 Basic Block Tables (21 tables)**
```
❌ HeaderNumber              # Block header numbers
❌ BadHeaderNumber           # Bad block header numbers
❌ HeadersTotalDifficulty    # Headers total difficulty
❌ BlockBody                 # Block bodies
❌ Header                    # Block headers
❌ BlockTransaction          # Block transactions
❌ Receipt                   # Transaction receipts
❌ TxSender                  # Transaction senders
❌ CanonicalHeader           # Canonical headers
❌ BlockRoot                 # Block roots
❌ BlockRootToBlockHash      # Block root to block hash mapping
❌ BlockRootToBlockNumber    # Block root to block number mapping
❌ BlockRootToKzgCommitments # Block root to KZG commitments
❌ LastBlock                 # Last block
❌ LastHeader                # Last header
❌ MaxTxNum                  # Max transaction number
❌ TransactionLog            # Transaction logs
❌ NonCanonicalTransaction   # Non-canonical transactions
❌ BlockTransactionLookup    # Block transaction lookup
❌ BlockBorTransactionLookup # Block Bor transaction lookup
❌ InnerTx                   # Inner transactions
```

#### **🔥 Remaining State Data Tables (6 tables)**
```
❌ Code                      # Contract code
❌ StateEvents               # State events
❌ StateRoot                 # State roots
❌ IncarnationMap            # Incarnation mapping
❌ plain_state_version       # Plain state version
```

#### **🔥 Remaining System Tables (4 tables)**
```
❌ Snapshots                 # Snapshots
❌ erigon_versions           # Erigon versions
❌ Issuance                  # Issuance
❌ Sequence                  # Sequence
```

**⚠️ Aggressive preserves ONLY:** 23 critical tables (PlainState + SMT + essential ZKEVM + system config)

</details>

#### Pruning Strategy Breakdown

| Level | Tables Deleted | Space Saved | Risk Level | Use Case |
|-------|----------------|-------------|------------|----------|
| **Conservative** | ~50 tables | 15-20% | 🟢 Low | First-time use, debugging |
| **Moderate** ⭐ | ~97 tables | 37% | 🟡 Medium | Sequencer operation |
| **Aggressive** | ~150+ tables | 50-60% | 🔴 High | Extreme space saving |

#### Usage

```bash
cd cmd/prune-smt-data

# Using main program
go run main.go prune-chaindata /path/to/erigon/data                    # Conservative (no partial pruning)
go run main.go prune-chaindata /path/to/erigon/data moderate          # Moderate (default: keep 100 recent blocks in 9 tables)
go run main.go prune-chaindata /path/to/erigon/data aggressive        # Aggressive (default: keep 100 recent blocks in 9 tables)

# With custom recent blocks count (for moderate/aggressive levels)
go run main.go prune-chaindata /path/to/erigon/data moderate --keep-recent-blocks 50    # Keep only 50 recent blocks
go run main.go prune-chaindata /path/to/erigon/data aggressive --keep-recent-blocks 200  # Keep 200 recent blocks

# Or using script (Note: scripts don't support --keep-recent-blocks parameter)
./prune-chaindata.sh /path/to/erigon/data                             # Conservative
./prune-chaindata.sh /path/to/erigon/data moderate                    # Moderate
./prune-chaindata.sh /path/to/erigon/data aggressive                  # Aggressive
```

## Directory Structure

```
cmd/prune-smt-data/
├── README.md                    # Main documentation
├── main.go                      # Main program entry
├── list-tables.md              # Table listing tool documentation
├── list-tables.sh              # Table listing tool script
├── prune-chaindata.sh          # Chaindata pruning tool script
├── pkg/                        # Shared packages
│   └── db.go                   # Database operations package
└── cmd/                        # Subcommand source code
    ├── list-tables/
    │   └── main.go             # Table listing subcommand
    └── prune-chaindata/
        └── main.go             # Chaindata pruning subcommand
```

## Main Program Usage

```bash
# Show help
go run main.go help

# List database tables
go run main.go list-tables /path/to/erigon/data

# Prune chaindata
go run main.go prune-chaindata /path/to/erigon/data                    # Conservative
go run main.go prune-chaindata /path/to/erigon/data moderate          # Moderate
go run main.go prune-chaindata /path/to/erigon/data aggressive        # Aggressive
```

## Features

### ✅ Smart Database Detection
- Automatically detects if SMT database separation has been performed
- Handles both unified and separated database configurations
- Protects all SMT-related tables automatically

### ✅ Safe Cleaning Methods
- Uses database `ClearBucket()` method instead of file deletion
- Preserves table structure while clearing data
- Transaction-based operations with rollback capability

### ✅ Comprehensive Analysis
- Complete coverage of all 192 database tables
- Detailed categorization and size analysis
- Real-time statistics and preview before operation

### ✅ Protection Mechanisms
- Critical table protection (SMT, system, ZKEVM operational tables)
- User confirmation before destructive operations
- Detailed operation logs and progress tracking

## Safety Warnings

⚠️ **IMPORTANT REMINDERS**:
- All pruning operations are **irreversible**
- Deleted data will be **permanently lost**
- **Backup your database** before operations
- Aggressive pruning will delete all block and transaction data - use with extreme caution

## Development Roadmap

This directory is planned to include more SMT data management tools:

1. **SMT Data Pruning Tool** - Clean unnecessary SMT data
2. **SMT Data Compression Tool** - Compress SMT data to save space
3. **SMT Data Validation Tool** - Verify SMT data integrity
4. **SMT Data Migration Tool** - Migrate SMT data between databases

## Related Links

- [SMT Database Separation Tool](../smt-db-split/) - For separating SMT and chain data
- [SMT Package](../../smt/) - Core SMT implementation

## 📊 Storage Distribution Analysis

**Based on Real Database Analysis (Total Size: 1,696 KB / 1.66 MB)**

### Top 10 Largest Tables

| Rank | Table Name | Size | % of Total | Category | Pruning Action |
|------|------------|------|------------|----------|----------------|
| 1 | `HermezSmt` | 520.0 KB | 30.7% | SMT | 🔒 **Never Deleted** |
| 2 | `Header` | 152.0 KB | 9.0% | Block Data | 🔥 Deleted in Moderate+ |
| 3 | `Code` | 104.0 KB | 6.1% | State Data | ⚠️ Deleted in Aggressive (breaks contracts) |
| 4 | `StorageChangeSet` | 88.0 KB | 5.2% | History | 🔥 Deleted in Conservative+ |
| 5 | `HermezSmtMetadata` | 64.0 KB | 3.8% | SMT | 🔒 **Never Deleted** |
| 6 | `BlockTransaction` | 56.0 KB | 3.3% | Block Data | 🔥 Deleted in Moderate+ |
| 7 | `PlainState` | 56.0 KB | 3.3% | State Data | 🔒 **Never Deleted** |
| 8 | `StorageHistory` | 56.0 KB | 3.3% | History | 🔥 Deleted in Conservative+ |
| 9 | `HermezSmtHashKey` | 48.0 KB | 2.8% | SMT | 🔒 **Never Deleted** |
| 10 | `HashedStorage` | 48.0 KB | 2.8% | State Data | 🔥 Deleted in Moderate+ |
| - | **Others (182 tables)** | **504.0 KB** | **29.7%** | Mixed | Various |

**Top 10 Total**: 1,192 KB (70.3% of database)  
**Others Total**: 504.0 KB (29.7% of database)

### Storage by Category

| Category | Total Size | % of Database | Table Count | Pruning Impact |
|----------|------------|---------------|-------------|----------------|
| **🔒 SMT Related** | 648 KB | **38.2%** | 6 tables | Never deleted |
| **📦 Block Data** | 368 KB | **21.7%** | 21 tables | Conservative: ✅ Moderate: 🔥 |
| **💾 State Data** | 256 KB | **15.1%** | 15 tables | Conservative: ✅ Moderate: 🔥 Aggressive: 🔥 |
| **📜 History Data** | 184 KB | **10.8%** | 12 tables | All levels: 🔥 |
| **🏗️ ZKEVM Data** | 112 KB | **6.6%** | 35 tables | Critical ones protected |
| **📋 Log/Index Data** | 40 KB | **2.4%** | 15 tables | All levels: 🔥 |
| **⚙️ System Config** | 56 KB | **3.3%** | 8 tables | Critical ones protected |
| **🎯 Other Categories** | 32 KB | **1.9%** | 80 tables | Most deleted |

### Space Savings by Pruning Level

| Level | Tables Deleted | Space Saved | % Saved | What Gets Deleted |
|-------|----------------|-------------|---------|-------------------|
| **Conservative** | ~50 tables | ~250-300 KB | **15-18%** | History + Index + Trie + Beacon |
| **Moderate** ⭐ | ~97 tables | ~624 KB | **36.8%** | + Block Data + Some State |
| **Aggressive** | ~150+ tables | ~850-1000 KB | **50-60%** | + Most State + Domain Tables |

### Key Insights

1. **🔥 SMT Dominates Storage (38.2%)**: SMT tables are the largest component but are always protected
2. **📦 Block Data is Major Target (21.7%)**: Historical block data is the best candidate for space savings
3. **📜 History Data Worth Deleting (10.8%)**: Account/storage history provides good savings with minimal impact
4. **💡 Moderate Level Optimal**: Saves 37% space while preserving operational capabilities
5. **⚠️ Code Table Critical for Contracts**: The `Code` table (104KB, 6.1%) stores smart contract bytecode - deleting it (Aggressive level) prevents contract execution but allows pure state maintenance

## Table Coverage Statistics

- **Total Tables**: 192
- **Categorized Tables**: ~185 (96.4% coverage)
- **Protected SMT Tables**: 6 (always preserved)
- **Critical System Tables**: 26 (always preserved)
- **Available for Pruning**: ~172 tables
- **Actual Database Size**: 1,696 KB (1.66 MB)

The tool provides comprehensive coverage of the X Layer Erigon database structure and ensures safe, reversible operations for space optimization.