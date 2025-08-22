package stages

import (
	"context"

	proto_downloader "github.com/ledgerwatch/erigon-lib/gointerfaces/downloader"
	"github.com/ledgerwatch/erigon-lib/kv"
	"github.com/ledgerwatch/erigon-lib/state"
	"github.com/ledgerwatch/erigon/consensus"
	"github.com/ledgerwatch/erigon/core/rawdb/blockio"
	state2 "github.com/ledgerwatch/erigon/core/state"
	"github.com/ledgerwatch/erigon/core/types"
	"github.com/ledgerwatch/erigon/core/vm"
	"github.com/ledgerwatch/erigon/eth/ethconfig"
	"github.com/ledgerwatch/erigon/eth/stagedsync"
	"github.com/ledgerwatch/erigon/p2p/sentry/sentry_multi_client"
	"github.com/ledgerwatch/erigon/turbo/engineapi/engine_helpers"
	"github.com/ledgerwatch/erigon/turbo/shards"
	"github.com/ledgerwatch/erigon/turbo/snapshotsync/freezeblocks"
	"github.com/ledgerwatch/erigon/zk/datastream/server"
	"github.com/ledgerwatch/erigon/zk/l1infotree"
	realtimeCache "github.com/ledgerwatch/erigon/zk/realtime/cache"
	realtimeTypes "github.com/ledgerwatch/erigon/zk/realtime/types"
	zkStages "github.com/ledgerwatch/erigon/zk/stages"
	"github.com/ledgerwatch/erigon/zk/syncer"
	"github.com/ledgerwatch/erigon/zk/txpool"
)

// NewDefaultZkStages creates stages for zk syncer (RPC mode)
func NewDefaultZkStages(ctx context.Context,
	db kv.RwDB,
	dbsmt kv.RwDB,
	cfg *ethconfig.Config,
	controlServer *sentry_multi_client.MultiClient,
	notifications *shards.Notifications,
	snapDownloader proto_downloader.DownloaderClient,
	snapshots *freezeblocks.RoSnapshots,
	agg *state.Aggregator,
	forkValidator *engine_helpers.ForkValidator,
	engine consensus.Engine,
	l1Syncer *syncer.L1Syncer,
	l1BlockSyncer *syncer.L1Syncer, // 添加：支持Sequencer L1区块同步
	sequencerL1Syncer *syncer.L1Syncer, // 添加：RPC节点专用的Sequencer L1同步器（Sequencer节点传nil）
	datastreamClient zkStages.DatastreamClient,
	dataStreamServer server.DataStreamServer,
	infoTreeUpdater *l1infotree.Updater,
	// For X Layer, realtime
	realtimeCache *realtimeCache.RealtimeCache,
	realtimeFinishChan chan realtimeTypes.FinishedEntry,
) []*stagedsync.Stage {
	dirs := cfg.Dirs
	blockWriter := blockio.NewBlockWriter(cfg.HistoryV3)
	blockReader := freezeblocks.NewBlockReader(snapshots, nil)

	// todo: upstream merge
	// blockRetire := freezeblocks.NewBlockRetire(1, dirs, blockReader, blockWriter, db, cfg.Genesis.Config, notifications.Events, logger)

	// During Import we don't want other services like header requests, body requests etc. to be running.
	// Hence we run it in the test mode.
	runInTestMode := cfg.ImportMode

	// 构建stage配置参数
	l1SyncerCfg := zkStages.StageL1SyncerCfg(db, l1Syncer, cfg.Zk)
	l1InfoTreeCfg := zkStages.StageL1InfoTreeCfg(db, cfg.Zk, infoTreeUpdater)

	var l1SequencerSyncCfg zkStages.L1SequencerSyncCfg
	var sequencerL1BlockSyncCfg zkStages.SequencerL1BlockSyncCfg

	// 如果是RPC节点（sequencerL1Syncer != nil），配置Sequencer同步stages
	if sequencerL1Syncer != nil {
		l1SequencerSyncCfg = zkStages.StageL1SequencerSyncCfg(db, cfg.Zk, sequencerL1Syncer)
		sequencerL1BlockSyncCfg = zkStages.StageSequencerL1BlockSyncCfg(db, cfg.Zk, l1BlockSyncer)
	}

	return zkStages.DefaultZkStages(ctx,
		l1SyncerCfg,
		l1SequencerSyncCfg, // RPC节点有值，Sequencer节点为空
		l1InfoTreeCfg,
		sequencerL1BlockSyncCfg, // RPC节点有值，Sequencer节点为空
		zkStages.StageBatchesCfg(db, datastreamClient, cfg.Zk, controlServer.ChainConfig, &cfg.Miner),
		zkStages.StageDataStreamCatchupCfg(dataStreamServer, db, cfg.Genesis.Config.ChainID.Uint64(), cfg.DatastreamVersion),
		stagedsync.StageBlockHashesCfg(db, dirs.Tmp, controlServer.ChainConfig, blockWriter),
		stagedsync.StageSendersCfg(db, controlServer.ChainConfig, false, dirs.Tmp, cfg.Prune, blockReader, controlServer.Hd, nil),
		stagedsync.StageExecuteBlocksCfg(
			db,
			cfg.Prune,
			cfg.BatchSize,
			nil,
			controlServer.ChainConfig,
			controlServer.Engine,
			&vm.Config{},
			notifications.Accumulator,
			cfg.StateStream,
			/*stateStream=*/ false,
			cfg.HistoryV3,
			dirs,
			blockReader,
			controlServer.Hd,
			cfg.Genesis,
			cfg.Sync,
			agg,
			cfg.Zk,
			nil,
		),
		stagedsync.StageHashStateCfg(db, dirs, cfg.HistoryV3, agg),
		// For X Layer, split db and ac
		zkStages.StageZkInterHashesCfg(db, dbsmt, true, true, false, dirs.Tmp, blockReader, controlServer.Hd, cfg.HistoryV3, agg, cfg.Zk),
		stagedsync.StageHistoryCfg(db, cfg.Prune, dirs.Tmp),
		stagedsync.StageLogIndexCfg(db, cfg.Prune, dirs.Tmp, cfg.Genesis.Config.NoPruneContracts),
		stagedsync.StageCallTracesCfg(db, cfg.Prune, 0, dirs.Tmp),
		stagedsync.StageTxLookupCfg(db, cfg.Prune, dirs.Tmp, controlServer.ChainConfig.Bor, blockReader),
		// For X Layer. RPC latency optimization
		stagedsync.StageFinishCfg(db, dirs.Tmp, forkValidator, realtimeCache, cfg.XLayer.Realtime.Enable, cfg.XLayer.Realtime.CacheHeightThreshold, realtimeFinishChan),
		runInTestMode)
}

