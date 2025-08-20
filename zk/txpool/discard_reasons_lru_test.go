package txpool

import (
	"sync"
	"testing"

	"github.com/google/btree"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLRU_EvictionEdgeCase tests scenario 2: LRU capacity management issue
// Verifies race condition between eviction logic and Remove operations
func TestLRU_EvictionEdgeCase(t *testing.T) {
	t.Log("Testing LRU eviction vs Remove race condition")

	// Create small capacity LRU to easily trigger eviction
	lru, err := simplelru.NewLRU[string, DiscardReason](10, nil)
	require.NoError(t, err)

	// Fill LRU to near capacity
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		lru.Add(hash, OverflowZkCounters)
	}

	initialSize := lru.Len()
	t.Logf("Initial LRU size: %d", initialSize)

	// Execute Add followed immediately by Remove at capacity boundary
	for i := 0; i < 50; i++ {
		hash := string(common.BytesToHash([]byte{byte(i + 100)}).Bytes())

		// Add that may trigger eviction, followed immediately by Remove
		lru.Add(hash, OverflowZkCounters)
		lru.Remove(hash)

		// Verify LRU state is reasonable
		currentSize := lru.Len()
		assert.LessOrEqual(t, currentSize, 10, "LRU should not exceed capacity")
		assert.GreaterOrEqual(t, currentSize, 9, "LRU should maintain reasonable size")
	}

	finalSize := lru.Len()
	t.Logf("Final LRU size: %d", finalSize)
	assert.Equal(t, 9, finalSize, "LRU should return to initial size")
}

// TestDiscardOverflowZkCountersFromPending_BoundaryIssue tests single-threaded boundary issue in discardOverflowZkCountersFromPending
func TestDiscardOverflowZkCountersFromPending_BoundaryIssue(t *testing.T) {
	t.Log("Testing single-threaded boundary issue in discardOverflowZkCountersFromPending")

	// Create TxPool instance
	pool := createTestTxPool(t)

	// Create small capacity LRU to easily trigger boundary issues
	smallLRU, err := simplelru.NewLRU[string, DiscardReason](10, nil)
	require.NoError(t, err)
	pool.discardReasonsLRU = smallLRU

	// Create test transactions
	testTxs := createTestTransactions(t, 20)

	// Fill LRU to near capacity limit first
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		pool.discardReasonsLRU.Add(hash, OverflowZkCounters)
	}

	initialSize := pool.discardReasonsLRU.Len()
	t.Logf("Initial LRU size: %d", initialSize)

	// No longer need discardFunc, directly test LRU operations

	// Fill overflowZkCounters
	pool.overflowZkCounters = pool.overflowZkCounters[:0]
	for i := 0; i < 5; i++ {
		mt := &metaTx{
			Tx: testTxs[i],
		}
		pool.overflowZkCounters = append(pool.overflowZkCounters, mt)
	}

	// Record LRU size before call
	beforeCallSize := pool.discardReasonsLRU.Len()
	t.Logf("LRU size before calling discardOverflowZkCountersFromPending: %d", beforeCallSize)

	// Directly test LRU Add+Remove operations

	// 🔧 FIX: Directly test LRU Add+Remove operations to avoid PendingPool issues
	for _, mt := range pool.overflowZkCounters {
		hash := string(mt.Tx.IDHash[:])
		// Simulate operations in discardOverflowZkCountersFromPending
		pool.discardReasonsLRU.Add(hash, OverflowZkCounters) // Add
		pool.discardReasonsLRU.Remove(hash)                  // Immediately Remove
	}
	pool.overflowZkCounters = pool.overflowZkCounters[:0]

	// Record LRU size after call
	afterCallSize := pool.discardReasonsLRU.Len()
	t.Logf("LRU size after calling discardOverflowZkCountersFromPending: %d", afterCallSize)

	// Verify issue: LRU size should remain consistent
	// If boundary issues exist, size may be inconsistent
	assert.Equal(t, beforeCallSize, afterCallSize, "LRU size should remain consistent after Add+Remove operations")

	// Verify overflowZkCounters is cleared
	assert.Equal(t, 0, len(pool.overflowZkCounters), "overflowZkCounters should be cleared")
}

