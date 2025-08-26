# Small Tables Protection Strategy

## Overview

This document explains the strategy for protecting small database tables from cleanup operations in X Layer Erigon pruning tools.

## Protected Small Tables

The following 5 tables are **permanently protected** from all pruning operations regardless of mode:

| Table Name | Size | Description | Rationale |
|------------|------|-------------|-----------|
| **block_l1_info_tree_index** | 1.2 MB | L1 info tree index | Small size, index data |
| **plain_state_version** | 8.0 KB | State version tracking | Tiny size, version control |
| **smt_depths** | 8.0 KB | SMT tree depth info | Tiny size, SMT metadata |
| **HeadersTotalDifficulty** | 8.0 KB | Chain total difficulty | Tiny size, chain metadata |
| **MaxTxNum** | 8.0 KB | Maximum transaction number | Tiny size, transaction metadata |

## Protection Strategy

### Why These Tables Are Protected

1. **Minimal Space Impact**: Combined size < 10MB, negligible cleanup benefit
2. **Safety First**: Preserving small tables eliminates any risk of breaking functionality
3. **Development Efficiency**: Reduces complexity in pruning logic
4. **User Request**: Explicitly requested by development team

### Implementation Details

#### Code Changes
- Added to `getCriticalTables()` function as protected tables
- Removed from all pruning logic functions:
  - `copyBlockData()`: Excluded from batch operations
  - `clearBatchTables()`: Excluded from table clearing
  - `deleteBlockData()`: Excluded from legacy deletion
  - `deleteCompositeKeyData()`: HeadersTotalDifficulty excluded

#### Before/After Behavior

**Before (Previous Behavior)**:
```
Moderate Mode: 🔄 Batch-based pruning
Aggressive Mode: 🔄 Batch-based pruning  
```

**After (Current Behavior)**:
```
All Modes: 🛡️ Protected (never touched)
```

## Impact Analysis

### Space Savings Impact
- **Before**: Could save ~10MB total from these 5 tables
- **After**: 0MB saved from these tables
- **Net Impact**: Negligible (<0.01% of typical database size)

### Risk Reduction
- **Before**: Small risk of breaking edge-case functionality
- **After**: Zero risk from these tables
- **Benefit**: Higher safety margin with negligible space trade-off

### Code Maintenance
- **Before**: Complex logic handling small tables in multiple functions
- **After**: Simple exclusion, cleaner code
- **Benefit**: Reduced maintenance burden, fewer edge cases

## Comparison With Other Protection Strategies

### Critical Tables (Always Protected)
- **SMT Tables**: Essential for zkEVM operation (60GB+)
- **System Tables**: Database integrity (Config, DbInfo, etc.)
- **Current State**: PlainState, HashedStorage, etc.

### Small Tables (User-Specified Protection)
- **Size-Based**: < 10MB each
- **Safety-Based**: Better safe than sorry
- **Maintenance-Based**: Reduces code complexity

### DupCursor Tables (Special Handling)
- **AccountChangeSet**: DupCursor handling in Aggressive mode
- **StorageChangeSet**: DupCursor handling in Aggressive mode  
- **CanonicalHeader**: DupCursor handling in Aggressive mode
- **hermez_blockBatches**: DupCursor handling in Aggressive mode

## Future Considerations

### Adding New Small Tables
If new small tables (< 10MB) are identified:
1. Evaluate cleanup benefit vs. risk
2. If benefit < 50MB, consider adding to protection list
3. Update this document and code accordingly

### Removing Protection
To remove protection from any of these tables:
1. Remove from `getCriticalTables()` function
2. Add back to appropriate pruning functions
3. Test thoroughly in staging environment
4. Update all documentation

## Configuration

These tables are **hard-coded** as protected. There is no configuration option to change this behavior.

### Rationale for Hard-Coding
- **Simplicity**: No configuration complexity
- **Safety**: Prevents accidental enabling of risky operations
- **User Intent**: Explicit request was to never clean these tables

## Testing Verification

To verify protection is working:

```bash
# Run pruning and check these tables are never mentioned in deletion logs
./prune-tool prune-chaindata /path/to/datadir aggressive --yes

# Check logs should NOT contain:
# - "Clearing table: block_l1_info_tree_index"
# - "Clearing table: plain_state_version"  
# - "Clearing table: smt_depths"
# - "Clearing table: HeadersTotalDifficulty"
# - "Clearing table: MaxTxNum"

# Instead should see:
# - "⊜ Skipped table: ... (small table protection)"
```

## Related Documentation

- [README.md](README.md): Main tool documentation
- [ACTIVE_TABLES_ANALYSIS.md](ACTIVE_TABLES_ANALYSIS.md): Complete table analysis
- [COMPLETE_TABLE_FORMATS.md](cmd/prune-chaindata/COMPLETE_TABLE_FORMATS.md): Table format reference

---

**Last Updated**: December 2024  
**Change Reason**: User request to protect small tables from cleanup operations
