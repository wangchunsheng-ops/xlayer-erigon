package txpool

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ledgerwatch/erigon-lib/common"
	"github.com/ledgerwatch/erigon-lib/common/fixedgas"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/types"
	"github.com/ledgerwatch/log/v3"
)

// Fat transaction detection configuration
const (
	DefaultFatTxGasRatio  = 0.05  // Default fat transaction gas ratio threshold (5%)
	DefaultFatTxPoolLimit = 10000 // Default fat transaction pool size limit
	DefaultFatTxMaxRatio  = 0.1   // Default fat transaction max ratio (10%)
)

// FatTxPool manages fat transactions separately from normal transactions
type FatTxPool struct {
	pool   *SubPool
	config FatTxConfig
	stats  FatTxStats
	mu     sync.RWMutex
	// Function to get RLP data - provided by the main pool
	getRlpFunc func(kv.Tx, []byte) ([]byte, common.Address, bool, error)
}

// FatTxConfig contains configuration for fat transaction pool
type FatTxConfig struct {
	GasRatioThreshold float64
	PoolSizeLimit     int
	MaxRatio          float64
	Enabled           bool
}

// FatTxStats contains statistics for fat transaction pool
type FatTxStats struct {
	TotalFatTxs     int64
	ProcessedFatTxs int64
	RejectedFatTxs  int64
	LastUpdated     time.Time
}

// NewFatTxPool creates a new fat transaction pool
func NewFatTxPool(config FatTxConfig, getRlpFunc func(kv.Tx, []byte) ([]byte, common.Address, bool, error)) *FatTxPool {
	if config.GasRatioThreshold == 0 {
		config.GasRatioThreshold = DefaultFatTxGasRatio
	}
	if config.PoolSizeLimit == 0 {
		config.PoolSizeLimit = DefaultFatTxPoolLimit
	}
	if config.MaxRatio == 0 {
		config.MaxRatio = DefaultFatTxMaxRatio
	}

	return &FatTxPool{
		pool:       NewSubPool(FatTxsSubPool, config.PoolSizeLimit),
		config:     config,
		stats:      FatTxStats{LastUpdated: time.Now()},
		getRlpFunc: getRlpFunc,
	}
}

// IsFatTransaction checks if a transaction is a fat transaction
func (ftp *FatTxPool) IsFatTransaction(tx *types.TxSlot, blockGasLimit uint64) bool {
	if !ftp.config.Enabled || blockGasLimit == 0 {
		return false
	}

	gasRatio := float64(tx.Gas) / float64(blockGasLimit)
	return gasRatio > ftp.config.GasRatioThreshold
}

// AddTransaction adds a transaction to the fat transaction pool
func (ftp *FatTxPool) AddTransaction(mt *metaTx) bool {
	ftp.mu.Lock()
	defer ftp.mu.Unlock()

	if ftp.pool.Len() >= ftp.config.PoolSizeLimit {
		log.Debug("Fat transaction pool is full", "limit", ftp.config.PoolSizeLimit)
		atomic.AddInt64(&ftp.stats.RejectedFatTxs, 1)
		return false
	}

	ftp.pool.Add(mt)
	atomic.AddInt64(&ftp.stats.TotalFatTxs, 1)
	ftp.stats.LastUpdated = time.Now()

	log.Trace("Added transaction to fat pool",
		"txHash", fmt.Sprintf("%x", mt.Tx.IDHash),
		"gas", mt.Tx.Gas,
		"poolSize", ftp.pool.Len())

	return true
}

// GetTransactions retrieves transactions from fat transaction pool
func (ftp *FatTxPool) GetTransactions(n uint16, availableGas uint64) []*metaTx {
	ftp.mu.RLock()
	defer ftp.mu.RUnlock()

	if !ftp.config.Enabled || ftp.pool.Len() == 0 {
		return nil
	}

	var result []*metaTx
	best := ftp.pool.best

	for i := 0; i < len(best.ms) && len(result) < int(n); i++ {
		mt := best.ms[i]

		// Check if we have enough gas
		if mt.Tx.Gas > availableGas {
			continue
		}

		result = append(result, mt)
		availableGas -= mt.Tx.Gas
	}

	atomic.AddInt64(&ftp.stats.ProcessedFatTxs, int64(len(result)))
	return result
}