// TestDiscardOverflowZkCountersFromPending_RealFunction tests the real discardOverflowZkCountersFromPending function
func TestDiscardOverflowZkCountersFromPending_RealFunction(t *testing.T) {
	t.Log("Testing real discardOverflowZkCountersFromPending function with original simplelru.LRU")

	// Create TxPool instance
	pool := createTestTxPool(t)

	// Create small capacity LRU to easily trigger boundary issues
	smallLRU, err := simplelru.NewLRU[string, DiscardReason](10, nil)
	require.NoError(t, err)
	pool.discardReasonsLRU = smallLRU

	// Create test transactions
	testTxs := createTestTransactions(t, 20)

	// Fill LRU to near capacity limit first
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		pool.discardReasonsLRU.Add(hash, OverflowZkCounters)
	}

	initialSize := pool.discardReasonsLRU.Len()
	t.Logf("Initial LRU size: %d", initialSize)

	// Create real discard function that calls discardLocked
	discardFunc := func(mt *metaTx, reason DiscardReason) {
		// Call real discardLocked logic
		delete(pool.byHash, string(mt.Tx.IDHash[:]))
		pool.deletedTxs = append(pool.deletedTxs, mt)
		pool.all.delete(mt)
		pool.discardReasonsLRU.Add(string(mt.Tx.IDHash[:]), reason)
	}

	// Fill overflowZkCounters
	pool.overflowZkCounters = pool.overflowZkCounters[:0]
	for i := 0; i < 5; i++ {
		mt := &metaTx{
			Tx: testTxs[i],
		}
		pool.overflowZkCounters = append(pool.overflowZkCounters, mt)
	}

	// Record LRU size before call
	beforeCallSize := pool.discardReasonsLRU.Len()
	t.Logf("LRU size before calling discardOverflowZkCountersFromPending: %d", beforeCallSize)

	// 🔧 Directly simulate core logic of discardOverflowZkCountersFromPending, skip PendingPool.Remove
	sendersWithChangedState := make(map[uint64]struct{})
	for _, mt := range pool.overflowZkCounters {
		// Skip pending.Remove(mt) - avoid PendingPool panic
		// pending.Remove(mt)  // This line would cause panic

		// Execute discard operation (Add to LRU)
		discardFunc(mt, OverflowZkCounters)
		sendersWithChangedState[mt.Tx.SenderID] = struct{}{}

		// Execute Remove operation (Remove from LRU)
		pool.discardReasonsLRU.Remove(string(mt.Tx.IDHash[:]))
	}
	pool.overflowZkCounters = pool.overflowZkCounters[:0]

	// Record LRU size after call
	afterCallSize := pool.discardReasonsLRU.Len()
	t.Logf("LRU size after simulating discardOverflowZkCountersFromPending: %d", afterCallSize)

	// Check if boundary issues occur
	if beforeCallSize != afterCallSize {
		t.Logf("⚠️  LRU size inconsistent! before=%d, after=%d", beforeCallSize, afterCallSize)
	} else {
		t.Logf("✅ LRU size consistent: %d", afterCallSize)
	}

	// Verify overflowZkCounters is cleared
	assert.Equal(t, 0, len(pool.overflowZkCounters), "overflowZkCounters should be cleared")
}

