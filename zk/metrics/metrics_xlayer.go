package metrics

import (
	"fmt"
	"time"

	"github.com/ledgerwatch/log/v3"
	"github.com/prometheus/client_golang/prometheus"
)

type BatchFinalizeType string

const (
	BatchTimeOut         BatchFinalizeType = "EmptyBatchTimeOut"
	BatchCounterOverflow BatchFinalizeType = "BatchCounterOverflow"
	BatchLimboRecovery   BatchFinalizeType = "LimboRecovery"
)

var (
	SeqPrefix                     = "sequencer_"
	SeqBlockNumberName            = SeqPrefix + "block_number"
	SeqBlockExecuteTimingName     = SeqPrefix + "block_execute_timing"
	SeqBlockProcessTxTimingName   = SeqPrefix + "block_process_tx_timing"
	SeqBlockGetTxTimingName       = SeqPrefix + "block_get_tx_timing"
	SeqBlockGetTxPauseTimingName  = SeqPrefix + "block_get_tx_pause_timing"
	SeqBlockInvalidTxCountName    = SeqPrefix + "block_invalid_tx_count"
	SeqBlockSetSmtCacheTimingName = SeqPrefix + "block_set_smt_cache_timing"

	SeqPoolTxCountName  = SeqPrefix + "pool_tx_count"
	SeqTxDurationName   = SeqPrefix + "tx_duration"
	SeqTxCountName      = SeqPrefix + "tx_count"
	SeqBlockGasUsedName = SeqPrefix + "block_gas_used"

	SeqBatchNumberName                        = SeqPrefix + "batch_number"
	SeqBatchExecuteTimingName                 = SeqPrefix + "batch_execute_timing"
	SeqBatchDurationName                      = SeqPrefix + "batch_duration"
	SeqSequencingBatchTimingName              = SeqPrefix + "sequencing_batch_timing"
	SeqBatchProcessTxTimingName               = SeqPrefix + "process_tx_timing"
	SeqBatchGetTxTimingName                   = SeqPrefix + "get_tx_timing"
	SeqBatchGetTxPauseTimingName              = SeqPrefix + "get_tx_pause_timing"
	SeqBatchPbStateTimingName                 = SeqPrefix + "pb_state_timing"
	SeqBatchZkIncIntermediateHashesTimingName = SeqPrefix + "zk_inc_intermediate_hashes_timing"
	SeqBatchFinaliseBlockWriteTimingName      = SeqPrefix + "finalise_block_write_timing"
	SeqBatchSmtBatchCommitDBTimingName        = SeqPrefix + "smt_batch_commit_db_timing"
	SeqBatchCommitDBTimingName                = SeqPrefix + "batch_commit_db_timing"
	SeqBatchSetSmtCacheTimingName             = SeqPrefix + "batch_set_smt_cache_timing"

	RpcPrefix              = "rpc_"
	RpcDynamicGasPriceName = RpcPrefix + "dynamic_gas_price"
	RpcInnerTxExecutedName = RpcPrefix + "inner_tx_executed"
)

func Init() {
	prometheus.MustRegister(BatchExecuteTimingGauge)
	prometheus.MustRegister(PoolTxCount)
	prometheus.MustRegister(SeqTxDuration)
	prometheus.MustRegister(SeqTxCount)
	prometheus.MustRegister(SeqBlockGasUsed)
	prometheus.MustRegister(SeqBatchNumber)

	// Register block metrics
	prometheus.MustRegister(SeqBlockNumber)
	prometheus.MustRegister(SeqBlockExecuteTiming)
	prometheus.MustRegister(SeqBlockProcessTxTiming)
	prometheus.MustRegister(SeqBlockGetTxTiming)
	prometheus.MustRegister(SeqBlockGetTxPauseTiming)
	prometheus.MustRegister(SeqBlockInvalidTxCount)
	prometheus.MustRegister(SeqBlockSetSmtCacheTiming)
	prometheus.MustRegister(RpcDynamicGasPrice)
	prometheus.MustRegister(RpcInnerTxExecuted)

	// Register new batch timing metrics
	prometheus.MustRegister(SeqBatchDuration)
	prometheus.MustRegister(SeqSequencingBatchTiming)
	prometheus.MustRegister(SeqBatchProcessTxTiming)
	prometheus.MustRegister(SeqBatchGetTxTiming)
	prometheus.MustRegister(SeqBatchGetTxPauseTiming)
	prometheus.MustRegister(SeqBatchPbStateTiming)
	prometheus.MustRegister(SeqBatchZkIncIntermediateHashesTiming)
	prometheus.MustRegister(SeqBatchFinaliseBlockWriteTiming)
	prometheus.MustRegister(SeqBatchSmtBatchCommitDBTiming)
	prometheus.MustRegister(SeqBatchCommitDBTiming)
	prometheus.MustRegister(SeqBatchSetSmtCacheTiming)
}

var BatchExecuteTimingGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: SeqBatchExecuteTimingName,
		Help: "[SEQUENCER] batch execution timing in millisecond (ms)",
	},
	[]string{"closingReason"},
)

