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

## Operation Modes

### 📊 Analysis Mode (Dry Run)
Analyze potential space savings without performing actual compaction:
```bash
# No output path needed for analysis
prune-mdbx-data compact-db -source ./seq/chaindata -dry-run
prune-mdbx-data compact-db -source ./seq/smt -dry-run -type smt
```

### 📁 Copy Mode (Default)
Creates a new compacted database while preserving the original:
```bash
# Chaindata compaction
prune-mdbx-data compact-db -source ./seq/chaindata -output ./seq/chaindata.compact

# SMT database compaction  
prune-mdbx-data compact-db -source ./seq/smt -output ./seq/smt.compact -type smt
```

### 🔄 In-Place Mode (Recommended)
Compacts database and automatically replaces the original:
```bash
# Chaindata in-place compaction (saves disk space)
prune-mdbx-data compact-db -source ./seq/chaindata -in-place

# SMT in-place compaction
prune-mdbx-data compact-db -source ./seq/smt -in-place -type smt
```



## Safety Procedures

### 🔄 In-Place Mode (Automated)
The tool handles backup and replacement automatically:

1. **Stop Erigon node** completely
2. **Run in-place compaction**:
   ```bash
   prune-mdbx-data compact-db -source ./seq/chaindata -in-place
   ```
3. **Tool automatically**:
   - Creates backup (`database.backup`)
   - Compacts to temporary location
   - Replaces original atomically
4. **Start Erigon node** and verify operation
5. **Clean up backup** (after verification):
   ```bash
   rm -rf ./seq/chaindata.backup
   ```

### 📁 Copy Mode (Manual)
For users who prefer manual control:

1. **Stop Erigon node** completely
2. **Create backup**:
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
5. **Test startup** and verify operation
6. **Clean up**:
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

### Copy Mode
- **Process**: Creates new database and copies data table by table
- **Space**: Requires ~2x database size (original + compacted)
- **Safety**: Original database remains untouched
- **Manual**: Requires manual backup and replacement

### In-Place Mode  
- **Process**: Creates temporary compacted copy, then atomically replaces original
- **Space**: Requires ~1x database size temporarily (automatic cleanup)
- **Safety**: Automatic backup created (`database.backup`)
- **Automated**: Handles backup, replacement, and cleanup automatically

### Common Characteristics
- **Result**: Eliminates freelist and optimizes page layout  
- **Duration**: ~10-30 minutes per 100GB depending on hardware
- **Path Support**: Supports both relative and absolute paths
- **Dry Run**: Analysis mode requires no additional disk space



## Troubleshooting

### "Source database not found"
- **Relative paths**: Use `seq/chaindata` not `seq`
- **Verify file**: Ensure `mdbx.dat` exists in source directory
- **Database type**: Check `-type chaindata` vs `-type smt`
- **Working directory**: Tool auto-handles relative paths from main directory

### "Output path already exists"  
- **Copy mode**: Tool won't overwrite existing directories
- **Solution**: Remove or rename existing output path
- **In-place mode**: Not applicable (uses temporary paths)

### Parameter errors
- **Dry run**: Use `-source path -dry-run` (no output path needed)
- **In-place**: Use `-source path -in-place` (no output path needed)  
- **Copy mode**: Requires both `-source path -output newpath`

### "Database compaction failed"
- **Disk space**: 
  - Copy mode: needs ~2x database size
  - In-place mode: needs ~1x database size temporarily
- **Database in use**: Ensure Erigon node is completely stopped
- **Permissions**: Check read/write access to source and destination

### "resource temporarily unavailable" error
- **Fixed**: Database connection management improved to prevent MDBX lock conflicts
- **Cause**: Previously occurred when multiple database connections weren't properly closed
- **Solution**: Tool now includes proper connection cleanup and timing delays

### Database size display issues
- **Fixed**: Tool now shows actual disk usage instead of sparse file virtual size
- **Cause**: MDBX creates sparse files where `ls -lh` shows virtual size (e.g., 8GB) but `du -h` shows real usage (e.g., 24MB)
- **Solution**: Both `list-tables` and `compact-db` now use syscall to report actual disk usage

### Low space savings
- **Normal overhead**: 1-3% is typical for healthy databases
- **Recent databases**: Have minimal fragmentation
- **Cost-benefit**: Consider if compaction effort is worthwhile
- **Analysis first**: Always run `-dry-run` to check potential savings
