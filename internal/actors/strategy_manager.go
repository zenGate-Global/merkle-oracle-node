package actors

import (
	"fmt"
	"sync"
	"time"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	"zenGate-Global/merkle-oracle-node/internal/cloud"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"
	"zenGate-Global/merkle-oracle-node/internal/metrics"
	"zenGate-Global/merkle-oracle-node/internal/oprovider"
	"zenGate-Global/merkle-oracle-node/internal/types"

	connector "github.com/zenGate-Global/cardano-connector-go"
)

type LoadStrategyCommand struct {
	Config types.StrategyConfig
}

type UnloadStrategyCommand struct {
	StrategyID string
}

// RestartRecord tracks when a restart request happened
type RestartRecord struct {
	Timestamp time.Time
	Reason    string
	Slot      uint64
	Hash      string
}

type StrategyManagerActor struct {
	cfg               *config.Config
	logger            *zap.SugaredLogger
	engine            *actor.Engine
	selfPID           *actor.PID
	strategyInstances map[string]*actor.PID           // Key: StrategyConfig.ID
	strategyConfigs   map[string]types.StrategyConfig // Track configs for restart
	indexerPID        *actor.PID                      // Reference to the IndexerActor
	strategyRegistry  *StrategyRegistry               // Registry for strategy factories
	provider          connector.Provider              // Shared provider instance
	db                *database.Database

	// Restart tracking
	restartHistory []RestartRecord
	restartMutex   sync.RWMutex
}

func NewStrategyManagerActor(
	cfg *config.Config,
	provider connector.Provider,
	db *database.Database,
	cloud cloud.Cloud,
	oracleDataProvider oprovider.Provider,
) actor.Producer {
	return func() actor.Receiver {
		return &StrategyManagerActor{
			cfg: cfg,
			logger: logging.GetLogger().
				With("actor", "StrategyManagerActor"),
			strategyInstances: make(map[string]*actor.PID),
			strategyConfigs:   make(map[string]types.StrategyConfig),
			strategyRegistry: NewStrategyRegistry(
				db,
				cloud,
				oracleDataProvider,
			),
			provider: provider,
			db:       db,
		}
	}
}

