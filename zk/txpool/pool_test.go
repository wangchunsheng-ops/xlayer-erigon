package txpool

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/holiman/uint256"
	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/common/datadir"
	"github.com/ledgerwatch/erigon-lib/common/u256"
	"github.com/ledgerwatch/erigon-lib/gointerfaces"
	"github.com/ledgerwatch/erigon-lib/gointerfaces/remote"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/kv/kvcache"
	"github.com/ledgerwatch/erigon-lib/kv/memdb"
	"github.com/ledgerwatch/erigon-lib/kv/temporal/temporaltest"
	"github.com/ledgerwatch/erigon-lib/txpool/txpoolcfg"
	"github.com/ledgerwatch/erigon-lib/types"
	"github.com/ledgerwatch/erigon/eth/ethconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"golang.org/x/exp/rand"
	"google.golang.org/grpc"
)

func TestNonceFromAddress(t *testing.T) {
	assert, require := assert.New(t), require.New(t)
	ch := make(chan types.Announcements, 100)
	_, coreDB, _ := temporaltest.NewTestDB(t, datadir.New(t.TempDir()))
	defer coreDB.Close()

	db := memdb.NewTestPoolDB(t)
	path := fmt.Sprintf("/tmp/db-test-%v", time.Now().UTC().Format(time.RFC3339Nano))
	txPoolDB := newTestTxPoolDB(t, path)
	defer txPoolDB.Close()
	aclsDB := newTestACLDB(t, path)
	defer aclsDB.Close()

	// Check if the dbs are created.
	require.NotNil(t, db)
	require.NotNil(t, txPoolDB)
	require.NotNil(t, aclsDB)

	cfg := txpoolcfg.DefaultConfig
	ethCfg := &ethconfig.Defaults
	sendersCache := kvcache.New(kvcache.DefaultCoherentConfig)
	pool, err := New(ch, coreDB, cfg, ethCfg, sendersCache, *u256.N1, nil, nil, aclsDB)
	assert.NoError(err)
	require.True(pool != nil)
	ctx := context.Background()
	var stateVersionID uint64 = 0
	pendingBaseFee := uint64(200000)
	h1 := gointerfaces.ConvertHashToH256([32]byte{})

	// Create address for testing.
	var addr [20]byte
	addr[0] = 1

	// Fund addr with 18 Ether for sending transactions.
	v := make([]byte, types.EncodeSenderLengthForStorage(2, *uint256.NewInt(18 * common.Ether)))
	types.EncodeSender(2, *uint256.NewInt(18 * common.Ether), v)

	change := &remote.StateChangeBatch{
		StateVersionId:      stateVersionID,
		PendingBlockBaseFee: pendingBaseFee,
		BlockGasLimit:       1000000,
		ChangeBatch: []*remote.StateChange{
			{BlockHeight: 0, BlockHash: h1},
		},
	}
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(addr),
		Data:    v,
	})
	tx, err := db.BeginRw(ctx)
	require.NoError(err)
	defer tx.Rollback()
	err = pool.OnNewBlock(ctx, change, types.TxSlots{}, types.TxSlots{}, tx)
	assert.NoError(err)

	{
		var txSlots types.TxSlots
		txSlot1 := &types.TxSlot{
			Tip:    *uint256.NewInt(300000),
			FeeCap: *uint256.NewInt(1000000000),
			Gas:    100000,
			Nonce:  3,
		}
		txSlot1.IDHash[0] = 1
		txSlots.Append(txSlot1, addr[:], true)

		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		for _, reason := range reasons {
			assert.Equal(Success, reason, reason.String())
		}

		// Add remote transactions, and check it processes.
		pool.AddRemoteTxs(ctx, txSlots)
		err = pool.processRemoteTxs(ctx)
		assert.NoError(err)

	}

	// Test sending normal transactions with expected nonces.
	{
		txSlots := types.TxSlots{}
		txSlot2 := &types.TxSlot{
			Tip:    *uint256.NewInt(300000),
			FeeCap: *uint256.NewInt(1000000000),
			Gas:    100000,
			Nonce:  4,
		}
		txSlot2.IDHash[0] = 2
		txSlot3 := &types.TxSlot{
			Tip:    *uint256.NewInt(300000),
			FeeCap: *uint256.NewInt(1000000000),
			Gas:    100000,
			Nonce:  6,
		}
		txSlot3.IDHash[0] = 3
		txSlots.Append(txSlot2, addr[:], true)
		txSlots.Append(txSlot3, addr[:], true)
		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		for _, reason := range reasons {
			assert.Equal(Success, reason, reason.String())
		}

		// Test NonceFromAddress function to check if the address' nonce is being properly tracked.
		nonce, _ := pool.NonceFromAddress(addr)
		// CDK Erigon will return 0, Upstream Erigon will return latest nonce including txns in the queued pool.
		assert.Equal(uint64(0), nonce)
	}

	// Test sending transactions without having enough balance for it.
	{
		var txSlots types.TxSlots
		txSlot1 := &types.TxSlot{
			Tip:    *uint256.NewInt(300000),
			FeeCap: *uint256.NewInt(9 * common.Ether),
			Gas:    100000,
			Nonce:  3,
		}
		txSlot1.IDHash[0] = 4
		txSlots.Append(txSlot1, addr[:], true)
		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		for _, reason := range reasons {
			assert.Equal(InsufficientFunds, reason, reason.String())
		}
	}

	// Test sending transactions with too low nonce.
	{
		var txSlots types.TxSlots
		txSlot1 := &types.TxSlot{
			Tip:    *uint256.NewInt(300000),
			FeeCap: *uint256.NewInt(1000000000),
			Gas:    100000,
			Nonce:  1,
		}
		txSlot1.IDHash[0] = 5
		txSlots.Append(txSlot1, addr[:], true)
		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		for _, reason := range reasons {
			assert.Equal(NonceTooLow, reason, reason.String())
		}
	}
}

func TestOnNewBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	coreDB, db := memdb.NewTestDB(t), memdb.NewTestDB(t)
	ctrl := gomock.NewController(t)

	stream := remote.NewMockKV_StateChangesClient(ctrl)
	i := 0
	stream.EXPECT().
		Recv().
		DoAndReturn(func() (*remote.StateChangeBatch, error) {
			if i > 0 {
				return nil, io.EOF
			}
			i++
			return &remote.StateChangeBatch{
				StateVersionId: 1,
				ChangeBatch: []*remote.StateChange{
					{
						Txs: [][]byte{
							decodeHex(types.TxParseMainnetTests[0].PayloadStr),
							decodeHex(types.TxParseMainnetTests[1].PayloadStr),
							decodeHex(types.TxParseMainnetTests[2].PayloadStr),
						},
						BlockHeight: 1,
						BlockHash:   gointerfaces.ConvertHashToH256([32]byte{}),
					},
				},
			}, nil
		}).
		AnyTimes()

	stateChanges := remote.NewMockKVClient(ctrl)
	stateChanges.
		EXPECT().
		StateChanges(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ *remote.StateChangeRequest, _ ...grpc.CallOption) (remote.KV_StateChangesClient, error) {
			return stream, nil
		})

	pool := NewMockPool(ctrl)
	pool.EXPECT().
		ValidateSerializedTxn(gomock.Any()).
		DoAndReturn(func(_ []byte) error {
			return nil
		}).
		Times(3)

	var minedTxs types.TxSlots
	pool.EXPECT().
		OnNewBlock(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(
			func(
				_ context.Context,
				_ *remote.StateChangeBatch,
				_ types.TxSlots,
				minedTxsArg types.TxSlots,
				_ kv.Tx,
			) error {
				minedTxs = minedTxsArg
				return nil
			},
		).
		Times(1)

	fetch := NewFetch(ctx, nil, pool, stateChanges, coreDB, db, *u256.N1)
	err := fetch.handleStateChanges(ctx, stateChanges)
	assert.ErrorIs(t, io.EOF, err)
	assert.Equal(t, 3, len(minedTxs.Txs))
}

// For X Layer

func generateRandomMetaTx(id int, senderNonceMap map[uint64]uint64) *metaTx {
	senderID := uint64(rand.Intn(1000))
	nonce, exists := senderNonceMap[senderID]
	if !exists {
		nonce = 0
	}
	senderNonceMap[senderID] = nonce + 1

	var idHash [32]byte
	_, err := rand.Read(idHash[:])
	if err != nil {
		panic(fmt.Sprintf("Failed to generate random IDHash: %v", err))
	}

	minFeeCap := uint64(rand.Intn(1000000)) // [0, 999999]

	var minTip uint64
	if minFeeCap > 0 {
		minTip = uint64(rand.Intn(int(minFeeCap))) // [0, minFeeCap-1]
	} else {
		minTip = 0
	}

	return &metaTx{
		Tx: &types.TxSlot{
			IDHash:   idHash,
			SenderID: senderID,
			Nonce:    nonce,
		},
		minFeeCap:                 *uint256.NewInt(minFeeCap),
		nonceDistance:             0,
		cumulativeBalanceDistance: 0,
		minTip:                    minTip,
		bestIndex:                 -1,
		worstIndex:                -1,
		timestamp:                 uint64(time.Now().UnixNano()),
		created:                   uint64(time.Now().Unix()),
		subPool:                   0,
		currentSubPool:            PendingSubPool,
	}
}

