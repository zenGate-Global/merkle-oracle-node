package actors

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"
	"zenGate-Global/merkle-oracle-node/internal/metrics"
	"zenGate-Global/merkle-oracle-node/internal/types"

	"github.com/blinklabs-io/adder/event"
	addevent "github.com/blinklabs-io/adder/event"
	filter_event "github.com/blinklabs-io/adder/filter/event"
	input_chainsync "github.com/blinklabs-io/adder/input/chainsync"
	output_embedded "github.com/blinklabs-io/adder/output/embedded"
	"github.com/blinklabs-io/adder/pipeline"

	ocommon "github.com/blinklabs-io/gouroboros/protocol/common"
)

const (
	syncStatusLogInterval = 30 * time.Second
)

// Message to tell IndexerActor who to send events to
type SetDownstream struct {
	PID *actor.PID
}

// Internal message to carry pipeline events from goroutine to actor's Receive
type internalPipelineEvent struct {
	event event.Event
	err   error
}

// Internal message for status updates
type internalStatusUpdate struct {
	status input_chainsync.ChainSyncStatus
}

// IndexerActor manages the blockchain indexing pipeline and forwards events
type IndexerActor struct {
	cfg            *config.Config
	db             *database.Database
	logger         *zap.SugaredLogger
	pipeline       *pipeline.Pipeline
	pipelineCtx    context.Context
	pipelineCancel context.CancelFunc
	cursorSlot     uint64
	cursorHash     string
	tipSlot        uint64
	tipHash        string
	tipReached     bool
	syncLogTimer   *time.Timer
	downstreamPID  *actor.PID
	engine         *actor.Engine
	selfPID        *actor.PID
}

func NewIndexerActor(cfg *config.Config, db *database.Database) actor.Producer {
	return func() actor.Receiver {
		return &IndexerActor{
			cfg:    cfg,
			db:     db,
			logger: logging.GetLogger().With("actor", "IndexerActor"),
		}
	}
}

func (a *IndexerActor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Initialized:
		a.logger.Info("initializing")
		a.engine = c.Engine()
		a.selfPID = c.PID()

	case actor.Started:
		a.logger.Info("started")
		a.pipelineCtx, a.pipelineCancel = context.WithCancel(context.Background())
		if err := a.initializeAndStartPipeline(); err != nil {
			a.logger.Errorf("failed to initialize or start pipeline: %v", err)
			panic(fmt.Sprintf("pipeline initialization failed: %v", err))
		}

	case actor.Stopped:
		a.logger.Info("stopping adder pipeline")
		if a.pipelineCancel != nil {
			a.pipelineCancel()
		}
		if a.syncLogTimer != nil {
			a.syncLogTimer.Stop()
		}
		if a.pipeline != nil {
			if err := a.pipeline.Stop(); err != nil {
				a.logger.Error("failed to stop pipeline", "err", err)
			}
		}
		a.logger.Info("stopped")

	case SetDownstream:
		a.logger.Infow("downstream PID set", "pid", msg.PID)
		a.downstreamPID = msg.PID

	case internalPipelineEvent:
		if msg.err != nil {
			a.logger.Error("error from pipeline event", "err", msg.err)
			if msg.err == context.Canceled {
				return // Actor is stopping
			}
			return
		}
		a.processPipelineEvent(c, msg.event)

	case internalStatusUpdate:
		a.processStatusUpdate(msg.status)

	default:
		a.logger.Warn("received unknown message", "type", fmt.Sprintf("%T", msg))
	}
}