func (sm *StrategyManagerActor) Receive(c *actor.Context) {
	switch msg := c.Message().(type) {
	case actor.Initialized:
		sm.logger.Info("initializing")
		sm.engine = c.Engine()
		sm.selfPID = c.PID()

	case actor.Started:
		sm.logger.Debug("started")
		indexerPID := c.SpawnChild(
			NewIndexerActor(sm.cfg, sm.db),
			"indexer",
			actor.WithID("primary"),
			actor.WithMaxRestarts(5),
			actor.WithRestartDelay(10*time.Second),
		)
		sm.indexerPID = indexerPID
		sm.logger.Infow("IndexerActor spawned as child", "pid", indexerPID)

		sm.engine.Send(indexerPID, SetDownstream{PID: sm.selfPID})

	case actor.Stopped:
		sm.logger.Info("stopped")

	case types.IndexerBlockEvent:
		sm.logger.Debugw("received IndexerBlockEvent, forwarding to strategies",
			"block_number", msg.BlockEvent.Block.BlockNumber(), "tip_reached", msg.TipReached)
		sm.broadcastToStrategies(msg)

	case types.IndexerTransactionEvent:
		sm.logger.Debugw("received IndexerTransactionEvent, forwarding to strategies",
			"transaction_id", msg.EventTransaction.Transaction.Hash().String())
		sm.broadcastToStrategies(msg)

	case types.IndexerRollbackEvent:
		sm.logger.Infow("received IndexerRollbackEvent, forwarding to strategies",
			"slot_number", msg.SlotNumber)
		sm.broadcastToStrategies(msg)

	case types.IndexerStatusEvent:
		sm.logger.Debugw("received IndexerStatusEvent, forwarding to strategies",
			"cursor_slot", msg.CursorSlot, "tip_reached", msg.TipReached)
		sm.broadcastToStrategies(msg)

	case LoadStrategyCommand:
		sm.logger.Debugw("received LoadStrategyCommand", "strategy_id", msg.Config.ID, "kind", msg.Config.Kind)
		sm.loadStrategy(c, msg.Config)

	case UnloadStrategyCommand:
		sm.logger.Debugw("received UnloadStrategyCommand", "strategy_id", msg.StrategyID)
		sm.unloadStrategy(msg.StrategyID)

	case actor.ActorStoppedEvent:
		sm.logger.Debugw("child actor stopped", "pid", msg.PID.String())

		// Add debug logging to see all stopped events
		sm.logger.Infow("ActorStoppedEvent received",
			"stopped_pid", msg.PID.String(),
			"indexer_pid", func() string {
				if sm.indexerPID != nil {
					return sm.indexerPID.String()
				}
				return "nil"
			}(),
			"are_equal", sm.indexerPID != nil && sm.indexerPID.Equals(msg.PID))

		if sm.indexerPID != nil && sm.indexerPID.Equals(msg.PID) {
			sm.logger.Warnw("IndexerActor child stopped unexpectedly", "pid", msg.PID.String())
			metrics.RecordActorRestart("IndexerActor", "primary")
			sm.indexerPID = nil

			sm.logger.Infow("scheduling IndexerActor restart")
			go sm.autoRestartIndexer(c)
		}

		for id, pid := range sm.strategyInstances {
			if pid.Equals(msg.PID) {
				delete(sm.strategyInstances, id)
				metrics.DecrementStrategyCount()
				sm.logger.Debugw("removed stopped strategy instance from tracking", "strategy_id", id)

				if sm.shouldAutoRestart(id) {
					strategyCfg, exists := sm.strategyConfigs[id]
					if exists {
						metrics.RecordStrategyRestart(id, strategyCfg.Kind)
					}
					sm.logger.Warnw("auto-restarting critical strategy", "strategy_id", id)
					go sm.autoRestartStrategy(c, id)
				}
				break
			}
		}

	case types.IndexerRestartRequest:
		sm.logger.Warnw(
			"received indexer restart request",
			"reason",
			msg.Reason,
			"strategy_pid",
			msg.StrategyPID.String(),
			"restart_slot",
			msg.RestartSlot,
			"restart_hash",
			msg.RestartHash,
		)
		sm.handleIndexerRestartRequest(c, msg)

	case types.IndexerRestartComplete:
		sm.logger.Infow("received indexer restart complete", "success", msg.Success)
		sm.broadcastToStrategies(msg)

	default:
		sm.logger.Warn("received unknown message", "type", fmt.Sprintf("%T", msg))
	}
}

func (sm *StrategyManagerActor) broadcastToStrategies(event interface{}) {
	if len(sm.strategyInstances) == 0 {
		sm.logger.Debug("no active strategies to forward event to")
		return
	}
	for strategyID, pid := range sm.strategyInstances {
		sm.logger.Debugw(
			"forwarding event to strategy",
			"strategy_id",
			strategyID,
			"pid",
			pid.String(),
			"event_type",
			fmt.Sprintf("%T", event),
		)
		sm.engine.Send(pid, event)
	}
}