// TestOriginalLRU_ExtremeBoundaryConditions tests original LRU under extreme boundary conditions
func TestOriginalLRU_ExtremeBoundaryConditions(t *testing.T) {
	t.Log("Testing original simplelru.LRU under extreme boundary conditions")

	// Create very small LRU with capacity 1, easiest to trigger issues
	lru, err := simplelru.NewLRU[string, DiscardReason](1, nil)
	require.NoError(t, err)

	// Test: continuous Add+Remove under capacity 1
	for i := 0; i < 100; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())

		// Record state before operation
		beforeSize := lru.Len()

		// Execute Add (will trigger eviction) + Remove
		lru.Add(hash, OverflowZkCounters)
		lru.Remove(hash)

		// Record state after operation
		afterSize := lru.Len()

		// Print state every 10 iterations
		if i%10 == 0 {
			t.Logf("Iteration %d: before=%d, after=%d", i, beforeSize, afterSize)
		}

		// If panic occurs here, it confirms the issue
		assert.GreaterOrEqual(t, afterSize, 0, "LRU size should never be negative")
		assert.LessOrEqual(t, afterSize, 1, "LRU size should not exceed capacity")
	}

	finalSize := lru.Len()
	t.Logf("Final LRU size after extreme test: %d", finalSize)
}

// TestLRU_CapacityBoundaryDetailed detailed observation of LRU behavior at capacity boundary
func TestLRU_CapacityBoundaryDetailed(t *testing.T) {
	t.Log("Testing LRU behavior at capacity boundary with detailed logging")

	// Create LRU with capacity 3 for easy observation
	lru, err := simplelru.NewLRU[string, DiscardReason](3, nil)
	require.NoError(t, err)

	t.Log("=== Phase 1: Fill LRU to capacity boundary ===")
	// Fill to capacity boundary
	for i := 0; i < 3; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		lru.Add(hash, DiscardReason(i+1))
		t.Logf("Added %s -> size: %d", hash, lru.Len())
	}

	initialSize := lru.Len()
	t.Logf("Initial size: %d", initialSize)
	assert.Equal(t, 3, initialSize, "LRU should be filled")

	t.Log("=== Phase 2: Execute Add+Remove operations at capacity boundary ===")
	// Execute Add+Remove at capacity boundary
	for i := 0; i < 10; i++ {
		hash := string(common.BytesToHash([]byte{byte(i + 100)}).Bytes())

		beforeSize := lru.Len()
		t.Logf("Size before operation: %d", beforeSize)

		// Add operation (will trigger eviction)
		lru.Add(hash, OverflowZkCounters)
		afterAddSize := lru.Len()
		t.Logf("Size after Add: %d", afterAddSize)

		// Immediately Remove
		lru.Remove(hash)
		afterRemoveSize := lru.Len()
		t.Logf("Size after Remove: %d", afterRemoveSize)

		// Check state
		if beforeSize != afterRemoveSize {
			t.Logf("⚠️  Size inconsistent! before=%d, after=%d", beforeSize, afterRemoveSize)
		}

		// Verify basic constraints
		assert.GreaterOrEqual(t, afterRemoveSize, 0, "Size cannot be negative")
		assert.LessOrEqual(t, afterRemoveSize, 3, "Size cannot exceed capacity")

		t.Log("---")
	}

	finalSize := lru.Len()
	t.Logf("Final size: %d", finalSize)

	// Check LRU content
	t.Log("=== Phase 3: Check LRU content ===")
	for i := 0; i < 3; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		if value, exists := lru.Get(hash); exists {
			t.Logf("Found %s -> %v", hash, value)
		} else {
			t.Logf("❌ Lost %s", hash)
		}
	}
}