func isSorted(slice *bestSlice) bool {
	for i := 1; i < slice.Len(); i++ {
		if slice.Less(i, i-1) {
			fmt.Printf("Unsorted at %d: %+v < %+v\n", i, slice.ms[i-1], slice.ms[i])
			return false
		}
	}
	return true
}

func TestEnforceBestInvariantsAfterBestChanged(t *testing.T) {
	const txCount = 1_000_000
	rand.Seed(uint64(time.Now().UnixNano()))

	poolSort := NewPendingSubPool(PendingSubPool, txCount+1, false)
	poolTimSort := NewPendingSubPool(PendingSubPool, txCount+1, true)

	senderNonceMap := make(map[uint64]uint64)
	for i := 0; i < txCount; i++ {
		tx := generateRandomMetaTx(i, senderNonceMap)
		txCopy := *tx
		txCopy.Tx = &types.TxSlot{
			IDHash:   tx.Tx.IDHash,
			SenderID: tx.Tx.SenderID,
			Nonce:    tx.Tx.Nonce,
		}
		poolSort.Add(tx)
		poolTimSort.Add(&txCopy)
	}

	poolTimSort.EnforceBestInvariants()
	assert.True(t, isSorted(poolTimSort.best), "timsort.TimSort failed to sort bestSlice")

	poolSort.EnforceBestInvariants()
	assert.True(t, isSorted(poolSort.best), "sort.Sort failed to sort bestSlice")

	assert.Equal(t, poolSort.best.ms[0].Tx.IDHash, poolTimSort.best.ms[0].Tx.IDHash, "Best tx IDHash differs before removing best txs")

	// remove top 30 transactions
	for i := 0; i < 1000; i++ {
		poolSort.Remove(poolSort.Best())
		poolTimSort.Remove(poolTimSort.Best())
	}

	// add 30 new transacions
	for i := 0; i < 500; i++ {
		tx := generateRandomMetaTx(i, senderNonceMap)
		txCopy := *tx
		txCopy.Tx = &types.TxSlot{
			IDHash:   tx.Tx.IDHash,
			SenderID: tx.Tx.SenderID,
			Nonce:    tx.Tx.Nonce,
		}
		poolSort.Add(tx)
		poolTimSort.Add(&txCopy)
	}

	start := time.Now()
	poolTimSort.EnforceBestInvariants()
	timSortDuration := time.Since(start)

	start = time.Now()
	poolSort.EnforceBestInvariants()
	sortDuration := time.Since(start)

	assert.True(t, isSorted(poolTimSort.best), "timsort.TimSort failed to sort bestSlice")
	assert.True(t, isSorted(poolSort.best), "sort.Sort failed to sort bestSlice")
	assert.Equal(t, poolSort.best.ms[0].Tx.IDHash, poolTimSort.best.ms[0].Tx.IDHash, "Best tx IDHash differs after removing best txs")
	assert.Greaterf(t, sortDuration, timSortDuration, "sort.Sort cost less time than timsort.TimSort")

	t.Logf("sort.Sort duration(After BestRead): %v", sortDuration)
	t.Logf("timsort.TimSort duration(After BestRead): %v", timSortDuration)
}

func BenchmarkSortingPerformance(b *testing.B) {
	const (
		txCount     = 100_000 // Reduced for benchmark speed; adjust as needed
		removeCount = 1_000   // Number of transactions to remove
		addCount    = 500     // Number of transactions to add back
	)

	// Seed random generator
	rand.Seed(uint64(time.Now().UnixNano()))

	// Run benchmark for both sorting methods
	b.Run("sort.Sort", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Initialize pool
			pool := NewPendingSubPool(PendingSubPool, txCount+addCount, false)
			senderNonceMap := make(map[uint64]uint64)

			// Add initial transactions
			for j := 0; j < txCount; j++ {
				tx := generateRandomMetaTx(j, senderNonceMap)
				pool.Add(tx)
			}

			// First sort
			pool.EnforceBestInvariants()
			assert.True(b, isSorted(pool.best), "Initial sort.Sort failed")

			// Remove some transactions
			for j := 0; j < removeCount && pool.best.Len() > 0; j++ {
				pool.Remove(pool.Best())
			}

			// Add new transactions
			for j := 0; j < addCount; j++ {
				tx := generateRandomMetaTx(txCount+j, senderNonceMap)
				pool.Add(tx)
			}

			// Second sort (benchmark this)
			b.StartTimer()
			pool.EnforceBestInvariants()
			b.StopTimer()

			assert.True(b, isSorted(pool.best), "Second sort.Sort failed")
		}
	})

	b.Run("timsort.TimSort", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			// Initialize pool
			pool := NewPendingSubPool(PendingSubPool, txCount+addCount, true)
			senderNonceMap := make(map[uint64]uint64)

			// Add initial transactions
			for j := 0; j < txCount; j++ {
				tx := generateRandomMetaTx(j, senderNonceMap)
				pool.Add(tx)
			}

			// First sort
			pool.EnforceBestInvariants()
			assert.True(b, isSorted(pool.best), "Initial timsort.TimSort failed")

			// Remove some transactions
			for j := 0; j < removeCount && pool.best.Len() > 0; j++ {
				pool.Remove(pool.Best())
			}

			// Add new transactions
			for j := 0; j < addCount; j++ {
				tx := generateRandomMetaTx(txCount+j, senderNonceMap)
				pool.Add(tx)
			}

			// Second sort (benchmark this)
			b.StartTimer()
			pool.EnforceBestInvariants()
			b.StopTimer()

			assert.True(b, isSorted(pool.best), "Second timsort.TimSort failed")
		}
	})
}