func (sm *StrategyManagerActor) loadStrategy(
	ctx *actor.Context,
	strategyCfg types.StrategyConfig,
) {
	if _, exists := sm.strategyInstances[strategyCfg.ID]; exists {
		sm.logger.Warnw(
			"strategy already loaded",
			"strategy_id",
			strategyCfg.ID,
		)
		return
	}

	producer, exists := sm.strategyRegistry.CreateStrategy(
		strategyCfg,
		sm.cfg,
		sm.provider,
	)
	if !exists {
		sm.logger.Errorw(
			"unknown strategy kind",
			"kind",
			strategyCfg.Kind,
			"available",
			sm.strategyRegistry.GetAvailableStrategies(),
		)
		return
	}

	if producer == nil {
		sm.logger.Errorw(
			"failed to create strategy producer - strategy initialization failed",
			"strategy_id",
			strategyCfg.ID,
			"kind",
			strategyCfg.Kind,
		)
		return
	}

	childPID := ctx.SpawnChild(
		producer,
		strategyCfg.Kind,
		actor.WithID(strategyCfg.ID),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(10*time.Second),
	)
	sm.strategyInstances[strategyCfg.ID] = childPID
	sm.strategyConfigs[strategyCfg.ID] = strategyCfg

	metrics.IncrementStrategyCount()

	// Send parent PID to the strategy so it can communicate back
	sm.engine.Send(childPID, types.SetParentPID{PID: sm.selfPID})

	sm.logger.Infow(
		"strategy instance spawned",
		"strategy_id",
		strategyCfg.ID,
		"kind",
		strategyCfg.Kind,
		"pid",
		childPID.String(),
	)
}

func (sm *StrategyManagerActor) unloadStrategy(strategyID string) {
	pid, exists := sm.strategyInstances[strategyID]
	if !exists {
		sm.logger.Warnw(
			"strategy not found for unloading",
			"strategy_id",
			strategyID,
		)
		return
	}

	sm.logger.Infow(
		"poisoning strategy instance",
		"strategy_id",
		strategyID,
		"pid",
		pid.String(),
	)
	delete(
		sm.strategyConfigs,
		strategyID,
	) // Remove config to prevent auto-restart

	metrics.DecrementStrategyCount()

	sm.engine.Poison(pid)
}

// shouldAutoRestart determines if a strategy should be automatically restarted
func (sm *StrategyManagerActor) shouldAutoRestart(strategyID string) bool {
	criticalStrategies := map[string]bool{
		"chain-event-processor": true,
	}

	return criticalStrategies[strategyID]
}

// autoRestartStrategy attempts to restart a failed strategy
func (sm *StrategyManagerActor) autoRestartStrategy(
	ctx *actor.Context,
	strategyID string,
) {
	// Wait a bit before restarting to avoid rapid restart loops
	time.Sleep(5 * time.Second)

	// Check if we still have the config
	strategyCfg, exists := sm.strategyConfigs[strategyID]
	if !exists {
		sm.logger.Errorw(
			"cannot restart strategy - config not found",
			"strategy_id",
			strategyID,
		)
		return
	}

	// Check if it's already been restarted manually
	if _, exists := sm.strategyInstances[strategyID]; exists {
		sm.logger.Infow(
			"strategy already restarted manually",
			"strategy_id",
			strategyID,
		)
		return
	}

	sm.logger.Infow("attempting to restart strategy", "strategy_id", strategyID)
	sm.loadStrategy(ctx, strategyCfg)
}

// autoRestartIndexer attempts to restart a failed IndexerActor
func (sm *StrategyManagerActor) autoRestartIndexer(ctx *actor.Context) {
	time.Sleep(20 * time.Second)

	if sm.indexerPID != nil {
		sm.logger.Infow("IndexerActor already restarted manually")
		return
	}

	sm.logger.Infow("attempting to restart IndexerActor")

	newIndexerPID := ctx.SpawnChild(
		NewIndexerActor(sm.cfg, sm.db),
		"indexer",
		actor.WithID("primary"),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(10*time.Second),
	)

	sm.indexerPID = newIndexerPID
	sm.logger.Infow("IndexerActor restarted", "new_pid", newIndexerPID.String())

	sm.engine.Send(newIndexerPID, SetDownstream{PID: sm.selfPID})
}

// addRestartRecord adds a new restart record to the history
func (sm *StrategyManagerActor) addRestartRecord(
	reason string,
	slot uint64,
	hash string,
) {
	sm.restartMutex.Lock()
	defer sm.restartMutex.Unlock()

	record := RestartRecord{
		Timestamp: time.Now(),
		Reason:    reason,
		Slot:      slot,
		Hash:      hash,
	}

	sm.restartHistory = append(sm.restartHistory, record)

	// Clean up old records outside the time window
	sm.cleanupOldRestartRecords()

	sm.logger.Infow("added restart record",
		"reason", reason,
		"slot", slot,
		"hash", hash,
		"total_restarts_in_window", len(sm.restartHistory))
}