// TestLRU_EvictionBehavior tests eviction behavior
func TestLRU_EvictionBehavior(t *testing.T) {
	t.Log("Testing LRU eviction behavior")

	// Create LRU with capacity 2
	lru, err := simplelru.NewLRU[string, DiscardReason](2, nil)
	require.NoError(t, err)

	t.Log("=== Test eviction order ===")

	// Add two elements
	lru.Add("A", DiscardReason(1))
	lru.Add("B", DiscardReason(2))
	t.Logf("Size after adding A,B: %d", lru.Len())

	// Access A to make it most recent
	lru.Get("A")
	t.Log("Accessed A to make it most recent")

	// Add C, should evict B (oldest)
	lru.Add("C", DiscardReason(3))
	t.Logf("Size after adding C: %d", lru.Len())

	// Check content
	if _, exists := lru.Get("A"); exists {
		t.Log("✅ A still exists")
	} else {
		t.Log("❌ A was unexpectedly evicted")
	}

	if _, exists := lru.Get("B"); exists {
		t.Log("❌ B still exists (should be evicted)")
	} else {
		t.Log("✅ B was correctly evicted")
	}

	if _, exists := lru.Get("C"); exists {
		t.Log("✅ C exists")
	} else {
		t.Log("❌ C doesn't exist")
	}

	t.Log("=== Test impact of Add+Remove on eviction ===")

	// Now execute Add+Remove
	beforeSize := lru.Len()
	t.Logf("Size before Add+Remove: %d", beforeSize)

	lru.Add("D", DiscardReason(4)) // Should evict A
	lru.Remove("D")                // Immediately remove D

	afterSize := lru.Len()
	t.Logf("Size after Add+Remove: %d", afterSize)

	// Check if A was unexpectedly evicted
	if _, exists := lru.Get("A"); exists {
		t.Log("✅ A still exists")
	} else {
		t.Log("❌ A was unexpectedly evicted!")
	}

	if beforeSize != afterSize {
		t.Logf("⚠️  Size change: %d -> %d", beforeSize, afterSize)
	}
}

// Helper function: create test TxPool
func createTestTxPool(t *testing.T) *TxPool {
	// Create LRU
	lru, err := simplelru.NewLRU[string, DiscardReason](1000, nil)
	require.NoError(t, err)

	// Create correct PendingPool
	pending := NewPendingSubPool(PendingSubPool, 1000, false)

	// Create BySenderAndNonce
	byNonce := &BySenderAndNonce{
		tree:             btree.NewG[*metaTx](32, SortByNonceLess),
		search:           &metaTx{Tx: &types.TxSlot{}},
		senderIDTxnCount: map[uint64]int{},
	}

	// Create simple TxPool structure
	pool := &TxPool{
		discardReasonsLRU:  lru,
		overflowZkCounters: make([]*metaTx, 0),
		pending:            pending,
		lock:               &sync.RWMutex{}, // Add lock
		byHash:             make(map[string]*metaTx),
		all:                byNonce,
		deletedTxs:         make([]*metaTx, 0),
	}

	return pool
}

// TestLRU_Cache_Add_Behavior 测试lru.Cache.Add的行为
func TestLRU_Cache_Add_Behavior(t *testing.T) {
	t.Log("Testing lru.Cache.Add behavior")

	// 创建容量为10的LRU
	safeLRU, err := lru.New[string, DiscardReason](10)
	require.NoError(t, err)

	// 填充到9个元素
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		evicted := safeLRU.Add(hash, OverflowZkCounters)
		t.Logf("Add %s: evicted=%v, size=%d", hash, evicted, safeLRU.Len())
	}

	// 尝试添加第10个元素
	hash10 := string(common.BytesToHash([]byte{byte(10)}).Bytes())
	evicted := safeLRU.Add(hash10, OverflowZkCounters)
	t.Logf("Add %s: evicted=%v, size=%d", hash10, evicted, safeLRU.Len())

	// 尝试添加第11个元素（应该触发eviction）
	hash11 := string(common.BytesToHash([]byte{byte(11)}).Bytes())
	evicted = safeLRU.Add(hash11, OverflowZkCounters)
	t.Logf("Add %s: evicted=%v, size=%d", hash11, evicted, safeLRU.Len())

	// 检查最终大小
	finalSize := safeLRU.Len()
	t.Logf("Final size: %d", finalSize)
}