func (a *IndexerActor) initializeAndStartPipeline() error {

	a.pipeline = pipeline.New()

	inputOpts := []input_chainsync.ChainSyncOptionFunc{
		input_chainsync.WithAutoReconnect(true),
		input_chainsync.WithLogger(NewAdderLogger(a.logger.Desugar())),
		input_chainsync.WithStatusUpdateFunc(a.handleStatusUpdate),
		input_chainsync.WithNetwork(a.cfg.Network),
	}

	if a.cfg.Indexer.Address != "" {
		inputOpts = append(
			inputOpts,
			input_chainsync.WithAddress(a.cfg.Indexer.Address),
		)
	}

	cursorPoints, err := a.db.GetCursorPoints()
	if err != nil {
		return fmt.Errorf("failed to get cursor: %w", err)
	}

	if len(cursorPoints) > 0 {
		a.logger.Infof(
			"found previous chainsync cursor(s), latest is: %d, %x",
			cursorPoints[0].Slot,
			cursorPoints[0].Hash,
		)
		inputOpts = append(
			inputOpts,
			input_chainsync.WithIntersectPoints(
				cursorPoints,
			),
		)
	} else if a.cfg.Indexer.InterceptHash != "" && a.cfg.Indexer.InterceptSlot > 0 {
		hashBytes, err := hex.DecodeString(a.cfg.Indexer.InterceptHash)
		if err != nil {
			return err
		}
		inputOpts = append(inputOpts, input_chainsync.WithIntersectPoints(
			[]ocommon.Point{{Hash: hashBytes, Slot: a.cfg.Indexer.InterceptSlot}},
		))
	}

	input := input_chainsync.New(inputOpts...)
	a.pipeline.AddInput(input)

	filterEvent := filter_event.New(
		filter_event.WithTypes(
			[]string{
				"chainsync.transaction",
				"chainsync.rollback",
				"chainsync.block",
			},
		),
	)
	a.pipeline.AddFilter(filterEvent)

	// filterChainsync := filter_chainsync.New(
	// 	filter_chainsync.WithPolicies(
	// 		[]string{a.cfg.Contract.SingletonPolicyId},
	// 	),
	// )
	// a.pipeline.AddFilter(filterChainsync)

	output := output_embedded.New(
		output_embedded.WithCallbackFunc(a.handlePipelineEvent),
	)
	a.pipeline.AddOutput(output)

	if err := a.pipeline.Start(); err != nil {
		return fmt.Errorf("failed to start pipeline: %w", err)
	}

	go a.handlePipelineErrors()

	a.scheduleSyncStatusLog()

	return nil
}

func (a *IndexerActor) handlePipelineErrors() {
	for {
		select {
		case <-a.pipelineCtx.Done():
			return
		case err, ok := <-a.pipeline.ErrorChan():
			if !ok {
				return
			}
			a.logger.Error("pipeline error", "err", err)
			panic(fmt.Sprintf("pipeline error: %v", err))
		}
	}
}

func (a *IndexerActor) handlePipelineEvent(evt event.Event) error {
	a.engine.Send(a.selfPID, internalPipelineEvent{event: evt, err: nil})
	return nil
}

func (a *IndexerActor) handleStatusUpdate(
	status input_chainsync.ChainSyncStatus,
) {
	a.engine.Send(a.selfPID, internalStatusUpdate{status: status})
}

func (a *IndexerActor) processPipelineEvent(
	ctx *actor.Context,
	evt event.Event,
) {
	// check for shutdown
	select {
	case <-a.pipelineCtx.Done():
		return // Actor is stopping, don't process more events
	default:
	}

	switch evt.Payload.(type) {
	case addevent.RollbackEvent:
		a.handleEventRollback(evt)
	case addevent.TransactionEvent:
		a.handleEventTransaction(evt)
	case addevent.BlockEvent:
		a.handleNewBlockEvent(evt)
	default:
		a.logger.Warn("unknown event payload type", "type", fmt.Sprintf("%T", evt.Payload))
	}
}