var PoolTxCount = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: SeqPoolTxCountName,
		Help: "[SEQUENCER] tx count of each pool in tx pool",
	},
	[]string{"poolName"},
)

func BatchExecuteTiming(closingReason string, duration time.Duration) {
	log.Info(fmt.Sprintf("[BatchExecuteTiming] ClosingReason: %v, Duration: %dms", closingReason, duration.Milliseconds()))
	BatchExecuteTimingGauge.WithLabelValues(closingReason).Set(float64(duration.Milliseconds()))
}

func AddPoolTxCount(pending, baseFee, queued int) {
	log.Info(fmt.Sprintf("[PoolTxCount] pending: %v, basefee: %v, queued: %v", pending, baseFee, queued))
	PoolTxCount.WithLabelValues("pending").Set(float64(pending))
	PoolTxCount.WithLabelValues("basefee").Set(float64(baseFee))
	PoolTxCount.WithLabelValues("queued").Set(float64(queued))
}

var RpcDynamicGasPrice = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: RpcDynamicGasPriceName,
		Help: "[RPC] dynamic gas price",
	},
)

var RpcInnerTxExecuted = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: RpcInnerTxExecutedName,
		Help: "[RPC] inner tx executed, used to trace contract calls in blockchain explorer",
	},
)

var SeqTxDuration = prometheus.NewSummary(
	prometheus.SummaryOpts{
		Name: SeqTxDurationName,
		Help: "[SEQUENCER] tx processing duration in millisecond (ms)",
		Objectives: map[float64]float64{
			0.5:  0.05,  // 50th percentile (median) with 5% error
			0.9:  0.01,  // 90th percentile with 1% error
			0.95: 0.005, // 95th percentile with 0.5% error
			0.99: 0.001, // 99th percentile with 0.1% error
		},
	},
)

var SeqTxCount = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: SeqTxCountName,
		Help: "[SEQUENCER] total processed tx count",
	},
)

var SeqBlockGasUsed = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockGasUsedName,
		Help: "[SEQUENCER] gas used per block",
	},
)

var SeqBatchNumber = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchNumberName,
		Help: "[SEQUENCER] latest batch number",
	},
)

// Block metrics
var SeqBlockNumber = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockNumberName,
		Help: "[SEQUENCER] latest block number",
	},
)

var SeqBlockExecuteTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockExecuteTimingName,
		Help: "[SEQUENCER] block execution timing in milliseconds",
	},
)

var SeqBlockProcessTxTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockProcessTxTimingName,
		Help: "[SEQUENCER] block process transaction timing in milliseconds",
	},
)

var SeqBlockGetTxTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockGetTxTimingName,
		Help: "[SEQUENCER] block get transaction timing in milliseconds",
	},
)

var SeqBlockGetTxPauseTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockGetTxPauseTimingName,
		Help: "[SEQUENCER] block get transaction pause timing in milliseconds",
	},
)

var SeqBlockInvalidTxCount = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockInvalidTxCountName,
		Help: "[SEQUENCER] number of invalid transactions in block",
	},
)

var SeqBlockSetSmtCacheTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBlockSetSmtCacheTimingName,
		Help: "[SEQUENCER] block set SMT cache timing in milliseconds",
	},
)

// Batch timing metrics
var SeqBatchDuration = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchDurationName,
		Help: "[SEQUENCER] total batch duration in milliseconds",
	},
)

var SeqSequencingBatchTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqSequencingBatchTimingName,
		Help: "[SEQUENCER] sequencing batch timing in milliseconds",
	},
)

var SeqBatchProcessTxTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchProcessTxTimingName,
		Help: "[SEQUENCER] process transaction timing in milliseconds",
	},
)

var SeqBatchGetTxTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchGetTxTimingName,
		Help: "[SEQUENCER] get transaction timing in milliseconds",
	},
)

var SeqBatchGetTxPauseTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchGetTxPauseTimingName,
		Help: "[SEQUENCER] get transaction pause timing in milliseconds",
	},
)

var SeqBatchPbStateTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchPbStateTimingName,
		Help: "[SEQUENCER] pb state timing in milliseconds",
	},
)

var SeqBatchZkIncIntermediateHashesTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchZkIncIntermediateHashesTimingName,
		Help: "[SEQUENCER] zk increment intermediate hashes timing in milliseconds",
	},
)

var SeqBatchFinaliseBlockWriteTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchFinaliseBlockWriteTimingName,
		Help: "[SEQUENCER] finalise block write timing in milliseconds",
	},
)

var SeqBatchSmtBatchCommitDBTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchSmtBatchCommitDBTimingName,
		Help: "[SEQUENCER] smt batch commit DB timing in milliseconds",
	},
)

var SeqBatchCommitDBTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchCommitDBTimingName,
		Help: "[SEQUENCER] batch commit DB timing in milliseconds",
	},
)

var SeqBatchSetSmtCacheTiming = prometheus.NewGauge(
	prometheus.GaugeOpts{
		Name: SeqBatchSetSmtCacheTimingName,
		Help: "[SEQUENCER] batch set SMT cache timing in milliseconds",
	},
)