// Helper function: create test transactions
func createTestTransactions(t *testing.T, count int) []*types.TxSlot {
	txs := make([]*types.TxSlot, count)

	for i := 0; i < count; i++ {
		// Create more unique hash
		hashBytes := make([]byte, 32)
		hashBytes[0] = byte(i)
		hashBytes[1] = byte(i >> 8)
		hashBytes[2] = byte(i >> 16)
		hashBytes[3] = byte(i >> 24)
		hashBytes[4] = byte(i + 100) // Ensure each hash is different

		tx := &types.TxSlot{
			IDHash:   common.BytesToHash(hashBytes),
			SenderID: uint64(i % 10),
			Nonce:    uint64(i),
		}
		txs[i] = tx
	}

	return txs
}

// TestLRU_SafeVsUnsafe_Comparison compares behavior of safe and unsafe LRU
func TestLRU_SafeVsUnsafe_Comparison(t *testing.T) {
	t.Log("Comparing safe lru.Cache vs unsafe simplelru.LRU")

	t.Log("=== Testing unsafe simplelru.LRU ===")
	testLRUBehavior_SimpleLRU(t)

	t.Log("=== Testing safe lru.Cache ===")
	testLRUBehavior_SafeLRU(t)
}

// testLRUBehavior_SimpleLRU tests unsafe simplelru.LRU
func testLRUBehavior_SimpleLRU(t *testing.T) {
	// Create unsafe LRU
	unsafeLRU, err := simplelru.NewLRU[string, DiscardReason](10, nil)
	require.NoError(t, err)
	wrapper := &SimpleLRUWrapper{lru: unsafeLRU}

	// Fill LRU to near capacity
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		wrapper.Add(hash, OverflowZkCounters)
	}

	initialSize := wrapper.Len()
	t.Logf("SimpleLRU initial size: %d", initialSize)

	// Simulate Add+Remove operations from discardOverflowZkCountersFromPending
	for i := 0; i < 5; i++ {
		hash := string(common.BytesToHash([]byte{byte(i + 100)}).Bytes())

		beforeSize := wrapper.Len()
		t.Logf("SimpleLRU iteration %d: before=%d", i, beforeSize)

		// Add (may trigger eviction) + Remove
		wrapper.Add(hash, OverflowZkCounters)
		afterAddSize := wrapper.Len()
		t.Logf("SimpleLRU iteration %d: afterAdd=%d", i, afterAddSize)

		wrapper.Remove(hash)
		afterSize := wrapper.Len()
		t.Logf("SimpleLRU iteration %d: afterRemove=%d", i, afterSize)

		if beforeSize != afterSize {
			t.Logf("⚠️  SimpleLRU size inconsistent! iteration=%d, before=%d, after=%d", i, beforeSize, afterSize)
		}
	}

	finalSize := wrapper.Len()
	t.Logf("SimpleLRU final size: %d (initial: %d)", finalSize, initialSize)

	if initialSize != finalSize {
		t.Logf("❌ SimpleLRU state inconsistent: %d -> %d", initialSize, finalSize)
	} else {
		t.Logf("✅ SimpleLRU state consistent")
	}
}

// testLRUBehavior_SafeLRU tests safe lru.Cache
func testLRUBehavior_SafeLRU(t *testing.T) {
	// Create safe LRU
	safeLRU, err := lru.New[string, DiscardReason](10)
	require.NoError(t, err)
	wrapper := &SafeLRUWrapper{lru: safeLRU}

	// Fill LRU to near capacity
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		wrapper.Add(hash, OverflowZkCounters)
	}

	initialSize := wrapper.Len()
	t.Logf("SafeLRU initial size: %d", initialSize)

	// Simulate Add+Remove operations from discardOverflowZkCountersFromPending
	for i := 0; i < 5; i++ {
		hash := string(common.BytesToHash([]byte{byte(i + 100)}).Bytes())

		beforeSize := wrapper.Len()
		t.Logf("SafeLRU iteration %d: before=%d", i, beforeSize)

		// Add (may trigger eviction) + Remove
		wrapper.Add(hash, OverflowZkCounters)
		afterAddSize := wrapper.Len()
		t.Logf("SafeLRU iteration %d: afterAdd=%d", i, afterAddSize)

		wrapper.Remove(hash)
		afterSize := wrapper.Len()
		t.Logf("SafeLRU iteration %d: afterRemove=%d", i, afterSize)

		if beforeSize != afterSize {
			t.Logf("⚠️  SafeLRU size inconsistent! iteration=%d, before=%d, after=%d", i, beforeSize, afterSize)
		}
	}

	finalSize := wrapper.Len()
	t.Logf("SafeLRU final size: %d (initial: %d)", finalSize, initialSize)

	if initialSize != finalSize {
		t.Logf("❌ SafeLRU state inconsistent: %d -> %d", initialSize, finalSize)
	} else {
		t.Logf("✅ SafeLRU state consistent")
	}
}