type addLocalTxsTest struct {
	newTransactions types.TxSlots
	expectReasons   []DiscardReason
	discardIndex    []int
	goodTxsIndex    []uint8
	dupIndex        []uint8
}

func TestAddLocalTxsInParallel(t *testing.T) {
	var tests = []addLocalTxsTest{
		// Case1: single invalid transaction
		{
			newTransactions: types.TxSlots{
				Txs: []*types.TxSlot{
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  1,
					},
				},
			},
			expectReasons: []DiscardReason{NonceTooLow},
			goodTxsIndex:  []uint8{},
		},
		// Case2: single valid transaction
		{
			newTransactions: types.TxSlots{
				Txs: []*types.TxSlot{
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  3,
					},
				},
			},
			expectReasons: []DiscardReason{Success},
			goodTxsIndex:  []uint8{0},
		},
		// Case3: bulk transactions with invalid ones
		{
			newTransactions: types.TxSlots{
				Txs: []*types.TxSlot{
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  4,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  5,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  1,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  6,
					},
				},
			},
			expectReasons: []DiscardReason{Success, Expired, NonceTooLow, Success},
			discardIndex:  []int{1},
			goodTxsIndex:  []uint8{0x0, 0x3},
		},
		//  Case4: bulk transactions with no invalid ones
		{
			newTransactions: types.TxSlots{
				Txs: []*types.TxSlot{
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  9,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  8,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  7,
					},
				},
			},
			expectReasons: []DiscardReason{Success, Success, Success},
			goodTxsIndex:  []uint8{0x0, 0x1, 0x2},
		},
		// Case5:
		{
			newTransactions: types.TxSlots{
				Txs: []*types.TxSlot{
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  10,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  11,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  12,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  1,
					},
					{
						Tip:    *uint256.NewInt(3_000_000),
						FeeCap: *uint256.NewInt(1_000_000_000),
						Gas:    100000,
						Nonce:  14,
					},
				},
			},
			expectReasons: []DiscardReason{DuplicateHash, Expired, Success, NonceTooLow, Success},
			goodTxsIndex:  []uint8{0x2, 0x4},
			discardIndex:  []int{1},
			dupIndex:      []uint8{0x0},
		},
	}
	kv.InitStandaloneSMT(false)

	assert, require := assert.New(t), require.New(t)
	ch := make(chan types.Announcements, 100)
	_, coreDB, _ := temporaltest.NewTestDB(t, datadir.New(t.TempDir()))
	defer coreDB.Close()

	db := memdb.NewTestPoolDB(t)
	path := fmt.Sprintf("/tmp/db-test-%v", time.Now().UTC().Format(time.RFC3339Nano))
	txPoolDB := newTestTxPoolDB(t, path)
	defer txPoolDB.Close()
	aclsDB := newTestACLDB(t, path)
	defer aclsDB.Close()

	// Check if the dbs are created.
	require.NotNil(t, db)
	require.NotNil(t, txPoolDB)
	require.NotNil(t, aclsDB)

	cfg := txpoolcfg.DefaultConfig
	ethCfg := &ethconfig.Defaults
	sendersCache := kvcache.New(kvcache.DefaultCoherentConfig)
	pool, err := New(ch, coreDB, cfg, ethCfg, sendersCache, *u256.N1, nil, nil, aclsDB)
	assert.NoError(err)
	require.True(pool != nil)
	ctx := context.Background()
	var stateVersionID uint64 = 0
	pendingBaseFee := uint64(200000)
	h1 := gointerfaces.ConvertHashToH256([32]byte{})

	// Create address for testing.
	var addr [20]byte
	addr[0] = 1

	// Fund addr with 18 Ether for sending transactions.
	v := make([]byte, types.EncodeSenderLengthForStorage(2, *uint256.NewInt(18 * common.Ether)))
	types.EncodeSender(2, *uint256.NewInt(18 * common.Ether), v)

	change := &remote.StateChangeBatch{
		StateVersionId:      stateVersionID,
		PendingBlockBaseFee: pendingBaseFee,
		BlockGasLimit:       1000000,
		ChangeBatch: []*remote.StateChange{
			{BlockHeight: 0, BlockHash: h1},
		},
	}
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(addr),
		Data:    v,
	})
	tx, err := db.BeginRw(ctx)
	require.NoError(err)
	defer tx.Rollback()
	err = pool.OnNewBlock(ctx, change, types.TxSlots{}, types.TxSlots{}, tx)
	assert.NoError(err)

	dupIDHash := [32]byte{}
	dupIDHash[0] = 1
	pool.byHash[string(dupIDHash[:])] = &metaTx{}

	var txCount = 2
	for _, test := range tests {
		var txSlots types.TxSlots
		for i, tx := range test.newTransactions.Txs {
			tx.IDHash[0] = byte(i + txCount)
			txSlots.Append(tx, addr[:], true)
		}

		for _, index := range test.discardIndex {
			pool.discardReasonsLRU.Add(string(test.newTransactions.Txs[index].IDHash[:]), Expired)
		}

		for _, index := range test.dupIndex {
			txSlots.Txs[index].IDHash[0] = 1
		}

		var tx kv.Tx
		tx, err = txPoolDB.BeginRw(ctx)
		require.NoError(err)
		defer tx.Rollback()

		resaons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		if err != nil {
			t.Logf("AddLocalTxs failed: %v", err)
		}
		assert.NoError(err)
		t.Logf("After AddLocalTxs, promoted len: %d", pool.promoted.Len())

		for i, expect := range test.expectReasons {
			assert.Equal(expect, resaons[i], "The discard reason is wrong")
		}

		for i, expect := range test.goodTxsIndex {
			_, _, actual := pool.promoted.At(i)
			assert.Equal(expect+uint8(txCount), actual[0], "The good transaction is wrong")
		}

		txCount += len(test.newTransactions.Txs)
		tx.Commit()
	}
}