// NewSequencerZkStages creates stages for a zk sequencer
func NewSequencerZkStages(ctx context.Context,
	db kv.RwDB,
	dbsmt kv.RwDB,
	cfg *ethconfig.Config,
	controlServer *sentry_multi_client.MultiClient,
	notifications *shards.Notifications,
	snapDownloader proto_downloader.DownloaderClient,
	snapshots *freezeblocks.RoSnapshots,
	agg *state.Aggregator,
	forkValidator *engine_helpers.ForkValidator,
	engine consensus.Engine,
	dataStreamServer server.DataStreamServer,
	sequencerStageSyncer *syncer.L1Syncer,
	l1Syncer *syncer.L1Syncer,
	l1BlockSyncer *syncer.L1Syncer,
	txPool *txpool.TxPool,
	txPoolDb kv.RwDB,
	infoTreeUpdater *l1infotree.Updater,
	hook *Hook,
	kafkaBlockInfoChan chan *types.Header,
	kafkaTxInfoChan chan state2.TxInfo,
) []*stagedsync.Stage {
	dirs := cfg.Dirs
	blockReader := freezeblocks.NewBlockReader(snapshots, nil)

	// During Import we don't want other services like header requests, body requests etc. to be running.
	// Hence we run it in the test mode.
	runInTestMode := cfg.ImportMode

	return zkStages.SequencerZkStages(ctx,
		zkStages.StageL1SyncerCfg(db, l1Syncer, cfg.Zk),
		zkStages.StageL1SequencerSyncCfg(db, cfg.Zk, sequencerStageSyncer),
		zkStages.StageL1InfoTreeCfg(db, cfg.Zk, infoTreeUpdater),
		zkStages.StageSequencerL1BlockSyncCfg(db, cfg.Zk, l1BlockSyncer),
		zkStages.StageDataStreamCatchupCfg(dataStreamServer, db, cfg.Genesis.Config.ChainID.Uint64(), cfg.DatastreamVersion),
		zkStages.StageSequenceBlocksCfg(
			db,
			dbsmt,
			cfg.Prune,
			cfg.BatchSize,
			nil,
			controlServer.ChainConfig,
			controlServer.Engine,
			&vm.ZkConfig{},
			notifications.Accumulator,
			cfg.StateStream,
			/*stateStream=*/ false,
			cfg.HistoryV3,
			dirs,
			blockReader,
			cfg.Genesis,
			cfg.Sync,
			agg,
			dataStreamServer,
			cfg.Zk,
			&cfg.Miner,
			txPool,
			txPoolDb,
			uint16(cfg.YieldSize),
			infoTreeUpdater,
			hook,
			kafkaBlockInfoChan,
			kafkaTxInfoChan,
		),
		stagedsync.StageHashStateCfg(db, dirs, cfg.HistoryV3, agg),
		// For X Layer, split db and ac
		zkStages.StageZkInterHashesCfg(db, dbsmt, true, true, false, dirs.Tmp, blockReader, controlServer.Hd, cfg.HistoryV3, agg, cfg.Zk),
		stagedsync.StageHistoryCfg(db, cfg.Prune, dirs.Tmp),
		stagedsync.StageLogIndexCfg(db, cfg.Prune, dirs.Tmp, cfg.Genesis.Config.NoPruneContracts),
		stagedsync.StageCallTracesCfg(db, cfg.Prune, 0, dirs.Tmp),
		stagedsync.StageTxLookupCfg(db, cfg.Prune, dirs.Tmp, controlServer.ChainConfig.Bor, blockReader),
		// For X Layer, realtime
		stagedsync.StageFinishCfg(db, dirs.Tmp, forkValidator, nil, false, 0, nil),
		runInTestMode)
}