// ReadTransactionsForContext reads transactions from fat pool and fills the read context
func (ftp *FatTxPool) ReadTransactionsForContext(n uint16, readContext *ReadContext, tx kv.Tx, isShanghai, isLondon bool) (bool, error) {
	ftp.mu.RLock()
	defer ftp.mu.RUnlock()

	if !ftp.config.Enabled || ftp.pool.Len() == 0 {
		return true, nil
	}

	best := ftp.pool.best

	for i := 0; readContext.count < int(n) && i < len(best.ms); i++ {
		// if we wouldn't have enough gas for a standard transaction then quit out early
		if readContext.availableGas < fixedgas.TxGas {
			break
		}

		mt := best.ms[i]

		if readContext.toSkip.Contains(mt.Tx.IDHash) {
			continue
		}

		if !isLondon && mt.Tx.Type == 0x2 {
			// remove ldn txs when not in london
			readContext.toRemove = append(readContext.toRemove, mt)
			readContext.toSkip.Add(mt.Tx.IDHash)
			continue
		}

		if mt.Tx.Gas > transactionGasLimit {
			// Skip transactions with very large gas limit, these shouldn't enter the pool at all
			continue
		}

		// Get RLP data for the transaction
		rlpTx, sender, isLocal, err := ftp.getRlpFunc(tx, mt.Tx.IDHash[:])
		if err != nil {
			return false, err
		}
		if len(rlpTx) == 0 {
			readContext.toRemove = append(readContext.toRemove, mt)
			continue
		}

		// Skip transactions that require more blob gas than is available
		blobCount := uint64(len(mt.Tx.BlobHashes))
		if blobCount*fixedgas.BlobGasPerBlob > readContext.availableBlobGas {
			continue
		}
		readContext.availableBlobGas -= blobCount * fixedgas.BlobGasPerBlob

		// make sure we have enough gas in the caller to add this transaction.
		intrinsicGas, _ := CalcIntrinsicGas(uint64(mt.Tx.DataLen), uint64(mt.Tx.DataNonZeroLen), nil, mt.Tx.Creation, true, true, isShanghai)
		if intrinsicGas > readContext.availableGas {
			continue
		}

		if intrinsicGas <= readContext.availableGas { // check for potential underflow
			readContext.availableGas -= intrinsicGas
		}

		readContext.txs.Txs[readContext.count] = rlpTx
		readContext.txs.TxIds[readContext.count] = mt.Tx.IDHash
		copy(readContext.txs.Senders.At(readContext.count), sender.Bytes())
		readContext.txs.IsLocal[readContext.count] = isLocal
		readContext.toSkip.Add(mt.Tx.IDHash)
		readContext.count++

		atomic.AddInt64(&ftp.stats.ProcessedFatTxs, 1)
	}

	return true, nil
}

// RemoveTransaction removes a transaction from the fat transaction pool
func (ftp *FatTxPool) RemoveTransaction(mt *metaTx) {
	ftp.mu.Lock()
	defer ftp.mu.Unlock()

	ftp.pool.Remove(mt)
}

// GetStats returns current statistics
func (ftp *FatTxPool) GetStats() FatTxStats {
	ftp.mu.RLock()
	defer ftp.mu.RUnlock()

	stats := ftp.stats
	stats.TotalFatTxs = atomic.LoadInt64(&ftp.stats.TotalFatTxs)
	stats.ProcessedFatTxs = atomic.LoadInt64(&ftp.stats.ProcessedFatTxs)
	stats.RejectedFatTxs = atomic.LoadInt64(&ftp.stats.RejectedFatTxs)

	return stats
}

// Len returns the number of transactions in the pool
func (ftp *FatTxPool) Len() int {
	ftp.mu.RLock()
	defer ftp.mu.RUnlock()
	return ftp.pool.Len()
}

// UpdateConfig updates the configuration
func (ftp *FatTxPool) UpdateConfig(config FatTxConfig) {
	ftp.mu.Lock()
	defer ftp.mu.Unlock()

	ftp.config = config
	log.Info("Fat transaction pool config updated",
		"gasRatioThreshold", config.GasRatioThreshold,
		"poolSizeLimit", config.PoolSizeLimit,
		"maxRatio", config.MaxRatio,
		"enabled", config.Enabled)
}

// Enable enables the fat transaction pool
func (ftp *FatTxPool) Enable() {
	ftp.mu.Lock()
	defer ftp.mu.Unlock()
	ftp.config.Enabled = true
	log.Info("Fat transaction pool enabled")
}

// Disable disables the fat transaction pool
func (ftp *FatTxPool) Disable() {
	ftp.mu.Lock()
	defer ftp.mu.Unlock()
	ftp.config.Enabled = false
	log.Info("Fat transaction pool disabled")
}

// IsEnabled returns whether the fat transaction pool is enabled
func (ftp *FatTxPool) IsEnabled() bool {
	ftp.mu.RLock()
	defer ftp.mu.RUnlock()
	return ftp.config.Enabled
}

// GetPool returns the underlying sub pool
func (ftp *FatTxPool) GetPool() *SubPool {
	return ftp.pool
}