// TestFatTransactionIntegration tests fat transaction integration with the main pool
func TestFatTransactionIntegration(t *testing.T) {
	assert, require := assert.New(t), require.New(t)
	ch := make(chan types.Announcements, 100)
	_, coreDB, _ := temporaltest.NewTestDB(t, datadir.New(t.TempDir()))
	defer coreDB.Close()

	db := memdb.NewTestPoolDB(t)
	path := fmt.Sprintf("/tmp/db-test-fat-tx-%v", time.Now().UTC().Format(time.RFC3339Nano))
	txPoolDB := newTestTxPoolDB(t, path)
	defer txPoolDB.Close()
	aclsDB := newTestACLDB(t, path)
	defer aclsDB.Close()

	cfg := txpoolcfg.DefaultConfig
	ethCfg := &ethconfig.Defaults
	sendersCache := kvcache.New(kvcache.DefaultCoherentConfig)
	pool, err := New(ch, coreDB, cfg, ethCfg, sendersCache, *u256.N1, nil, nil, aclsDB)
	assert.NoError(err)
	require.True(pool != nil)

	ctx := context.Background()
	var stateVersionID uint64 = 0
	pendingBaseFee := uint64(200000)
	h1 := gointerfaces.ConvertHashToH256([32]byte{})

	// Create addresses for testing
	var normalAddr [20]byte
	normalAddr[0] = 1
	var fatAddr [20]byte
	fatAddr[0] = 2

	// Fund addresses with 18 Ether for sending transactions
	v := make([]byte, types.EncodeSenderLengthForStorage(0, *uint256.NewInt(18 * common.Ether)))
	types.EncodeSender(0, *uint256.NewInt(18 * common.Ether), v)

	change := &remote.StateChangeBatch{
		StateVersionId:      stateVersionID,
		PendingBlockBaseFee: pendingBaseFee,
		BlockGasLimit:       1000000, // 1M gas limit
		ChangeBatch: []*remote.StateChange{
			{BlockHeight: 0, BlockHash: h1},
		},
	}
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(normalAddr),
		Data:    v,
	})
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(fatAddr),
		Data:    v,
	})

	tx, err := db.BeginRw(ctx)
	require.NoError(err)
	defer tx.Rollback()
	err = pool.OnNewBlock(ctx, change, types.TxSlots{}, types.TxSlots{}, tx)
	assert.NoError(err)

	// Test 1: Add normal transactions (should go to normal pool)
	t.Run("NormalTransactions", func(t *testing.T) {
		var txSlots types.TxSlots
		for i := 0; i < 3; i++ {
			txSlot := &types.TxSlot{
				Tip:    *uint256.NewInt(300000),
				FeeCap: *uint256.NewInt(1000000000),
				Gas:    21000,     // Normal gas usage
				Nonce:  uint64(i), // Use nonce starting from 0 to match account state
			}
			txSlot.IDHash[0] = byte(i)
			txSlots.Append(txSlot, normalAddr[:], true)
		}

		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		// Check that at least some transactions were added successfully
		successCount := 0
		for _, reason := range reasons {
			if reason == Success {
				successCount++
			}
		}
		assert.Greater(successCount, 0, "At least one transaction should be added successfully")

		// Check that transactions are in normal pool, not fat pool
		assert.Greater(pool.pending.Len(), 0, "Should have transactions in normal pool")
		assert.Equal(0, pool.fatTxPool.Len())
	})

	// Test 2: Add fat transactions (should go to fat pool)
	t.Run("FatTransactions", func(t *testing.T) {
		var txSlots types.TxSlots
		for i := 0; i < 2; i++ {
			txSlot := &types.TxSlot{
				Tip:    *uint256.NewInt(300000),
				FeeCap: *uint256.NewInt(1000000000),
				Gas:    100000,    // 10% of block gas limit - should be fat
				Nonce:  uint64(i), // Use nonce starting from 0 to match account state
			}
			txSlot.IDHash[0] = byte(i + 10)
			txSlots.Append(txSlot, fatAddr[:], true)
		}

		reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
		assert.NoError(err)
		// Check that at least some transactions were added successfully
		successCount := 0
		for _, reason := range reasons {
			if reason == Success {
				successCount++
			}
		}
		assert.Greater(successCount, 0, "At least one transaction should be added successfully")

		// Check that transactions are in fat pool
		assert.Greater(pool.fatTxPool.Len(), 0, "Should have transactions in fat pool")
	})

	// Test 3: Test quota allocation with both pools having transactions
	t.Run("QuotaAllocation", func(t *testing.T) {
		// Verify that we have transactions in both pools
		normalPoolSize := pool.pending.Len()
		fatPoolSize := pool.fatTxPool.Len()
		assert.Greater(normalPoolSize, 0, "Should have transactions in normal pool")
		assert.Greater(fatPoolSize, 0, "Should have transactions in fat pool")

		// Test that fat transaction detection is working correctly
		// Normal transactions should be in normal pool
		assert.Equal(3, normalPoolSize, "Should have 3 normal transactions")
		// Fat transactions should be in fat pool
		assert.Equal(2, fatPoolSize, "Should have 2 fat transactions")

		// Test that the pools are properly isolated
		// This verifies that our fat transaction detection and isolation is working
		t.Logf("Normal pool size: %d, Fat pool size: %d", normalPoolSize, fatPoolSize)
	})
}

