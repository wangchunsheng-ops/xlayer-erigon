package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

type BatchFinalizeType string

const (
	BatchTimeOut         BatchFinalizeType = "EmptyBatchTimeOut"
	BatchCounterOverflow BatchFinalizeType = "BatchCounterOverflow"
	BatchLimboRecovery   BatchFinalizeType = "LimboRecovery"
)

var (
	// OperationTiming tracks operation timing in seconds
	OperationTiming = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xlayer_operation_timing_seconds",
			Help:    "Xlayer operation timing in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 3.0, 5.0, 10.0, 15.0, 30.0, 60.0},
		},
		[]string{"component", "metric_type"},
	)

	// OperationGauge tracks current state of operations
	OperationGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
			Name: "xlayer_operation_current",
			Help: "Current state of xlayer operations (timing in seconds, others in original units)",
		},
		[]string{"component", "metric_type"},
	)

	// OperationCounter counts operations
	OperationCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xlayer_operation_counter",
			Help: "Total count of xlayer operations",
		},
		[]string{"component", "metric_type"},
	)
)

// Init registers all metrics with Prometheus
func Init() {
	prometheus.MustRegister(OperationTiming)
	prometheus.MustRegister(OperationCounter)
	prometheus.MustRegister(OperationGauge)
}

// Block timing functions
func RecordBlockExecuteTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("block", "execute_timing").Observe(seconds)
	OperationGauge.WithLabelValues("block", "execute_timing").Set(seconds)
}

func RecordBlockProcessTxTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("block", "process_tx_timing").Observe(seconds)
	OperationGauge.WithLabelValues("block", "process_tx_timing").Set(seconds)
}

func RecordBlockGetTxTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("block", "get_tx_timing").Observe(seconds)
	OperationGauge.WithLabelValues("block", "get_tx_timing").Set(seconds)
}

func RecordBlockGetTxPauseTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("block", "get_tx_pause_timing").Observe(seconds)
	OperationGauge.WithLabelValues("block", "get_tx_pause_timing").Set(seconds)
}

func RecordBlockSetSmtCacheTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("block", "set_smt_cache_timing").Observe(seconds)
	OperationGauge.WithLabelValues("block", "set_smt_cache_timing").Set(seconds)
}

// Batch timing functions
func RecordBatchExecuteTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "execute_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "execute_timing").Set(seconds)
}

func RecordBatchSequencingTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "sequencing_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "sequencing_timing").Set(seconds)
}

func RecordBatchProcessTxTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "process_tx_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "process_tx_timing").Set(seconds)
}

func RecordBatchGetTxTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "get_tx_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "get_tx_timing").Set(seconds)
}

func RecordBatchGetTxPauseTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "get_tx_pause_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "get_tx_pause_timing").Set(seconds)
}

func RecordBatchPbStateTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "pb_state_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "pb_state_timing").Set(seconds)
}

func RecordBatchZkIncIntermediateHashesTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "zk_inc_intermediate_hashes_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "zk_inc_intermediate_hashes_timing").Set(seconds)
}

func RecordBatchFinaliseBlockWriteTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "finalise_block_write_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "finalise_block_write_timing").Set(seconds)
}

func RecordBatchSmtCommitDBTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "smt_commit_db_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "smt_commit_db_timing").Set(seconds)
}

func RecordBatchCommitDBTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "commit_db_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "commit_db_timing").Set(seconds)
}

func RecordBatchSetSmtCacheTimingMs(durationMs int64) {
	seconds := float64(durationMs) / 1000.0
	OperationTiming.WithLabelValues("batch", "set_smt_cache_timing").Observe(seconds)
	OperationGauge.WithLabelValues("batch", "set_smt_cache_timing").Set(seconds)
}

// Gauge functions
func SetBlockGasUsed(gasUsed float64) {
	OperationGauge.WithLabelValues("block", "gas_used").Set(gasUsed)
}

func SetRpcDynamicGasPrice(gasPrice float64) {
	OperationGauge.WithLabelValues("rpc", "dynamic_gas_price").Set(gasPrice)
}

// Counter functions
func IncBlockTxCount(txCount float64) {
	OperationCounter.WithLabelValues("block", "tx_count").Add(txCount)
}

func IncBlockInvalidTxCount(invalidTxCount float64) {
	OperationCounter.WithLabelValues("block", "invalid_tx_count").Add(invalidTxCount)
}

func IncBatchTxCount(txCount float64) {
	OperationCounter.WithLabelValues("batch", "tx_count").Add(txCount)
}

func IncBatchInvalidTxCount(invalidTxCount float64) {
	OperationCounter.WithLabelValues("batch", "invalid_tx_count").Add(invalidTxCount)
}

func IncRpcInnerTxExecuted(innerTxCount float64) {
	OperationCounter.WithLabelValues("rpc", "inner_tx_count").Add(innerTxCount)
}