func (a *IndexerActor) processStatusUpdate(
	status input_chainsync.ChainSyncStatus,
) {
	metrics.UpdateSlotMetrics(
		status.SlotNumber,
		status.TipSlotNumber,
		status.TipReached,
	)

	if !a.tipReached && status.TipReached {
		if a.syncLogTimer != nil {
			a.syncLogTimer.Stop()
		}
		a.tipReached = true
	}
	a.cursorSlot = status.SlotNumber
	a.cursorHash = status.BlockHash
	a.tipSlot = status.TipSlotNumber
	a.tipHash = status.TipBlockHash

	isRollback := status.BlockNumber == 0

	if a.downstreamPID != nil {
		statusEvent := types.IndexerStatusEvent{
			CursorSlot:  a.cursorSlot,
			CursorHash:  a.cursorHash,
			BlockNumber: status.BlockNumber,
			TipSlot:     a.tipSlot,
			TipHash:     a.tipHash,
			TipReached:  a.tipReached,
			IsRollback:  isRollback,
		}
		a.engine.Send(a.downstreamPID, statusEvent)
	}
}

func (a *IndexerActor) scheduleSyncStatusLog() {
	a.syncLogTimer = time.AfterFunc(syncStatusLogInterval, a.syncStatusLog)
}

func (a *IndexerActor) syncStatusLog() {
	a.logger.Infof(
		"catch-up sync in progress: at %d.%s (current tip slot is %d)",
		a.cursorSlot,
		a.cursorHash,
		a.tipSlot,
	)
	a.scheduleSyncStatusLog()
}

func (a *IndexerActor) handleNewBlockEvent(evt event.Event) {
	metrics.MetricBlocksProcessed.Inc()

	blockEvent := evt.Payload.(addevent.BlockEvent)

	if a.downstreamPID != nil {
		emissionBlockEvent := types.IndexerBlockEvent{
			BlockEvent: blockEvent,
			Timestamp:  evt.Timestamp,
			TipReached: a.tipReached,
		}
		a.engine.Send(a.downstreamPID, emissionBlockEvent)
	}
}

func (a *IndexerActor) handleEventRollback(evt event.Event) {
	metrics.MetricRollbacksProcessed.Inc()

	eventRollback := evt.Payload.(addevent.RollbackEvent)

	if a.downstreamPID != nil {
		emissionRollbackEvent := types.IndexerRollbackEvent{
			SlotNumber: eventRollback.SlotNumber,
			BlockHash:  eventRollback.BlockHash,
		}
		a.engine.Send(a.downstreamPID, emissionRollbackEvent)
	}

}

func (a *IndexerActor) handleEventTransaction(evt event.Event) {
	metrics.MetricTransactionsProcessed.Inc()

	eventTx := evt.Payload.(addevent.TransactionEvent)
	eventCtx := evt.Context.(addevent.TransactionContext)

	if a.downstreamPID != nil {
		emissionTxEvent := types.IndexerTransactionEvent{
			EventTransaction: eventTx,
			EventContext:     eventCtx,
			EventTimestamp:   evt.Timestamp,
			TipReached:       a.tipReached,
		}
		a.engine.Send(a.downstreamPID, emissionTxEvent)
	}
}

type AdderLogger struct {
	logger *zap.SugaredLogger
}

func NewAdderLogger(logger *zap.Logger) *AdderLogger {
	return &AdderLogger{logger: logger.Sugar()}
}

func (a *AdderLogger) Info(msg string, args ...any) {
	if strings.Contains(msg, "reconnecting") ||
		strings.Contains(msg, "connected to node") {
		return
	}
	a.logger.Infof(msg, args...)
}

func (a *AdderLogger) Warn(msg string, args ...any) {
	a.logger.Warnf(msg, args...)
}

func (a *AdderLogger) Debug(msg string, args ...any) {
	a.logger.Debugf(msg, args...)
}

func (a *AdderLogger) Error(msg string, args ...any) {
	a.logger.Errorf(msg, args...)
}

func (a *AdderLogger) Fatalf(msg string, args ...any) {
	a.logger.Fatalf(msg, args...)
}