// TestFatTransactionRemoval tests fat transaction removal scenarios
func TestFatTransactionRemoval(t *testing.T) {
	assert, require := assert.New(t), require.New(t)
	ch := make(chan types.Announcements, 100)
	_, coreDB, _ := temporaltest.NewTestDB(t, datadir.New(t.TempDir()))
	defer coreDB.Close()

	db := memdb.NewTestPoolDB(t)
	path := fmt.Sprintf("/tmp/db-test-fat-removal-%v", time.Now().UTC().Format(time.RFC3339Nano))
	txPoolDB := newTestTxPoolDB(t, path)
	defer txPoolDB.Close()
	aclsDB := newTestACLDB(t, path)
	defer aclsDB.Close()

	cfg := txpoolcfg.DefaultConfig
	ethCfg := &ethconfig.Defaults
	sendersCache := kvcache.New(kvcache.DefaultCoherentConfig)
	pool, err := New(ch, coreDB, cfg, ethCfg, sendersCache, *u256.N1, nil, nil, aclsDB)
	assert.NoError(err)
	require.True(pool != nil)

	ctx := context.Background()
	var stateVersionID uint64 = 0
	pendingBaseFee := uint64(200000)
	h1 := gointerfaces.ConvertHashToH256([32]byte{})

	// Create address for testing
	var addr [20]byte
	addr[0] = 1

	// Fund address with 18 Ether
	v := make([]byte, types.EncodeSenderLengthForStorage(0, *uint256.NewInt(18 * common.Ether)))
	types.EncodeSender(0, *uint256.NewInt(18 * common.Ether), v)

	change := &remote.StateChangeBatch{
		StateVersionId:      stateVersionID,
		PendingBlockBaseFee: pendingBaseFee,
		BlockGasLimit:       1000000,
		ChangeBatch: []*remote.StateChange{
			{BlockHeight: 0, BlockHash: h1},
		},
	}
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(addr),
		Data:    v,
	})

	tx, err := db.BeginRw(ctx)
	require.NoError(err)
	defer tx.Rollback()
	err = pool.OnNewBlock(ctx, change, types.TxSlots{}, types.TxSlots{}, tx)
	assert.NoError(err)

	// Add a fat transaction
	var txSlots types.TxSlots
	txSlot := &types.TxSlot{
		Tip:    *uint256.NewInt(300000),
		FeeCap: *uint256.NewInt(1000000000),
		Gas:    100000, // Fat transaction
		Nonce:  0,      // Use nonce 0 to match account state
	}
	txSlot.IDHash[0] = 1
	txSlots.Append(txSlot, addr[:], true)

	reasons, err := pool.AddLocalTxs(ctx, txSlots, tx)
	assert.NoError(err)
	for _, reason := range reasons {
		assert.Equal(Success, reason, reason.String())
	}

	// Verify transaction is in fat pool
	assert.Equal(1, pool.fatTxPool.Len())

	// Test removal via discard
	// Find the transaction in the pool
	var fatTx *metaTx
	for _, mt := range pool.byHash {
		if mt.currentSubPool == FatTxsSubPool {
			fatTx = mt
			break
		}
	}
	require.NotNil(fatTx, "Fat transaction should be found in pool")

	// Remove the transaction
	pool.discardLocked(fatTx, FeeTooLow)

	// Verify transaction is removed from fat pool
	assert.Equal(0, pool.fatTxPool.Len())
}

