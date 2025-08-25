# Database Compaction Tool

## Overview

The `compact-db` tool is designed to reclaim space from MDBX database freelist and fragmentation. When data is deleted from an MDBX database (either through pruning or normal operations), the space is added to a freelist for reuse, but this space still contributes to the database file size on disk.

## What Gets Reclaimed

- **Freelist pages**: Previously deleted data that's marked as available for reuse
- **Fragmentation**: Empty spaces between data caused by non-contiguous writes  
- **Overhead**: Reduced metadata overhead through reorganization

## When To Use

### ✅ Good Candidates for Compaction
- Database shows large "Difference" in `list-tables` output (>5% of total size)
- After significant pruning operations
- Database has been running for extended periods with many writes/deletes
- SMT databases with high fragmentation (common after batch processing)

### ❌ Not Worth Compacting
- Recently created databases
- Difference is <2% of total size
- Very active databases (fragmentation will return quickly)

## Usage Examples

### 1. Analyze Potential Savings (Dry Run)
```bash
prune-mdbx-data compact-db -source ./seq/chaindata -output /tmp/test -dry-run
```

### 2. Compact Chaindata Database
```bash
prune-mdbx-data compact-db -source ./seq/chaindata -output ./seq/chaindata.compact
```

### 3. Compact SMT Database  
```bash
prune-mdbx-data compact-db -source ./seq/smt -output ./seq/smt.compact -type smt
```



## Safety Procedure

1. **Stop Erigon node** completely
2. **Backup original database**:
   ```bash
   cp -r ./seq/chaindata ./seq/chaindata.backup
   ```
3. **Run compaction**:
   ```bash
   prune-mdbx-data compact-db -source ./seq/chaindata -output ./seq/chaindata.compact
   ```
4. **Replace original**:
   ```bash
   mv ./seq/chaindata ./seq/chaindata.old
   mv ./seq/chaindata.compact ./seq/chaindata
   ```
5. **Test startup** - start Erigon node and verify operation
6. **Clean up** (after verification):
   ```bash
   rm -rf ./seq/chaindata.old ./seq/chaindata.backup
   ```

## Expected Space Savings

| Database Type | Typical Savings | Notes |
|---------------|-----------------|--------|
| **Fresh DB** | 0-2% | Minimal fragmentation |
| **After Pruning** | 5-15% | Significant freelist |  
| **Long-running** | 3-8% | Accumulated fragmentation |
| **SMT (high activity)** | 10-25% | Batch processing creates gaps |

## Technical Details

- **Process**: Creates new database and copies data table by table
- **Result**: Eliminates freelist and optimizes page layout  
- **Duration**: ~10-30 minutes per 100GB depending on hardware
- **Space**: Requires free space equal to database size during operation
- **Atomicity**: Original database remains untouched until manual replacement



## Troubleshooting

### "Source database not found"
- Verify path includes `mdbx.dat` file
- Check database type (-type chaindata vs smt)

### "Output path already exists"  
- Tool won't overwrite existing directories
- Remove or rename existing output path

### "Database compaction failed"
- Check available disk space (needs ~2x database size)
- Ensure source database is not in use
- Check file permissions

### Low space savings
- Some overhead is normal (~1-3%)
- Recent databases have less fragmentation
- Consider if compaction is worth the effort
