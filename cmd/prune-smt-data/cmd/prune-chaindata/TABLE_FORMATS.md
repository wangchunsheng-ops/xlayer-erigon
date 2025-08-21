# Database Table Formats Analysis

This document provides analysis of the key database tables for partial pruning implementation.

> 📚 **Complete Reference**: For comprehensive analysis of ALL 192 tables, see [COMPLETE_TABLE_FORMATS.md](./COMPLETE_TABLE_FORMATS.md)

## 🎯 Purpose

This analysis focuses on the tables relevant to partial pruning implementation:
- **Accurate partial pruning** - understanding key formats for block number extraction
- **Safe database operations** - knowing which tables can be safely modified
- **Implementation guidance** - specific format details for the 7 supported tables

## 📊 Table Format Analysis

### ✅ Tables with Direct Block Number Keys (Work Correctly)

| Table Name | Key Format | Value Format | Block Number Location |
|------------|------------|--------------|----------------------|
| **Header** | `block_num_u64 + hash` | header (RLP) | **First 8 bytes of key** |
| **BlockBody** | `block_num_u64 + hash` | block body | **First 8 bytes of key** |
| **CanonicalHeader** | `block_num_u64` | header hash | **First 8 bytes of key** |
| **Receipt** | `block_num_u64` | canonical block receipts | **First 8 bytes of key** |
| **TxSender** | `block_num_u64 + blockHash` | sendersList | **First 8 bytes of key** |

### ❌ Tables with Different Key Formats (Current Implementation Fails)

| Table Name | Key Format | Value Format | Issue |
|------------|------------|--------------|-------|
| **BlockTransaction** | `tx_id_u64` | rlp(tx) | ❌ **Uses TX_ID, not block number!** |
| **TransactionLog** | `block_num_u64 + txId` | logs of transaction | ⚠️ **Partial: blockNum in first 8 bytes + txId** |
| **BlockTransactionLookup** | `transaction_hash` | lookup metadata | ❌ **Uses TX_HASH, not block number!** |
| **HeaderNumber** | `header_hash` | header_num_u64 | ❌ **Uses HEADER_HASH, block number in VALUE!** |

## 🔍 Detailed Format Specifications

### 1. Header Table
```
Table: "Header"
Key Format: [8 bytes block_num_u64][32 bytes hash]
Value: header (RLP encoded)
Example: [0x0000000000000001][0x1234...abcd] -> header_data
```

### 2. BlockBody Table  
```
Table: "BlockBody"
Key Format: [8 bytes block_num_u64][32 bytes hash]
Value: block body
Example: [0x0000000000000001][0x1234...abcd] -> block_body_data
```

### 3. CanonicalHeader Table
```
Table: "CanonicalHeader" 
Key Format: [8 bytes block_num_u64]
Value: header hash
Example: [0x0000000000000001] -> 0x1234...abcd
```

### 4. Receipt Table
```
Table: "Receipt"
Key Format: [8 bytes block_num_u64]
Value: canonical block receipts
Example: [0x0000000000000001] -> receipts_data
```

### 5. TxSender Table
```
Table: "TxSender"
Key Format: [8 bytes block_num_u64][32 bytes blockHash]
Value: sendersList (every 20 bytes = new sender address)
Example: [0x0000000000000001][0x1234...abcd] -> [sender1][sender2][sender3]...
```

### 6. TransactionLog Table
```
Table: "TransactionLog"
Key Format: [8 bytes block_num_u64][4 bytes txId]
Value: logs of transaction
Example: [0x0000000000000001][0x00000000] -> log_data
Note: BlockNumber can be extracted from first 8 bytes
```

### 7. BlockTransaction Table ❌ PROBLEMATIC
```
Table: "BlockTransaction"
Key Format: [8 bytes tx_id_u64]
Value: rlp(tx)
Example: [0x0000000000001234] -> transaction_data
❌ ISSUE: Key is TX_ID sequence, NOT block number!
💡 SOLUTION: Need to find block number through other means
```