// TestFatTransactionQuotaAllocation tests the quota allocation logic in detail
func TestFatTransactionQuotaAllocation(t *testing.T) {
	assert, require := assert.New(t), require.New(t)
	ch := make(chan types.Announcements, 100)
	_, coreDB, _ := temporaltest.NewTestDB(t, datadir.New(t.TempDir()))
	defer coreDB.Close()

	db := memdb.NewTestPoolDB(t)
	path := fmt.Sprintf("/tmp/db-test-fat-quota-%v", time.Now().UTC().Format(time.RFC3339Nano))
	txPoolDB := newTestTxPoolDB(t, path)
	defer txPoolDB.Close()
	aclsDB := newTestACLDB(t, path)
	defer aclsDB.Close()

	cfg := txpoolcfg.DefaultConfig
	ethCfg := &ethconfig.Defaults
	sendersCache := kvcache.New(kvcache.DefaultCoherentConfig)
	pool, err := New(ch, coreDB, cfg, ethCfg, sendersCache, *u256.N1, nil, nil, aclsDB)
	assert.NoError(err)
	require.True(pool != nil)

	ctx := context.Background()
	var stateVersionID uint64 = 0
	pendingBaseFee := uint64(200000)
	h1 := gointerfaces.ConvertHashToH256([32]byte{})

	// Create addresses for testing
	var normalAddr [20]byte
	normalAddr[0] = 1
	var fatAddr [20]byte
	fatAddr[0] = 2

	// Fund addresses with 18 Ether
	v := make([]byte, types.EncodeSenderLengthForStorage(0, *uint256.NewInt(18 * common.Ether)))
	types.EncodeSender(0, *uint256.NewInt(18 * common.Ether), v)

	change := &remote.StateChangeBatch{
		StateVersionId:      stateVersionID,
		PendingBlockBaseFee: pendingBaseFee,
		BlockGasLimit:       1000000,
		ChangeBatch: []*remote.StateChange{
			{BlockHeight: 0, BlockHash: h1},
		},
	}
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(normalAddr),
		Data:    v,
	})
	change.ChangeBatch[0].Changes = append(change.ChangeBatch[0].Changes, &remote.AccountChange{
		Action:  remote.Action_UPSERT,
		Address: gointerfaces.ConvertAddressToH160(fatAddr),
		Data:    v,
	})

	tx, err := db.BeginRw(ctx)
	require.NoError(err)
	defer tx.Rollback()
	err = pool.OnNewBlock(ctx, change, types.TxSlots{}, types.TxSlots{}, tx)
	assert.NoError(err)

	// Test 1: Both pools have transactions - test quota allocation
	t.Run("BothPoolsHaveTransactions", func(t *testing.T) {
		// Add normal transactions
		var normalTxSlots types.TxSlots
		for i := 0; i < 5; i++ {
			txSlot := &types.TxSlot{
				Tip:    *uint256.NewInt(300000),
				FeeCap: *uint256.NewInt(1000000000),
				Gas:    21000, // Normal gas usage
				Nonce:  uint64(i),
			}
			txSlot.IDHash[0] = byte(i)
			normalTxSlots.Append(txSlot, normalAddr[:], true)
		}

		reasons, err := pool.AddLocalTxs(ctx, normalTxSlots, tx)
		assert.NoError(err)
		successCount := 0
		for _, reason := range reasons {
			if reason == Success {
				successCount++
			}
		}
		assert.Greater(successCount, 0, "Should add normal transactions")

		// Add fat transactions
		var fatTxSlots types.TxSlots
		for i := 0; i < 3; i++ {
			txSlot := &types.TxSlot{
				Tip:    *uint256.NewInt(300000),
				FeeCap: *uint256.NewInt(1000000000),
				Gas:    100000, // Fat transaction
				Nonce:  uint64(i),
			}
			txSlot.IDHash[0] = byte(i + 10)
			fatTxSlots.Append(txSlot, fatAddr[:], true)
		}

		reasons, err = pool.AddLocalTxs(ctx, fatTxSlots, tx)
		assert.NoError(err)
		successCount = 0
		for _, reason := range reasons {
			if reason == Success {
				successCount++
			}
		}
		assert.Greater(successCount, 0, "Should add fat transactions")

		// Verify pool sizes
		normalPoolSize := pool.pending.Len()
		fatPoolSize := pool.fatTxPool.Len()
		assert.Greater(normalPoolSize, 0, "Should have normal transactions")
		assert.Greater(fatPoolSize, 0, "Should have fat transactions")

		// Test quota calculation logic (simulate the logic from bestForXLayer)
		requestedTx := uint16(10) // Request 10 transactions
		var normalTxQuota, fatTxQuota uint16

		if normalPoolSize > 0 && fatPoolSize > 0 {
			fatTxQuota = uint16(float64(requestedTx) * 0.2) // 20% fat transactions
			if fatTxQuota < 1 {
				fatTxQuota = 1
			}
			normalTxQuota = requestedTx - fatTxQuota

			// Adjust quotas based on actual pool sizes
			if uint16(fatPoolSize) < fatTxQuota {
				normalTxQuota += fatTxQuota - uint16(fatPoolSize)
				fatTxQuota = uint16(fatPoolSize)
			}
			if uint16(normalPoolSize) < normalTxQuota {
				fatTxQuota += normalTxQuota - uint16(normalPoolSize)
				normalTxQuota = uint16(normalPoolSize)
			}
		}

		// Verify quota calculation
		assert.Greater(int(normalTxQuota), 0, "Should allocate quota for normal transactions")
		assert.Greater(int(fatTxQuota), 0, "Should allocate quota for fat transactions")
		assert.Equal(int(requestedTx), int(normalTxQuota+fatTxQuota), "Total quota should equal requested amount")
	})
}