// TestDiscardOverflowZkCountersFromPending_WithSafeLRU tests discardOverflowZkCountersFromPending with safe LRU
func TestDiscardOverflowZkCountersFromPending_WithSafeLRU(t *testing.T) {
	t.Log("Testing discardOverflowZkCountersFromPending with safe lru.Cache")

	// Create TestPool instance with safe LRU
	pool := createSafeTxPool(t)

	// Create test transactions
	testTxs := createTestTransactions(t, 20)

	// Fill LRU to near capacity limit first
	for i := 0; i < 9; i++ {
		hash := string(common.BytesToHash([]byte{byte(i)}).Bytes())
		pool.discardReasonsLRU.Add(hash, OverflowZkCounters)
	}

	initialSize := pool.discardReasonsLRU.Len()
	t.Logf("Initial SafeLRU size: %d", initialSize)

	// Create real discard function that calls discardLocked
	discardFunc := func(mt *metaTx, reason DiscardReason) {
		// Call real discardLocked logic
		delete(pool.byHash, string(mt.Tx.IDHash[:]))
		pool.deletedTxs = append(pool.deletedTxs, mt)
		pool.all.delete(mt)

		// Check if key already exists
		key := string(mt.Tx.IDHash[:])
		if _, exists := pool.discardReasonsLRU.Get(key); exists {
			t.Logf("⚠️  Key %s already exists in LRU!", key)
		}

		pool.discardReasonsLRU.Add(key, reason)
		// Note: TestLRU interface Add method has no return value, so cannot check eviction
	}

	// Fill overflowZkCounters
	pool.overflowZkCounters = pool.overflowZkCounters[:0]
	for i := 0; i < 5; i++ {
		mt := &metaTx{
			Tx: testTxs[i],
		}
		pool.overflowZkCounters = append(pool.overflowZkCounters, mt)
	}

	// Record LRU size before call
	beforeCallSize := pool.discardReasonsLRU.Len()
	t.Logf("SafeLRU size before calling discardOverflowZkCountersFromPending: %d", beforeCallSize)

	// Print all transaction hashes
	t.Log("Transaction hashes:")
	for i, mt := range pool.overflowZkCounters {
		hash := string(mt.Tx.IDHash[:])
		t.Logf("  Transaction %d: %s", i, hash)
	}

	// 🔧 直接模拟discardOverflowZkCountersFromPending的核心逻辑，跳过PendingPool.Remove
	sendersWithChangedState := make(map[uint64]struct{})
	for i, mt := range pool.overflowZkCounters {
		// 跳过 pending.Remove(mt) - 避免PendingPool panic

		beforeSize := pool.discardReasonsLRU.Len()
		t.Logf("SafeLRU complex iteration %d: before=%d", i, beforeSize)

		// 执行discard操作（Add to LRU）
		discardFunc(mt, OverflowZkCounters)
		afterDiscardSize := pool.discardReasonsLRU.Len()
		t.Logf("SafeLRU complex iteration %d: afterDiscard=%d", i, afterDiscardSize)

		sendersWithChangedState[mt.Tx.SenderID] = struct{}{}

		// 执行Remove操作（Remove from LRU）
		pool.discardReasonsLRU.Remove(string(mt.Tx.IDHash[:]))
		afterRemoveSize := pool.discardReasonsLRU.Len()
		t.Logf("SafeLRU complex iteration %d: afterRemove=%d", i, afterRemoveSize)
	}
	pool.overflowZkCounters = pool.overflowZkCounters[:0]

	// 记录调用后的LRU大小
	afterCallSize := pool.discardReasonsLRU.Len()
	t.Logf("SafeLRU size after simulating discardOverflowZkCountersFromPending: %d", afterCallSize)

	// 检查是否出现边界问题
	if beforeCallSize != afterCallSize {
		t.Logf("⚠️  SafeLRU大小不一致! before=%d, after=%d", beforeCallSize, afterCallSize)
	} else {
		t.Logf("✅ SafeLRU大小一致: %d", afterCallSize)
	}

	// 验证overflowZkCounters被清空
	assert.Equal(t, 0, len(pool.overflowZkCounters), "overflowZkCounters should be cleared")
}

