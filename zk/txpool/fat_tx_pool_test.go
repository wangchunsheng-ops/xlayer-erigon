package txpool

import (
	"testing"

	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFatTxPoolBasic tests basic fat transaction pool functionality
func TestFatTxPoolBasic(t *testing.T) {
	assert, require := assert.New(t), require.New(t)

	// Create a new fat transaction pool
	config := FatTxConfig{
		GasRatioThreshold: 0.05, // 5%
		PoolSizeLimit:     100,
		MaxRatio:          0.2, // 20%
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)
	require.NotNil(fatPool)

	// Test initial state
	assert.Equal(0, fatPool.Len())
	assert.True(fatPool.IsEnabled())

	// Test configuration
	assert.Equal(0.05, fatPool.config.GasRatioThreshold)
	assert.Equal(100, fatPool.config.PoolSizeLimit)
	assert.Equal(0.2, fatPool.config.MaxRatio)
}

// TestFatTransactionDetection tests fat transaction detection logic
func TestFatTransactionDetection(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05, // 5%
		PoolSizeLimit:     100,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	blockGasLimit := uint64(1000000) // 1M gas

	// Test normal transaction (should not be fat)
	normalTx := &types.TxSlot{
		Gas: 21000, // Normal gas usage
	}
	assert.False(fatPool.IsFatTransaction(normalTx, blockGasLimit))

	// Test fat transaction (should be fat)
	fatTx := &types.TxSlot{
		Gas: 100000, // 10% of block gas limit
	}
	assert.True(fatPool.IsFatTransaction(fatTx, blockGasLimit))

	// Test edge case
	edgeTx := &types.TxSlot{
		Gas: 50000, // Exactly 5% of block gas limit
	}
	assert.False(fatPool.IsFatTransaction(edgeTx, blockGasLimit))

	// Test disabled pool
	fatPool.config.Enabled = false
	assert.False(fatPool.IsFatTransaction(fatTx, blockGasLimit))
}

// TestFatTxPoolAddRemove tests adding and removing transactions
func TestFatTxPoolAddRemove(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     3, // Small limit for testing
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Create test transactions
	tx1 := createTestMetaTx(1, 100000)
	tx2 := createTestMetaTx(2, 100000)
	tx3 := createTestMetaTx(3, 100000)
	tx4 := createTestMetaTx(4, 100000) // This should be rejected due to pool size limit

	// Add transactions
	assert.True(fatPool.AddTransaction(tx1))
	assert.Equal(1, fatPool.Len())

	assert.True(fatPool.AddTransaction(tx2))
	assert.Equal(2, fatPool.Len())

	assert.True(fatPool.AddTransaction(tx3))
	assert.Equal(3, fatPool.Len())

	// Try to add more than pool limit
	assert.False(fatPool.AddTransaction(tx4))
	assert.Equal(3, fatPool.Len()) // Should still be 3

	// Remove a transaction
	fatPool.RemoveTransaction(tx2)
	assert.Equal(2, fatPool.Len())

	// Now we should be able to add tx4
	assert.True(fatPool.AddTransaction(tx4))
	assert.Equal(3, fatPool.Len())
}

// TestFatTxPoolGetTransactions tests getting transactions from the pool
func TestFatTxPoolGetTransactions(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Add some transactions
	for i := 0; i < 5; i++ {
		tx := createTestMetaTx(uint64(i), 100000)
		fatPool.AddTransaction(tx)
	}

	assert.Equal(5, fatPool.Len())

	// Get transactions
	availableGas := uint64(1000000)
	txs := fatPool.GetTransactions(3, availableGas)
	assert.Equal(3, len(txs))

	// Get more transactions than available
	txs = fatPool.GetTransactions(10, availableGas)
	assert.Equal(5, len(txs)) // Should return all available

	// Test with insufficient gas
	txs = fatPool.GetTransactions(3, 50000) // Not enough gas for any transaction
	assert.Equal(0, len(txs))
}

// TestFatTxPoolStats tests statistics functionality
func TestFatTxPoolStats(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Initial stats
	stats := fatPool.GetStats()
	assert.Equal(int64(0), stats.TotalFatTxs)
	assert.Equal(int64(0), stats.ProcessedFatTxs)
	assert.Equal(int64(0), stats.RejectedFatTxs)

	// Add some transactions
	for i := 0; i < 3; i++ {
		tx := createTestMetaTx(uint64(i), 100000)
		fatPool.AddTransaction(tx)
	}

	// Try to add more than limit to trigger rejections
	for i := 3; i < 15; i++ {
		tx := createTestMetaTx(uint64(i), 100000)
		fatPool.AddTransaction(tx)
	}

	// Get updated stats
	stats = fatPool.GetStats()
	assert.Equal(int64(10), stats.TotalFatTxs)    // 10 transactions added (pool limit is 10)
	assert.Equal(int64(0), stats.ProcessedFatTxs) // No transactions processed yet
	assert.Equal(int64(5), stats.RejectedFatTxs)  // 5 transactions rejected (10 added, 5 rejected due to pool limit)
}

// TestFatTxPoolConfigUpdate tests configuration updates
func TestFatTxPoolConfigUpdate(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Test enable/disable
	fatPool.Disable()
	assert.False(fatPool.IsEnabled())

	fatPool.Enable()
	assert.True(fatPool.IsEnabled())

	// Test config update
	newConfig := FatTxConfig{
		GasRatioThreshold: 0.1, // 10%
		PoolSizeLimit:     20,
		MaxRatio:          0.3, // 30%
		Enabled:           true,
	}

	fatPool.UpdateConfig(newConfig)
	assert.Equal(0.1, fatPool.config.GasRatioThreshold)
	assert.Equal(20, fatPool.config.PoolSizeLimit)
	assert.Equal(0.3, fatPool.config.MaxRatio)
}

// createTestMetaTx creates a test metaTx for testing
func createTestMetaTx(nonce uint64, gas uint64) *metaTx {
	txSlot := &types.TxSlot{
		Gas:   gas,
		Nonce: nonce,
	}
	txSlot.IDHash[0] = byte(nonce)

	return &metaTx{
		Tx:             txSlot,
		currentSubPool: FatTxsSubPool,
	}
}

// TestFatTxPoolReadTransactionsForContext tests the ReadTransactionsForContext function
func TestFatTxPoolReadTransactionsForContext(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Add some transactions first
	for i := 0; i < 3; i++ {
		tx := createTestMetaTx(uint64(i), 100000)
		fatPool.AddTransaction(tx)
	}

	// Test that the pool has transactions
	assert.Equal(3, fatPool.Len(), "Should have 3 transactions in pool")

	// Test GetPool function
	pool := fatPool.GetPool()
	assert.NotNil(pool, "Should return a valid pool")
	assert.Equal(FatTxsSubPool, pool.t, "Should return fat transaction sub pool")
}

// TestFatTxPoolGetTransactionsEdgeCases tests edge cases in GetTransactions
func TestFatTxPoolGetTransactionsEdgeCases(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05,
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Test 1: Empty pool
	t.Run("EmptyPool", func(t *testing.T) {
		txs := fatPool.GetTransactions(5, 1000000)
		assert.Equal(0, len(txs), "Should return empty slice for empty pool")
	})

	// Test 2: Disabled pool
	t.Run("DisabledPool", func(t *testing.T) {
		fatPool.Disable()
		txs := fatPool.GetTransactions(5, 1000000)
		assert.Equal(0, len(txs), "Should return empty slice when disabled")
		fatPool.Enable()
	})

	// Test 3: Insufficient gas
	t.Run("InsufficientGas", func(t *testing.T) {
		// Add a transaction
		tx := createTestMetaTx(0, 100000)
		fatPool.AddTransaction(tx)

		// Try to get with insufficient gas
		txs := fatPool.GetTransactions(5, 50000)
		assert.Equal(0, len(txs), "Should return empty slice when gas is insufficient")
	})

	// Test 4: Request more transactions than available
	t.Run("RequestMoreThanAvailable", func(t *testing.T) {
		// Add a few transactions
		for i := 0; i < 2; i++ {
			tx := createTestMetaTx(uint64(i+1), 50000)
			fatPool.AddTransaction(tx)
		}

		// Request more than available
		txs := fatPool.GetTransactions(10, 1000000)
		assert.LessOrEqual(len(txs), 3, "Should not return more transactions than available")
	})
}