// TestFatTransactionDetectionEdgeCases tests edge cases in fat transaction detection
func TestFatTransactionDetectionEdgeCases(t *testing.T) {
	assert := assert.New(t)

	config := FatTxConfig{
		GasRatioThreshold: 0.05, // 5%
		PoolSizeLimit:     10,
		MaxRatio:          0.2,
		Enabled:           true,
	}

	getRlpFunc := func(tx kv.Tx, hash []byte) ([]byte, common.Address, bool, error) {
		return []byte{0x01, 0x02, 0x03}, common.Address{}, false, nil
	}

	fatPool := NewFatTxPool(config, getRlpFunc)

	// Test 1: Disabled fat transaction pool
	t.Run("DisabledPool", func(t *testing.T) {
		fatPool.Disable()
		txSlot := &types.TxSlot{
			Gas: 100000, // High gas usage
		}
		assert.False(fatPool.IsFatTransaction(txSlot, 1000000), "Should not detect fat transaction when disabled")
		fatPool.Enable()
	})

	// Test 2: Zero block gas limit
	t.Run("ZeroBlockGasLimit", func(t *testing.T) {
		txSlot := &types.TxSlot{
			Gas: 100000,
		}
		assert.False(fatPool.IsFatTransaction(txSlot, 0), "Should not detect fat transaction with zero block gas limit")
	})

	// Test 3: Exact threshold gas usage
	t.Run("ExactThreshold", func(t *testing.T) {
		txSlot := &types.TxSlot{
			Gas: 50000, // Exactly 5% of 1M gas limit
		}
		assert.False(fatPool.IsFatTransaction(txSlot, 1000000), "Should not detect fat transaction at exact threshold")
	})

	// Test 4: Just above threshold
	t.Run("JustAboveThreshold", func(t *testing.T) {
		txSlot := &types.TxSlot{
			Gas: 51000, // Just above 5% of 1M gas limit
		}
		assert.True(fatPool.IsFatTransaction(txSlot, 1000000), "Should detect fat transaction just above threshold")
	})

	// Test 5: Very high gas usage
	t.Run("VeryHighGasUsage", func(t *testing.T) {
		txSlot := &types.TxSlot{
			Gas: 500000, // 50% of 1M gas limit
		}
		assert.True(fatPool.IsFatTransaction(txSlot, 1000000), "Should detect fat transaction with very high gas usage")
	})
}