// cleanupOldRestartRecords removes restart records outside the configured time window
func (sm *StrategyManagerActor) cleanupOldRestartRecords() {
	cutoff := time.Now().Add(-sm.cfg.Indexer.RestartTimeWindow)

	var validRecords []RestartRecord
	for _, record := range sm.restartHistory {
		if record.Timestamp.After(cutoff) {
			validRecords = append(validRecords, record)
		}
	}

	sm.restartHistory = validRecords
}

// shouldDoFullReset determines if we should do a full reset based on restart frequency
func (sm *StrategyManagerActor) shouldDoFullReset() bool {
	sm.restartMutex.RLock()
	defer sm.restartMutex.RUnlock()

	recentRestarts := len(sm.restartHistory)
	threshold := sm.cfg.Indexer.RestartThreshold

	sm.logger.Infow("checking restart threshold",
		"recent_restarts", recentRestarts,
		"threshold", threshold,
		"time_window", sm.cfg.Indexer.RestartTimeWindow)

	return recentRestarts >= threshold
}

func (sm *StrategyManagerActor) handleIndexerRestartRequest(
	ctx *actor.Context,
	request types.IndexerRestartRequest,
) {
	sm.logger.Warnw(
		"handling indexer restart request",
		"reason",
		request.Reason,
		"strategy_pid",
		request.StrategyPID.String(),
		"restart_slot",
		request.RestartSlot,
		"restart_hash",
		request.RestartHash,
	)

	if sm.indexerPID == nil {
		sm.logger.Error(
			"cannot perform indexer restart: indexer PID not available",
		)
		return
	}

	// Add restart record and check if we should do a full reset
	sm.addRestartRecord(
		request.Reason,
		request.RestartSlot,
		request.RestartHash,
	)

	var restartSlot uint64
	var restartHash string
	var restartType string

	if sm.shouldDoFullReset() {
		// Too many restarts in the time window - do a full reset
		restartSlot = sm.cfg.Indexer.InterceptSlot
		restartHash = sm.cfg.Indexer.InterceptHash
		restartType = "full_reset"

		// Record circuit breaker activation
		metrics.MetricRestartCircuitBreakerActivations.Inc()

		sm.logger.Warnw(
			"restart threshold exceeded - performing full reset to config intercept point",
			"threshold",
			sm.cfg.Indexer.RestartThreshold,
			"time_window",
			sm.cfg.Indexer.RestartTimeWindow,
			"reset_slot",
			restartSlot,
			"reset_hash",
			restartHash,
		)

		// Clear restart history after a full reset
		sm.restartMutex.Lock()
		sm.restartHistory = nil
		sm.restartMutex.Unlock()
	} else {
		// Use the restart slot and hash from the request (partial restart)
		restartSlot = request.RestartSlot
		restartHash = request.RestartHash
		restartType = "partial_restart"

		sm.logger.Infow(
			"restarting from last successfully indexed position",
			"restart_slot", restartSlot,
			"restart_hash", restartHash,
		)
	}

	// Record restart metrics
	metrics.MetricIndexerRestarts.WithLabelValues(restartType, request.Reason).
		Inc()

	if err := sm.poisonAndWaitForStrategy(request.StrategyPID); err != nil {
		sm.logger.Error("failed to poison strategy actor", "err", err)
		return
	}

	if err := sm.db.RollbackCursor(restartSlot); err != nil {
		sm.logger.Error("failed to rollback cursor", "err", err)
		return
	}

	sm.logger.Infow(
		"poisoning indexer actor and waiting for complete shutdown",
		"pid",
		sm.indexerPID.String(),
	)
	poisonCtx := sm.engine.Poison(sm.indexerPID)
	<-poisonCtx.Done()
	sm.logger.Infow("indexer actor completely stopped")

	newIndexerPID := ctx.SpawnChild(
		NewIndexerActor(sm.cfg, sm.db),
		"indexer",
		actor.WithID("primary"),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(10*time.Second),
	)

	sm.indexerPID = newIndexerPID
	sm.logger.Infow("new IndexerActor spawned as child", "pid", newIndexerPID)

	sm.engine.Send(newIndexerPID, SetDownstream{PID: sm.selfPID})

	if err := sm.spawnFreshStrategyActor(ctx, request.StrategyPID, restartSlot, restartHash); err != nil {
		sm.logger.Error("failed to spawn fresh strategy actor", "err", err)
		return
	}

	sm.logger.Infow(
		"indexer restart completed successfully",
		"new_indexer_pid",
		newIndexerPID.String(),
		"restart_type",
		restartType,
		"restart_slot",
		restartSlot,
		"restart_hash",
		restartHash,
	)
}

