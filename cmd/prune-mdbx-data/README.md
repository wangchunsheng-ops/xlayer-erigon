# 🗂️ X Layer MDBX Data Pruning Tool

A comprehensive database management tool for X Layer (zkEVM) Erigon nodes.

## 🚀 Quick Start

```bash
# Build tool
go build -o prune-mdbx-data main.go

# List tables  
./prune-mdbx-data list-tables ./datadir

# Prune data
./prune-mdbx-data prune-chaindata ./datadir moderate --keep-recent-batches=5 --yes
```

## Commands

### list-tables
Analyze database structure and table sizes.

### prune-chaindata
Prune unnecessary data with batch-based optimization.

## Pruning Levels

- **Conservative**: Safe table-level pruning (15-20% space)
- **Moderate**: Batch-based zkEVM optimized (25-40% space)

## Options

- `--keep-recent-batches=N`: Keep N recent batches (default: 10)
- `--yes, -y`: Auto-confirm operations

---

For detailed documentation, see the generated help with `prune-mdbx-data help`