### 8. BlockTransactionLookup Table ❌ PROBLEMATIC
```
Table: "BlockTransactionLookup" 
Key Format: [32 bytes transaction_hash]
Value: lookup metadata
Example: [0x1234...abcd] -> metadata
❌ ISSUE: Key is TX_HASH, NOT block number!
💡 SOLUTION: Need to parse metadata to find block number
```

### 9. HeaderNumber Table ❌ PROBLEMATIC
```
Table: "HeaderNumber"
Key Format: [32 bytes header_hash]
Value: [8 bytes header_num_u64]
Example: [0x1234...abcd] -> [0x0000000000000001]
❌ ISSUE: Key is HEADER_HASH, block number is in VALUE!
💡 SOLUTION: Extract block number from VALUE, not KEY
```

## 🚨 Current Implementation Problems

### Problem Analysis
Our current `extractBlockNumberFromKey()` function incorrectly assumes all tables have block numbers in the first 8 bytes of the key:

```go
// ❌ WRONG for BlockTransaction, BlockTransactionLookup, HeaderNumber
return binary.BigEndian.Uint64(key[:8]), nil
```

### Why Some Tables Don't Delete Data
1. **BlockTransaction**: Key is `tx_id`, not `block_num` → extractBlockNumberFromKey fails
2. **BlockTransactionLookup**: Key is `tx_hash`, not `block_num` → extractBlockNumberFromKey fails  
3. **HeaderNumber**: Key is `header_hash`, block number in value → extractBlockNumberFromKey fails

### Why Some Tables Work Correctly
1. **Header, BlockBody, TxSender**: Have `block_num` in first 8 bytes ✅
2. **CanonicalHeader, Receipt**: Key is exactly `block_num` ✅
3. **TransactionLog**: Has `block_num` in first 8 bytes ✅

## 🔧 Required Fixes

### 1. BlockTransaction Table
**Challenge**: Key is `tx_id`, not `block_num`
**Solutions**:
- Option A: Use MaxTxNum table to map block numbers to transaction ranges
- Option B: Skip partial pruning for this table (too complex to implement correctly)
- **Recommended**: Option B - exclude from partial pruning

### 2. BlockTransactionLookup Table  
**Challenge**: Key is `transaction_hash`
**Solutions**:
- Option A: Parse lookup metadata to extract block information
- Option B: Skip partial pruning for this table
- **Recommended**: Option B - exclude from partial pruning

### 3. HeaderNumber Table
**Challenge**: Block number is in VALUE, not KEY
**Solutions**:
- Option A: Read value to extract block number for each key
- Option B: Skip partial pruning (table is usually small)
- **Recommended**: Option A - implement value reading

### 4. TransactionLog Table
**Status**: ✅ Should work correctly (block_num in first 8 bytes)
**Issue**: May have failed due to composite key confusion
**Fix**: Verify the key extraction logic

## 📋 Corrected Implementation Strategy

### Safe Tables (Keep Partial Pruning)
- ✅ Header
- ✅ BlockBody  
- ✅ CanonicalHeader
- ✅ Receipt
- ✅ TxSender
- ⚠️ TransactionLog (fix key extraction)
- ⚠️ HeaderNumber (implement value-based extraction)

### Problematic Tables (Exclude from Partial Pruning)
- ❌ BlockTransaction (too complex - uses tx_id sequence)
- ❌ BlockTransactionLookup (too complex - uses tx_hash)

## 🎯 Updated Implementation Plan

1. **Fix extractBlockNumberFromKey()** for HeaderNumber (read value)
2. **Verify TransactionLog** key extraction logic  
3. **Exclude BlockTransaction and BlockTransactionLookup** from partial pruning
4. **Update documentation** to reflect accurate table handling
5. **Test with corrected implementation**

This analysis reveals that **partial pruning should focus on 7 tables instead of 9**, with 2 tables excluded due to implementation complexity.