// poisonAndWaitForStrategy poisons a strategy actor and waits for it to completely stop
func (sm *StrategyManagerActor) poisonAndWaitForStrategy(
	strategyPID *actor.PID,
) error {
	// Find the strategy ID
	var strategyID string
	var found bool

	for id, pid := range sm.strategyInstances {
		if pid.Equals(strategyPID) {
			strategyID = id
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf(
			"strategy not found for PID: %s",
			strategyPID.String(),
		)
	}

	sm.logger.Infow(
		"poisoning strategy actor and waiting for complete shutdown",
		"strategy_id",
		strategyID,
		"pid",
		strategyPID.String(),
	)

	// Poison the strategy actor
	poisonCtx := sm.engine.Poison(strategyPID)

	// Wait for it to fully stop to ensure all processing has ceased
	<-poisonCtx.Done()
	sm.logger.Infow(
		"strategy actor completely stopped",
		"strategy_id",
		strategyID,
	)

	// Remove from tracking
	delete(sm.strategyInstances, strategyID)

	return nil
}

// spawnFreshStrategyActor spawns a new strategy actor with the same configuration
func (sm *StrategyManagerActor) spawnFreshStrategyActor(
	ctx *actor.Context,
	oldStrategyPID *actor.PID,
	restartSlot uint64,
	restartHash string,
) error {
	// Find the strategy config by the old PID (we need to search in configs since instances was cleared)
	var strategyID string
	var strategyConfig types.StrategyConfig
	var found bool

	// Since we removed from strategyInstances, we need to find by searching configs
	// and matching the PID pattern. For now, let's assume it's the unified strategy
	// since that's what triggers restarts
	for id, config := range sm.strategyConfigs {
		if config.Kind == "chain-event-processor" {
			strategyID = id
			strategyConfig = config
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("strategy config not found for restarting")
	}

	strategyConfig.StartBlockSlot = restartSlot
	strategyConfig.StartBlockHash = restartHash

	// Spawn a new strategy actor with the same ID
	producer, exists := sm.strategyRegistry.CreateStrategy(
		strategyConfig,
		sm.cfg,
		sm.provider,
	)
	if !exists {
		return fmt.Errorf("unknown strategy kind: %s", strategyConfig.Kind)
	}

	if producer == nil {
		return fmt.Errorf(
			"failed to create strategy producer - strategy initialization failed for %s",
			strategyID,
		)
	}

	newPID := ctx.SpawnChild(
		producer,
		strategyConfig.Kind,
		actor.WithID(strategyID),
		actor.WithMaxRestarts(5),
		actor.WithRestartDelay(10*time.Second),
	)
	sm.strategyInstances[strategyID] = newPID

	// Send parent PID to the new strategy so it can communicate back
	sm.engine.Send(newPID, types.SetParentPID{PID: sm.selfPID})

	sm.logger.Infow("fresh strategy actor spawned",
		"strategy_id", strategyID,
		"new_pid", newPID.String(),
		"old_pid", oldStrategyPID.String(),
		"restart_slot", restartSlot,
		"restart_hash", restartHash)

	return nil
}