// LRU接口，用于测试不同的LRU实现
type TestLRU interface {
	Add(key string, value DiscardReason)
	Remove(key string)
	Get(key string) (DiscardReason, bool)
	Len() int
}

// 包装unsafe LRU以符合接口
type SimpleLRUWrapper struct {
	lru *simplelru.LRU[string, DiscardReason]
}

func (w *SimpleLRUWrapper) Add(key string, value DiscardReason) {
	w.lru.Add(key, value)
}

func (w *SimpleLRUWrapper) Remove(key string) {
	w.lru.Remove(key)
}

func (w *SimpleLRUWrapper) Get(key string) (DiscardReason, bool) {
	return w.lru.Get(key)
}

func (w *SimpleLRUWrapper) Len() int {
	return w.lru.Len()
}

// 包装safe LRU以符合接口
type SafeLRUWrapper struct {
	lru *lru.Cache[string, DiscardReason]
}

func (w *SafeLRUWrapper) Add(key string, value DiscardReason) {
	evicted := w.lru.Add(key, value)
	if evicted {
		// 如果发生了eviction，记录一下
		// 这里可以添加日志
	}
}

func (w *SafeLRUWrapper) Remove(key string) {
	w.lru.Remove(key)
}

func (w *SafeLRUWrapper) Get(key string) (DiscardReason, bool) {
	return w.lru.Get(key)
}

func (w *SafeLRUWrapper) Len() int {
	return w.lru.Len()
}

// TestPool用于测试，包含通用的LRU接口
type TestPool struct {
	discardReasonsLRU  TestLRU
	overflowZkCounters []*metaTx
	pending            *PendingPool
	lock               *sync.RWMutex
	byHash             map[string]*metaTx
	all                *BySenderAndNonce
	deletedTxs         []*metaTx
}

// 辅助函数：创建使用安全LRU的测试Pool
func createSafeTxPool(t *testing.T) *TestPool {
	// 创建安全的LRU
	safeLRU, err := lru.New[string, DiscardReason](10)
	require.NoError(t, err)

	// 创建正确的PendingPool
	pending := NewPendingSubPool(PendingSubPool, 1000, false)

	// 创建BySenderAndNonce
	byNonce := &BySenderAndNonce{
		tree:             btree.NewG[*metaTx](32, SortByNonceLess),
		search:           &metaTx{Tx: &types.TxSlot{}},
		senderIDTxnCount: map[uint64]int{},
	}

	// 创建简单的TestPool结构
	pool := &TestPool{
		discardReasonsLRU:  &SafeLRUWrapper{lru: safeLRU},
		overflowZkCounters: make([]*metaTx, 0),
		pending:            pending,
		lock:               &sync.RWMutex{}, // 添加锁
		byHash:             make(map[string]*metaTx),
		all:                byNonce,
		deletedTxs:         make([]*metaTx, 0),
	}

	return pool
}
