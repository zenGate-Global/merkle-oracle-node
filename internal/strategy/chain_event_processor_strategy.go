package strategy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"zenGate-Global/merkle-oracle-node/internal/cloud"
	"zenGate-Global/merkle-oracle-node/internal/config"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"
	"zenGate-Global/merkle-oracle-node/internal/metrics"
	"zenGate-Global/merkle-oracle-node/internal/oprovider"
	"zenGate-Global/merkle-oracle-node/internal/provider"
	"zenGate-Global/merkle-oracle-node/internal/tx"
	"zenGate-Global/merkle-oracle-node/internal/types"
	"zenGate-Global/merkle-oracle-node/internal/version"

	"github.com/anthdm/hollywood/actor"
	"github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/disgoorg/disgo/webhook"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	connector "github.com/zenGate-Global/cardano-connector-go"
	"go.uber.org/zap"

	gouroborosCbor "github.com/blinklabs-io/gouroboros/cbor"

	apolloTxInput "github.com/Salvionied/apollo/serialization/TransactionInput"
	apolloTxOutput "github.com/Salvionied/apollo/serialization/TransactionOutput"
	apolloUTxO "github.com/Salvionied/apollo/serialization/UTxO"
)

type trieLike interface {
	Hash() []byte
	Has([]byte) bool
	Update([]byte, []byte, uint64) error
	Delete([]byte) error
}

type KVHex struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type KeyHex struct {
	Key string `json:"key"`
}

func bytes32(b []byte) (out [32]byte) { copy(out[:], b); return }

type kvBytes struct {
	k [32]byte
	v [32]byte
}

func trim0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}

func decodeHexBytes(s string) ([]byte, error) {
	return hex.DecodeString(trim0x(s))
}

// retryWithExponentialBackoff executes a function with retry logic using exponential backoff
func retryWithExponentialBackoff(
	operation func() ([]byte, error),
	maxAttempts int,
	initialDelay time.Duration,
	maxDelay time.Duration,
	logger *zap.SugaredLogger,
	operationName string,
) ([]byte, error) {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := operation()
		if err == nil {
			if attempt > 1 {
				logger.Infof("[%s] Succeeded on attempt %d/%d", operationName, attempt, maxAttempts)
			}
			return result, nil
		}

		lastErr = err
		logger.Warnf("[%s] Attempt %d/%d failed: %v", operationName, attempt, maxAttempts, err)

		if attempt == maxAttempts {
			break
		}

		delay := time.Duration(float64(initialDelay) * math.Pow(2, float64(attempt-1)))

		if delay > maxDelay {
			delay = maxDelay
		}

		logger.Infof("[%s] Waiting %v before retry %d/%d", operationName, delay, attempt+1, maxAttempts)
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("[%s] failed after %d attempts, last error: %v", operationName, maxAttempts, lastErr)
}

type ValidationReport struct {
	Valid                bool
	MissingInsertions    []string
	UnexpectedInsertions []string
	MissingUpdates       []string
	UnexpectedUpdates    []string
	MissingDeletions     []string
	UnexpectedDeletions  []string
	Errors               []string
}

// get state from db
func (s *ChainEventProcessorActor) getStateFromDB(
	ref string,
) (map[string]string, error) {
	state := make(map[string]string)

	if ref == "" || ref == config.NullTrieHash {
		return state, nil // empty/genesis state
	}

	prevOracleFile, err := s.db.GetOracleFileByCID(context.Background(), ref)
	if err != nil {
		return nil, err
	}

	prevTrie, err := s.db.GetTrieByOracleFileID(
		context.Background(),
		prevOracleFile.ID,
	)
	if err != nil {
		return nil, err
	}

	stateMapBytes, err := s.db.GetStateMapAtSlot(
		context.Background(),
		uint64(prevTrie.Slot),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to get state map at slot %d: %w",
			prevTrie.Slot,
			err,
		)
	}

	// build state map
	for k, v := range stateMapBytes {
		keyHex := hex.EncodeToString(k[:])
		valueHex := hex.EncodeToString(v[:])
		state[keyHex] = valueHex
	}

	return state, nil
}

// validateTrieOperations compares expected trie operations (derived from data diff)
// with actual trie operations from the payload
func (s *ChainEventProcessorActor) validateTrieOperations(
	oracleData []map[string]interface{},
	previousState map[string]string,
	actualInsertions []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	},
	actualUpdates []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	},
	actualDeletions []struct {
		Key string `json:"key"`
	},
) (*ValidationReport, error) {
	report := &ValidationReport{Valid: true}

	expectedInsertions, expectedUpdates, expectedDeletions := s.diffTrieOpsFromHexState(
		previousState,
		oracleData,
	)

	expectedInsertMap := make(map[string]string)
	for _, ins := range expectedInsertions {
		expectedInsertMap[ins.Key] = ins.Value
	}

	expectedUpdateMap := make(map[string]string)
	for _, upd := range expectedUpdates {
		expectedUpdateMap[upd.Key] = upd.Value
	}

	expectedDeleteSet := make(map[string]struct{})
	for _, del := range expectedDeletions {
		expectedDeleteSet[del.Key] = struct{}{}
	}

	actualInsertMap := make(map[string]string)
	for _, ins := range actualInsertions {
		actualInsertMap[ins.Key] = ins.Value
	}

	actualUpdateMap := make(map[string]string)
	for _, upd := range actualUpdates {
		actualUpdateMap[upd.Key] = upd.Value
	}

	actualDeleteSet := make(map[string]struct{})
	for _, del := range actualDeletions {
		actualDeleteSet[del.Key] = struct{}{}
	}

	// Compare insertions
	for key, value := range expectedInsertMap {
		if actualValue, exists := actualInsertMap[key]; !exists {
			report.MissingInsertions = append(report.MissingInsertions, key)
			report.Valid = false
		} else if actualValue != value {
			report.Errors = append(report.Errors, fmt.Sprintf("Insertion value mismatch for key %s: expected %s, got %s", key, value, actualValue))
			report.Valid = false
		}
	}

	for key := range actualInsertMap {
		if _, exists := expectedInsertMap[key]; !exists {
			report.UnexpectedInsertions = append(
				report.UnexpectedInsertions,
				key,
			)
			report.Valid = false
		}
	}

	// Compare updates
	for key, value := range expectedUpdateMap {
		if actualValue, exists := actualUpdateMap[key]; !exists {
			report.MissingUpdates = append(report.MissingUpdates, key)
			report.Valid = false
		} else if actualValue != value {
			report.Errors = append(report.Errors, fmt.Sprintf("Update value mismatch for key %s: expected %s, got %s", key, value, actualValue))
			report.Valid = false
		}
	}

	for key := range actualUpdateMap {
		if _, exists := expectedUpdateMap[key]; !exists {
			report.UnexpectedUpdates = append(report.UnexpectedUpdates, key)
			report.Valid = false
		}
	}

	// Compare deletions
	for key := range expectedDeleteSet {
		if _, exists := actualDeleteSet[key]; !exists {
			report.MissingDeletions = append(report.MissingDeletions, key)
			report.Valid = false
		}
	}

	for key := range actualDeleteSet {
		if _, exists := expectedDeleteSet[key]; !exists {
			report.UnexpectedDeletions = append(report.UnexpectedDeletions, key)
			report.Valid = false
		}
	}

	return report, nil
}

func (s *ChainEventProcessorActor) withGuard(
	slot uint64,
	label string,
	fn func() error,
) {
	defer func() {
		if r := recover(); r != nil {
			metrics.RecordProcessingError("ChainEventProcessor", label+"Panic")
			s.setProcessingFailed(
				slot,
				fmt.Sprintf("panic in %s: %v", label, r),
			)
		}
	}()
	if err := fn(); err != nil {
		metrics.RecordProcessingError("ChainEventProcessor", label)
		s.setProcessingFailed(slot, fmt.Sprintf("%s error: %v", label, err))
	}
}

// getOrFetchValidatorUTxO fetches/caches the singleton UTxO.
// Note: The global validator UTXO cache is actor-single-threaded and safe within this actor context.
// The shallow copy returned is safe for use within this actor but should not be shared across goroutines.
func (s *ChainEventProcessorActor) getOrFetchValidatorUTxO(
	ctx context.Context,
) (*apolloUTxO.UTxO, error) {
	if cu := GetGlobalValidatorUtxo(); cu != nil {
		s.logger.Debug("Using cached validator UTXO")
		// shallow copy - safe within actor context (single-threaded)
		return &apolloUTxO.UTxO{Input: cu.Input, Output: cu.Output}, nil
	}
	s.logger.Debug("Cached validator UTXO not available, querying provider...")
	singletonPolicyId := s.appCfg.Contract.SingletonPolicyId
	singletonName := s.appCfg.Contract.SingletonName
	singletonId := singletonPolicyId + hex.EncodeToString([]byte(singletonName))

	utxo, err := s.provider.GetUtxoByUnit(ctx, singletonId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch contract utxo: %w", err)
	}
	SetGlobalValidatorUtxo(utxo)
	return utxo, nil
}

// decodes the validator datum from a UTxO into our struct and also returns its CBOR bytes
func (s *ChainEventProcessorActor) decodeValidatorDatumFromUTxO(
	utxo *apolloUTxO.UTxO,
) (*tx.MerkleOracleDatum, []byte, error) {
	validatorDatum := utxo.Output.GetDatum().ToDatum()
	cborBytes, err := gouroborosCbor.Encode(validatorDatum)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to marshal validator datum to CBOR: %w",
			err,
		)
	}
	decoded, err := tx.DecodeMerkleOracleDatum(hex.EncodeToString(cborBytes))
	if err != nil {
		return nil, nil, fmt.Errorf(
			"failed to decode contract utxo datum: %w",
			err,
		)
	}
	return decoded, cborBytes, nil
}

// diffTrieOpsCore returns both bytes (for trie operations) and hex (for payload) versions
// This is the core implementation used by both diffTrieOps and diffTrieOpsFromHexState
func (s *ChainEventProcessorActor) diffTrieOpsCore(
	oracleData []map[string]interface{},
	currentStateMap map[[32]byte][32]byte,
) (insB []kvBytes, updB []kvBytes, delB [][32]byte, insHex []KVHex, updHex []KVHex, delHex []KeyHex) {

	seen := make(map[[32]byte]struct{}, len(currentStateMap))
	insB = make([]kvBytes, 0, len(oracleData))
	updB = make([]kvBytes, 0, len(oracleData))

	for _, item := range oracleData {
		objID, ok := item["object_id"].(string)
		if !ok || objID == "" {
			continue
		}
		for k, v := range item {
			if k == "object_id" {
				continue
			}
			kh := bytes32(s.db.KeyHash(objID, k))
			vh, err := s.db.ValueHash(v)
			if err != nil {
				s.logger.Warnf("marshal value for %s:%s: %v", objID, k, err)
				continue
			}
			vhBytes32 := bytes32(vh)

			if pv, existed := currentStateMap[kh]; !existed {
				insB = append(insB, kvBytes{k: kh, v: vhBytes32})
			} else if pv != vhBytes32 {
				updB = append(updB, kvBytes{k: kh, v: vhBytes32})
				// else unchanged -> no-op
			}
			seen[kh] = struct{}{}
		}
	}

	delB = make([][32]byte, 0, len(currentStateMap))
	for kh := range currentStateMap {
		if _, ok := seen[kh]; !ok {
			delB = append(delB, kh)
		}
	}

	insHex = make([]KVHex, len(insB))
	for i, e := range insB {
		insHex[i] = KVHex{
			Key:   hex.EncodeToString(e.k[:]),
			Value: hex.EncodeToString(e.v[:]),
		}
	}
	updHex = make([]KVHex, len(updB))
	for i, e := range updB {
		updHex[i] = KVHex{
			Key:   hex.EncodeToString(e.k[:]),
			Value: hex.EncodeToString(e.v[:]),
		}
	}
	delHex = make([]KeyHex, len(delB))
	for i, k := range delB {
		delHex[i] = KeyHex{Key: hex.EncodeToString(k[:])}
	}
	return
}

// diffTrieOpsFromHexState computes trie ops between a previous state (hex key->hex value)
// and a current full snapshot (object_id + primitive fields). Only marks update
// if the value hash changed; unchanged keys are no-ops.
func (s *ChainEventProcessorActor) diffTrieOpsFromHexState(
	previousState map[string]string, // key_hash_hex -> value_hash_hex
	oracleData []map[string]interface{},
) (insertions []KVHex, updates []KVHex, deletions []KeyHex) {

	// Convert previousState from hex strings to byte arrays
	prevStateMap := make(map[[32]byte][32]byte, len(previousState))
	for keyHex, valueHex := range previousState {
		keyBytes, err := hex.DecodeString(keyHex)
		if err != nil {
			s.logger.Errorf("failed to decode key hex %s: %v", keyHex, err)
			continue
		}
		valueBytes, err := hex.DecodeString(valueHex)
		if err != nil {
			s.logger.Errorf("failed to decode value hex %s: %v", valueHex, err)
			continue
		}
		prevStateMap[bytes32(keyBytes)] = bytes32(valueBytes)
	}

	// Use the core function
	_, _, _, insertions, updates, deletions = s.diffTrieOpsCore(
		oracleData,
		prevStateMap,
	)
	return
}

// apply ops to trie without hex decoding
func (s *ChainEventProcessorActor) applyTrieOperations(
	mem trieLike,
	ins []kvBytes,
	ups []kvBytes,
	del [][32]byte,
	slot uint64,
) error {
	for _, e := range ins {
		if err := mem.Update(e.k[:], e.v[:], slot); err != nil {
			return fmt.Errorf("trie insert failed: %w", err)
		}
	}
	for _, e := range ups {
		if err := mem.Update(e.k[:], e.v[:], slot); err != nil {
			return fmt.Errorf("trie update failed: %w", err)
		}
	}
	for _, k := range del {
		if err := mem.Delete(k[:]); err != nil {
			return fmt.Errorf("trie delete failed: %w", err)
		}
	}
	return nil
}

type ChainEventProcessorActor struct {
	config    types.StrategyConfig
	appCfg    *config.Config
	logger    *zap.SugaredLogger
	engine    *actor.Engine
	selfPID   *actor.PID
	parentPID *actor.PID // Reference to StrategyManagerActor

	provider           connector.Provider
	cloud              cloud.Cloud
	oracleDataProvider oprovider.Provider
	db                 *database.Database

	//nolint:unused
	discordClient webhook.Client

	startBlockSlot uint64
	justStarted    bool

	processingFailed bool
	failedAtSlot     uint64

	progressBar *progressbar.ProgressBar

	trieRootMismatchCount           int
	lastSuccessfullyIndexedSlot     uint64
	lastSucessfullyIndexedBlockHash string

	processedBlockMap map[BlockKey]struct{}

	blockTransactionCountMap map[BlockKey]uint64
}

func NewChainEventProcessorStrategy(
	cfg types.StrategyConfig,
	appCfg *config.Config,
	provider connector.Provider,
	db *database.Database,
	cloud cloud.Cloud,
	oracleDataProvider oprovider.Provider,
) actor.Producer {
	return func() actor.Receiver {
		return &ChainEventProcessorActor{
			config: cfg,
			appCfg: appCfg,
			logger: logging.GetLogger().
				With("actor", "ChainEventProcessor", "strategy_id", cfg.ID),
			provider:                        provider,
			db:                              db,
			cloud:                           cloud,
			oracleDataProvider:              oracleDataProvider,
			processingFailed:                false,
			startBlockSlot:                  cfg.StartBlockSlot,
			justStarted:                     true,
			failedAtSlot:                    0,
			trieRootMismatchCount:           0,
			lastSuccessfullyIndexedSlot:     cfg.StartBlockSlot,
			lastSucessfullyIndexedBlockHash: cfg.StartBlockHash,
			processedBlockMap:               make(map[BlockKey]struct{}),
			blockTransactionCountMap:        make(map[BlockKey]uint64),
		}
	}
}

// BlockKey represents a composite key for identifying blocks uniquely
// This ensures we can distinguish between different blocks with the same number due to rollbacks
// and enables garbage collection based on slot numbers
type BlockKey struct {
	BlockNumber uint64 `json:"blockNumber"`
	BlockHash   string `json:"blockHash"`
	SlotNumber  uint64 `json:"slotNumber"`
}

// String returns a string representation of the BlockKey for logging purposes
func (bk BlockKey) String() string {
	return fmt.Sprintf("%d:%s:%d", bk.BlockNumber, bk.BlockHash, bk.SlotNumber)
}

// createBlockKey creates a composite key from block number, block hash, and slot number
// This ensures we can distinguish between different blocks with the same number due to rollbacks
func (s *ChainEventProcessorActor) createBlockKey(
	blockNumber uint64,
	blockHash string,
	slotNumber uint64,
) BlockKey {
	return BlockKey{
		BlockNumber: blockNumber,
		BlockHash:   blockHash,
		SlotNumber:  slotNumber,
	}
}

// garbageCollectOldBlocks removes processed blocks with slot numbers less than N
func (s *ChainEventProcessorActor) garbageCollectOldBlocks(cutoffSlot uint64) {
	var removedBlocks []BlockKey
	for blockKey := range s.processedBlockMap {
		if blockKey.SlotNumber < cutoffSlot {
			delete(s.processedBlockMap, blockKey)
			removedBlocks = append(removedBlocks, blockKey)
		}
	}

	if len(removedBlocks) > 0 {
		s.logger.Debugf(
			"Garbage collected %d old processed blocks (cutoff slot: %d)",
			len(removedBlocks),
			cutoffSlot,
		)
		s.logger.Debugf(
			"Remaining processed blocks count: %d",
			len(s.processedBlockMap),
		)
	}
}

func (s *ChainEventProcessorActor) Receive(c *actor.Context) {

	switch msg := c.Message().(type) {
	case actor.Initialized:
		metrics.RecordActorMessage("ChainEventProcessor", "Initialized")
		s.logger.Debug("initializing ChainEvent strategy")
		s.engine = c.Engine()
		s.selfPID = c.PID()

	case actor.Started:
		metrics.RecordActorMessage("ChainEventProcessor", "Started")
		s.logger.Debug("ChainEvent strategy started")

	case actor.Stopped:
		metrics.RecordActorMessage("ChainEventProcessor", "Stopped")
		s.logger.Debug("ChainEvent strategy stopped")

	case types.IndexerBlockEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "BlockEvent")
		s.logger.Debugw("received IndexerBlockEvent",
			"block_number", msg.BlockEvent.Block.BlockNumber(),
			"block_hash", msg.BlockEvent.Block.Hash().String(),
			"slot_number", msg.BlockEvent.Block.SlotNumber(),
			"tip_reached", msg.TipReached)

		blockKey := s.createBlockKey(
			msg.BlockEvent.Block.BlockNumber(),
			msg.BlockEvent.Block.Hash().String(),
			msg.BlockEvent.Block.SlotNumber(),
		)
		s.blockTransactionCountMap[blockKey] = msg.BlockEvent.TransactionCount

		if s.justStarted {
			if msg.BlockEvent.Block.SlotNumber() <= s.startBlockSlot {
				// this means some sort of rollback occured while the program was offline

				s.logger.Infof("detected rollback while program was offline, rolling back to slot %d", s.startBlockSlot)

				// so we first need to rollback any persistant data

				if err := s.db.RollbackCursor(s.startBlockSlot); err != nil {
					s.logger.Errorf("failed to rollback cursor: %v", err)
				}

				if err := s.db.Rollback(s.startBlockSlot); err != nil {
					s.logger.Errorf("failed to rollback: %v", err)
				}

				// then we set the start block slot to the actual start block slot

				s.startBlockSlot = msg.BlockEvent.Block.SlotNumber()

				s.logger.Infof("rolled back to slot %d", s.startBlockSlot)
			}

			s.justStarted = false
		}

		if msg.TipReached {
			s.logger.Debug("tip reached, block #%d", msg.BlockEvent.Block.BlockNumber())
		}

	case types.IndexerTransactionEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "TransactionEvent")
		s.logger.Debugw("received IndexerTransactionEvent",
			"transaction_id", msg.EventTransaction.Transaction.Hash().String(),
			"slot_number", msg.EventContext.SlotNumber,
			"strategy_id", s.config.ID)

		if s.processingFailed {
			s.logger.Warnw("dropping IndexerTransactionEvent due to previous processing failure",
				"slot_number", msg.EventContext.SlotNumber,
				"failed_at_slot", s.failedAtSlot)
			return
		}

		s.withGuard(msg.EventContext.SlotNumber, "TransactionProcessing", func() error {
			if err := s.processTransactionEvent(msg); err != nil {
				return err
			}

			blockKey := s.createBlockKey(
				msg.EventContext.BlockNumber,
				msg.EventTransaction.BlockHash,
				msg.EventContext.SlotNumber,
			)
			if cnt := s.blockTransactionCountMap[blockKey]; cnt <= 1 {
				delete(s.blockTransactionCountMap, blockKey)
				blockHash, _ := hex.DecodeString(msg.EventTransaction.BlockHash)
				if err := s.db.AddCursorPoint(common.Point{
					Hash: blockHash,
					Slot: msg.EventContext.SlotNumber,
				}); err != nil {
					s.logger.Errorf("failed to update cursor: %v", err)
				}
			} else if cnt > 1 {
				s.blockTransactionCountMap[blockKey] = cnt - 1
			} else {
				s.logger.Errorf("block transaction count not found for block %d (hash: %s)", msg.EventContext.BlockNumber, msg.EventTransaction.BlockHash)
			}

			// build after all block transactions are processed to make sure we build with updated trie WRT the utxo being consumed
			if _, exists := s.blockTransactionCountMap[blockKey]; !exists {
				s.logger.Debug("All transactions for block processed, attempting transaction building")
				if err := s.processBlockEvent(msg); err != nil {
					return fmt.Errorf("block processing: %w", err)
				}
				s.lastSuccessfullyIndexedSlot = msg.EventContext.SlotNumber
				s.lastSucessfullyIndexedBlockHash = msg.EventTransaction.BlockHash
			}
			return nil
		})

	case types.IndexerRollbackEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "RollbackEvent")
		s.logger.Warnw("received IndexerRollbackEvent",
			"slot_number", msg.SlotNumber,
			"block_hash", msg.BlockHash)

		if s.processingFailed {
			s.logger.Warnw("dropping IndexerRollbackEvent due to previous processing failure",
				"slot_number", msg.SlotNumber,
				"failed_at_slot", s.failedAtSlot)
			return
		}

		s.withGuard(msg.SlotNumber, "RollbackProcessing", func() error {
			return s.processRollbackEvent(msg)
		})

	case types.IndexerStatusEvent:
		metrics.RecordActorMessage("ChainEventProcessor", "StatusEvent")
		s.logger.Debugw("received IndexerStatusEvent",
			"cursor_slot", msg.CursorSlot,
			"cursor_hash", msg.CursorHash,
			"tip_slot", msg.TipSlot,
			"tip_hash", msg.TipHash,
			"tip_reached", msg.TipReached)

		if !msg.TipReached && msg.TipSlot > s.startBlockSlot {
			max := int64(msg.TipSlot - s.startBlockSlot)
			current := int64(msg.CursorSlot - s.startBlockSlot)
			if s.progressBar == nil {
				if s.startBlockSlot < msg.CursorSlot {
					s.startBlockSlot = msg.CursorSlot
					max = int64(msg.TipSlot - s.startBlockSlot)
					current = int64(msg.CursorSlot - s.startBlockSlot)
				}
				s.logger.Infof("starting progress bar with max %d, tip slot %d, cursor slot %d, start block slot %d", max, msg.TipSlot, msg.CursorSlot, s.startBlockSlot)
				s.progressBar = progressbar.Default(
					max,
					"Indexer sync progress",
				)
			} else {
				s.progressBar.ChangeMax64(max)
			}
			if err := s.progressBar.Set64(current); err != nil {
				s.logger.Warnf("Failed to set progress bar value: %v", err)
			}
		} else if msg.TipReached && s.progressBar != nil {
			if err := s.progressBar.Finish(); err != nil {
				s.logger.Warnf("Failed to finish progress bar: %v", err)
			}
			_ = s.progressBar.Close()
			s.progressBar = nil
		}

	case types.SetParentPID:
		metrics.RecordActorMessage("ChainEventProcessor", "SetParentPID")
		s.logger.Debugw("parent PID set", "pid", msg.PID)
		s.parentPID = msg.PID

	default:
		metrics.RecordActorMessage("ChainEventProcessor", "UnknownMessage")
		s.logger.Warn("received unknown message", "type", fmt.Sprintf("%T", msg))
	}
}

func (s *ChainEventProcessorActor) processBlockEvent(
	event types.IndexerTransactionEvent,
) error {
	blockNumber := event.EventContext.BlockNumber
	blockHash := event.EventTransaction.BlockHash
	slotNumber := event.EventContext.SlotNumber
	blockKey := s.createBlockKey(blockNumber, blockHash, slotNumber)

	if _, exists := s.processedBlockMap[blockKey]; exists {
		s.logger.Infof(
			"Block %s already processed, skipping block",
			blockKey.String(),
		)
		return nil
	}

	s.processedBlockMap[blockKey] = struct{}{}

	s.logger.Debugw("processing block event",
		"block_number", event.EventContext.BlockNumber,
		"block_hash", event.EventTransaction.BlockHash,
		"slot_number", event.EventContext.SlotNumber,
		"tip_reached", event.TipReached,
		"strategy_id", s.config.ID)

	if event.TipReached {
		return s.processBlockTipReached(event)
	}

	return nil
}

func (s *ChainEventProcessorActor) setProcessingFailed(
	slotNumber uint64,
	reason string,
) {
	s.processingFailed = true
	s.failedAtSlot = slotNumber

	s.logger.Errorw(
		"setting processing failed state - all subsequent events will be blocked",
		"failed_at_slot",
		s.failedAtSlot,
		"reason",
		reason,
	)

}

func (s *ChainEventProcessorActor) processBlockTipReached(
	blockEvent types.IndexerTransactionEvent,
) error {
	s.logger.Debug("Processing block tip reached...")

	currentBlockHeight := uint64(blockEvent.EventContext.BlockNumber)

	// Pending tx GC
	removedTxs := CleanupOldPendingTransactions(
		currentBlockHeight,
		s.appCfg.Indexer.TxExpirationBlockNumber,
	)
	if n := len(removedTxs); n > 0 {
		s.logger.Infof("Cleaned up %d old pending transactions", n)
	}
	if c := GetPendingTransactionCount(); c > 0 {
		s.logger.Debugw("Current pending transactions", "count", c)
	}

	currentSlot := uint64(blockEvent.EventContext.SlotNumber)
	s.garbageCollectOldBlocks(currentSlot - uint64(config.FinalitySlotsNeeded))

	memTrie, err := s.db.GetInMemoryTrie()
	if err != nil {
		return fmt.Errorf("get in-memory trie: %w", err)
	}
	trieLatestRoot := memTrie.Hash()

	// Validator UTxO + datum
	validatorOutRef, err := s.getOrFetchValidatorUTxO(context.Background())
	if err != nil {
		return err
	}
	decodedValidatorDatum, _, err := s.decodeValidatorDatumFromUTxO(
		validatorOutRef,
	)
	if err != nil {
		return err
	}

	if GetPendingTransactionCount() == 0 &&
		!bytes.Equal(decodedValidatorDatum.MerkleRoot, trieLatestRoot) {
		s.trieRootMismatchCount++
		metrics.MetricTrieRootMismatches.Inc()
		s.logger.Warnf(
			"Block %d: validator datum trie root mismatch. Got: %x, Want: %x. Mismatch count: %d",
			currentBlockHeight,
			trieLatestRoot,
			decodedValidatorDatum.MerkleRoot,
			s.trieRootMismatchCount,
		)

		if s.trieRootMismatchCount >= 3 {
			s.logger.Errorf(
				"Block %d: validator datum trie root mismatch for 3 consecutive blocks. Got: %x, Want: %x",
				currentBlockHeight,
				decodedValidatorDatum.MerkleRoot,
				trieLatestRoot,
			)
			s.triggerIndexerRestart(
				"trie_root_mismatch",
				s.lastSuccessfullyIndexedSlot,
				s.lastSucessfullyIndexedBlockHash,
			)
			// no need to return error here, as we will restart the indexer
		}

		return nil

	} else {
		s.trieRootMismatchCount = 0
	}

	// respect update cadence unless current root is null (bootstrap)
	blockTimestamp := blockEvent.EventTimestamp
	prevUpdate := time.UnixMilli(decodedValidatorDatum.CreatedAt)
	nextAllowed := prevUpdate.Add(s.appCfg.Oracle.UpdateInterval)
	curRootHex := hex.EncodeToString(decodedValidatorDatum.MerkleRoot)
	if blockTimestamp.Before(nextAllowed) && curRootHex != config.NullTrieHash {
		s.logger.Infof(
			"Block timestamp is before oracle update time (%s), skipping update",
			nextAllowed.Format(time.RFC3339),
		)
		return nil
	}

	oracleData, err := s.oracleDataProvider.Fetch(context.Background(), "10")
	if err != nil {
		return fmt.Errorf("fetch oracle data: %w", err)
	}
	s.logger.Infof("Fetched %d oracle data items", len(oracleData))

	ipfsCidHex := hex.EncodeToString(decodedValidatorDatum.IpfsCid)
	ipfsCidDecoded := tx.DecodeHexIfValid(ipfsCidHex)

	previousFileReference := ""
	if ipfsCidHex != config.NullTrieHash {
		previousFileReference = ipfsCidDecoded
	}

	// normalize oracle data to []map[string]interface{} and ensure object_id
	oracleDataMap := make([]map[string]interface{}, len(oracleData))
	for i, item := range oracleData {
		m := make(map[string]interface{}, len(item))
		for k, v := range item {
			m[k] = v
		}
		if _, ok := m["object_id"]; !ok {
			if drumID, ok := m["drumId"].(string); ok {
				var keyResult database.Key
				err := s.db.DB().
					Joins("join value on key.current_value_hash = value.value_hash").
					Where("value.raw = ?", fmt.Sprintf(`"%s"`, drumID)).
					First(&keyResult).Error
				if err == nil {
					m["object_id"] = keyResult.ObjectID
				} else {
					m["object_id"] = uuid.New().String()
				}
			} else {
				m["object_id"] = uuid.New().String()
			}
			s.logger.Debugf(
				"Generated new object_id for oracle item %d: %s",
				i,
				m["object_id"],
			)
		}
		oracleDataMap[i] = m
	}

	currentStateMap, err := s.db.GetCurrentStateMap(context.Background())
	if err != nil {
		return err
	}

	insB, updB, delB, ins, ups, dels := s.diffTrieOpsCore(
		oracleDataMap,
		currentStateMap,
	)
	s.logger.Infow(
		"Calculated trie operations",
		"insertions",
		len(ins),
		"updates",
		len(ups),
		"deletions",
		len(dels),
	)

	prevMerkleRootHex := hex.EncodeToString(memTrie.Hash())

	uploadPayload := struct {
		Data     []map[string]interface{} `json:"data"`
		TrieData struct {
			Insertions []KVHex  `json:"insertions"`
			Updates    []KVHex  `json:"updates"`
			Deletions  []KeyHex `json:"deletions"`
		} `json:"trieData"`
		CurrentMerkleRoot     string `json:"currentMerkleRoot"`
		PreviousMerkleRoot    string `json:"previousMerkleRoot"`
		PreviousFileReference string `json:"previousFileReference"`
		TrieLibrary           string `json:"trieLibrary"`
		CreatedAt             int64  `json:"createdAt"`
	}{
		Data:                  oracleDataMap,
		PreviousMerkleRoot:    prevMerkleRootHex,
		PreviousFileReference: previousFileReference,
		TrieLibrary:           version.TrieLibrary,
		CreatedAt:             blockEvent.EventTimestamp.Unix(),
	}
	uploadPayload.TrieData.Insertions = ins
	uploadPayload.TrieData.Updates = ups
	uploadPayload.TrieData.Deletions = dels

	if err := s.applyTrieOperations(memTrie, insB, updB, delB, currentSlot); err != nil {
		return err
	}
	updatedTrieRoot := memTrie.Hash()
	uploadPayload.CurrentMerkleRoot = hex.EncodeToString(updatedTrieRoot)
	s.logger.Infof("Updated Trie Root Hash: %x", updatedTrieRoot)

	payloadBytes, err := json.Marshal(uploadPayload)
	if err != nil {
		return fmt.Errorf("marshal upload payload: %w", err)
	}

	s.logger.Infof(
		"Uploading data to cloud, payload size: %d bytes",
		len(payloadBytes),
	)
	cloudRef, err := s.cloud.Upload(payloadBytes)
	if err != nil {
		return fmt.Errorf("upload to cloud: %w", err)
	}
	s.logger.Infof(
		"Successfully uploaded data to cloud with reference: %s",
		string(cloudRef),
	)

	cloudRefBytes := []byte(string(cloudRef))

	txObj, err := tx.BuildRecreateTx(
		s.appCfg, s.provider, validatorOutRef, updatedTrieRoot, cloudRefBytes,
	)
	if err != nil {
		return fmt.Errorf("build tx: %w", err)
	}

	inputs := make([]tx.InputKey, 0, len(txObj.TransactionBody.Inputs))
	inputMap := make(
		map[tx.InputKey]struct{},
		len(txObj.TransactionBody.Inputs),
	)
	for _, in := range txObj.TransactionBody.Inputs {
		ik := tx.InputKey{
			TxId:  hex.EncodeToString(in.TransactionId),
			Index: in.Index,
		}
		inputs = append(inputs, ik)
		inputMap[ik] = struct{}{}
	}

	if hasConflict, conflicting := CheckInputConflicts(inputs...); hasConflict {
		s.logger.Warnw(
			"tx inputs conflict with pending tx; skipping",
			"conflicting_tx", conflicting,
			"potential_inputs", len(inputs),
		)
		return nil
	}

	submit := func() (string, error) {
		txBytes, err := txObj.Bytes()
		if err != nil {
			return "", fmt.Errorf("tx bytes: %w", err)
		}
		return provider.SubmitTx(s.appCfg, s.provider, txBytes)
	}

	txHash, err := submit()
	if err != nil &&
		strings.Contains(err.Error(), "OutsideValidityIntervalUTxO") {
		s.logger.Warnw(
			"Validity interval expired; rebuilding and resubmitting",
			"err",
			err.Error(),
		)

		// Rebuild and replace txObj
		txObj, err = tx.BuildRecreateTx(
			s.appCfg,
			s.provider,
			validatorOutRef,
			updatedTrieRoot,
			cloudRefBytes,
		)
		if err != nil {
			s.logger.Errorf("failed to rebuild tx: %v", err)
			return nil
		}

		inputs = make([]tx.InputKey, 0, len(txObj.TransactionBody.Inputs))
		inputMap = make(
			map[tx.InputKey]struct{},
			len(txObj.TransactionBody.Inputs),
		)
		for _, in := range txObj.TransactionBody.Inputs {
			ik := tx.InputKey{
				TxId:  hex.EncodeToString(in.TransactionId),
				Index: in.Index,
			}
			inputs = append(inputs, ik)
			inputMap[ik] = struct{}{}
		}

		if hasConflict, conflicting := CheckInputConflicts(inputs...); hasConflict {
			s.logger.Warnw(
				"tx inputs conflict with pending tx; skipping",
				"conflicting_tx", conflicting,
				"potential_inputs", len(inputs),
			)
			return nil
		}

		txHash, err = submit()
	}
	if err != nil {
		s.logger.Errorf("failed to submit tx: %v", err)
		return nil
	}

	s.logger.Infof(
		"[Block %d] Transaction submitted %s",
		currentBlockHeight,
		txHash,
	)
	AddPendingTransaction(txHash, inputMap, currentBlockHeight)

	return nil
}

func (s *ChainEventProcessorActor) processTransactionEvent(
	txEvent types.IndexerTransactionEvent,
) error {
	s.logger.Debugf(
		"processing transaction event %s at slot %d, block %d",
		txEvent.EventTransaction.Transaction.Hash().String(),
		txEvent.EventContext.SlotNumber,
		txEvent.EventContext.BlockNumber,
	)

	txHash := txEvent.EventTransaction.Transaction.Hash().String()

	isPending := IsPendingTransaction(txHash)
	if isPending {
		pendingTxInfo := GetPendingTransaction(txHash)
		RemovePendingTransaction(txHash)
		s.logger.Infof(
			"TX [%s] confirmed after %d blocks",
			txHash,
			txEvent.EventContext.BlockNumber-pendingTxInfo.submissionBlockHeight,
		)
	}

	redeemers := txEvent.EventTransaction.Witnesses.Redeemers()

	validTransaction := false
	var decodedDatum *tx.MerkleOracleDatum
	var err error

	// find singleton output, cache UTXO, and capture datum CBOR
	for idx, output := range txEvent.EventTransaction.Outputs {
		assets := output.Assets()
		if assets == nil {
			continue
		}
		for _, policyId := range assets.Policies() {
			if policyId.String() != s.appCfg.Contract.SingletonPolicyId {
				continue
			}
			for _, assetName := range assets.Assets(policyId) {
				if string(assetName) != s.appCfg.Contract.SingletonName {
					continue
				}
				validTransaction = true

				apolloTxIn := apolloTxInput.TransactionInput{
					TransactionId: txEvent.EventTransaction.Transaction.Hash().
						Bytes(),
					Index: idx,
				}
				outputCbor := output.Cbor()
				if outputCbor == nil {
					s.logger.Errorf("failed to get output cbor")
					ClearGlobalValidatorUtxo()
					break
				}
				var apolloTxOut apolloTxOutput.TransactionOutput
				if err := apolloTxOut.UnmarshalCBOR(outputCbor); err != nil {
					s.logger.Errorf(
						"failed to unmarshal output CBOR into Apollo TransactionOutput: %v",
						err,
					)
					ClearGlobalValidatorUtxo()
					break
				}
				apolloUtxo := &apolloUTxO.UTxO{
					Input:  apolloTxIn,
					Output: apolloTxOut,
				}

				SetGlobalValidatorUtxo(apolloUtxo)
				s.logger.Debugw(
					"Updated global validator UTXO cache",
					"tx_hash",
					txEvent.EventTransaction.Transaction.Hash().String(),
				)

				decodedDatum, _, err = s.decodeValidatorDatumFromUTxO(
					apolloUtxo,
				)
				if err != nil {
					return err
				}
				break
			}
			if validTransaction {
				break
			}
		}
		if validTransaction {
			break
		}
	}

	// must have a spend redeemer with RecreateAction
	if validTransaction && redeemers != nil {
		spendIndexes := redeemers.Indexes(0) // SPEND tag = 0
		if len(spendIndexes) == 0 {
			s.logger.Warnf("no spend redeemer found for transaction %v",
				txEvent.EventTransaction.Transaction.Hash().String())
			return nil
		}
		hasRecreateAction := false
		for _, index := range spendIndexes {
			lazyValue := redeemers.Value(index, 0)
			cborBytes := lazyValue.Data.Cbor()
			decodedRedeemer, err := tx.DecodeMerkleOracleRedeemer(cborBytes)
			if err != nil {
				continue
			}
			if decodedRedeemer.Action.String() == (tx.RecreateAction{}).String() {
				hasRecreateAction = true
				break
			}
		}
		if !hasRecreateAction {
			s.logger.Warnf("no recreate action found for transaction %v",
				txEvent.EventTransaction.Transaction.Hash().String())
			return nil
		}

		decodedValidatorDatum := decodedDatum
		trieRootHex := hex.EncodeToString(decodedValidatorDatum.MerkleRoot)
		if trieRootHex == config.NullTrieHash {
			s.logger.Infof("Trie root is null, skipping trie update")
			return nil
		}

		ipfsCidHex := hex.EncodeToString(decodedValidatorDatum.IpfsCid)
		ipfsCidDecoded := tx.DecodeHexIfValid(ipfsCidHex)
		if ipfsCidHex == config.NullTrieHash {
			s.logger.Infof("IPFS CID is null, skipping")
			return nil
		}

		s.logger.Infof(
			"[Process Transaction Event] reading cloud data for ipfs cid: %s",
			ipfsCidDecoded,
		)

		cloudReadOperation := func() ([]byte, error) {
			return s.cloud.Read(cloud.Ref(ipfsCidDecoded))
		}

		cloudData, err := retryWithExponentialBackoff(
			cloudReadOperation,
			s.appCfg.Cloud.RetryMaxAttempts,
			s.appCfg.Cloud.RetryInitialDelay,
			s.appCfg.Cloud.RetryMaxDelay,
			s.logger,
			"CloudRead",
		)
		if err != nil {
			return fmt.Errorf("failed to read cloud data: %v", err)
		}

		var payload struct {
			Data     []map[string]interface{} `json:"data"`
			TrieData struct {
				Insertions []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"insertions"`
				Updates []struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				} `json:"updates"`
				Deletions []struct {
					Key string `json:"key"`
				} `json:"deletions"`
			} `json:"trieData"`
			CurrentMerkleRoot     string `json:"currentMerkleRoot"`
			PreviousMerkleRoot    string `json:"previousMerkleRoot"`
			PreviousFileReference string `json:"previousFileReference"`
			TrieLibrary           string `json:"trieLibrary"`
			CreatedAt             int64  `json:"createdAt"`
		}

		if err := json.Unmarshal(cloudData, &payload); err != nil {
			return fmt.Errorf("failed to decode cloud data JSON: %v", err)
		}

		previousState, err := s.getStateFromDB(payload.PreviousFileReference)
		if err != nil {
			return fmt.Errorf("failed to get previous state from DB: %v", err)
		}

		validationReport, err := s.validateTrieOperations(
			payload.Data,
			previousState,
			payload.TrieData.Insertions,
			payload.TrieData.Updates,
			payload.TrieData.Deletions,
		)
		if err != nil {
			s.logger.Warnw("Failed to validate trie operations", "error", err)
		} else if !validationReport.Valid {
			s.logger.Warnw("Trie operations validation failed",
				"missing_insertions", len(validationReport.MissingInsertions),
				"unexpected_insertions", len(validationReport.UnexpectedInsertions),
				"missing_updates", len(validationReport.MissingUpdates),
				"unexpected_updates", len(validationReport.UnexpectedUpdates),
				"missing_deletions", len(validationReport.MissingDeletions),
				"unexpected_deletions", len(validationReport.UnexpectedDeletions),
				"errors", len(validationReport.Errors),
			)

			if len(validationReport.MissingInsertions) > 0 {
				s.logger.Debugw("Missing insertions", "keys", validationReport.MissingInsertions)
			}
			if len(validationReport.UnexpectedInsertions) > 0 {
				s.logger.Debugw("Unexpected insertions", "keys", validationReport.UnexpectedInsertions)
			}
			if len(validationReport.MissingUpdates) > 0 {
				s.logger.Debugw("Missing updates", "keys", validationReport.MissingUpdates)
			}
			if len(validationReport.UnexpectedUpdates) > 0 {
				s.logger.Debugw("Unexpected updates", "keys", validationReport.UnexpectedUpdates)
			}
			if len(validationReport.MissingDeletions) > 0 {
				s.logger.Debugw("Missing deletions", "keys", validationReport.MissingDeletions)
			}
			if len(validationReport.UnexpectedDeletions) > 0 {
				s.logger.Debugw("Unexpected deletions", "keys", validationReport.UnexpectedDeletions)
			}
			if len(validationReport.Errors) > 0 {
				s.logger.Debugw("Validation errors", "errors", validationReport.Errors)
			}

		} else {
			s.logger.Infow("Trie operations validation passed successfully")
		}

		// build map of key_hash -> (object_id, raw_key, raw_value) from the full snapshot `data`
		type rawTriple struct {
			objID string
			key   string
			val   json.RawMessage
		}
		keyHashToRaw := make(map[[32]byte]rawTriple)
		for _, dataItem := range payload.Data {
			// Expect "object_id" to be present in each object
			objID, _ := dataItem["object_id"].(string)
			if objID == "" {
				// If your schema uses another field name, adapt here.
				s.logger.Errorf("missing object_id in data item: %v", dataItem)
				continue
			}
			for k, v := range dataItem {
				if k == "object_id" {
					continue
				}
				// Compute key hash = blake2b256(object_id + ":" + raw_key)
				kh := bytes32(s.db.KeyHash(objID, k))

				// Canonicalize raw value to JSON for consistency
				marshaled, err := json.Marshal(v)
				if err != nil {
					s.logger.Warnf(
						"failed to marshal value for key %s: %v",
						k,
						err,
					)
					continue
				}
				valBytes, err := database.CanonicalizeJSON(
					json.RawMessage(marshaled),
				)
				if err != nil {
					s.logger.Warnf(
						"failed to marshal value for key %s: %v",
						k,
						err,
					)
					continue
				}
				keyHashToRaw[kh] = rawTriple{
					objID: objID,
					key:   k,
					val:   json.RawMessage(valBytes),
				}
			}
		}

		// Build Entries from trieData (or fallback to full snapshot for genesis)
		var entries []database.Entry
		seqInsert, seqUpdate, seqDelete := uint32(0), uint32(0), uint32(0)

		isGenesis := payload.PreviousMerkleRoot == config.NullTrieHash ||
			payload.PreviousMerkleRoot == ""
		if isGenesis {
			// Treat all keys in snapshot as inserts
			for _, t := range keyHashToRaw {
				entries = append(entries, database.Entry{
					ObjectID:      t.objID,
					RawKey:        t.key,
					RawValue:      t.val,
					OperationType: database.OpInsert,
					SequenceOrder: seqInsert,
				})
				seqInsert++
			}
		} else {
			// Insertions
			for _, ins := range payload.TrieData.Insertions {
				khBytes, err := decodeHexBytes(ins.Key)
				if err != nil || len(khBytes) != 32 {
					s.logger.Warnf("bad insertion key hash: %s", ins.Key)
					continue
				}
				var kh [32]byte
				copy(kh[:], khBytes)

				triple, ok := keyHashToRaw[kh]
				if !ok {
					// If not in current snapshot (shouldn't happen for insert), skip
					s.logger.Warnf("insertion key not found in current snapshot map: %s", ins.Key)
					continue
				}
				entries = append(entries, database.Entry{
					ObjectID:      triple.objID,
					RawKey:        triple.key,
					RawValue:      triple.val,
					OperationType: database.OpInsert,
					SequenceOrder: seqInsert,
				})
				seqInsert++
			}

			// Updates
			for _, up := range payload.TrieData.Updates {
				khBytes, err := decodeHexBytes(up.Key)
				if err != nil || len(khBytes) != 32 {
					s.logger.Warnf("bad update key hash: %s", up.Key)
					continue
				}
				var kh [32]byte
				copy(kh[:], khBytes)

				triple, ok := keyHashToRaw[kh]
				if !ok {
					// Updated key must exist in current snapshot
					s.logger.Warnf("update key not found in current snapshot map: %s", up.Key)
					continue
				}
				entries = append(entries, database.Entry{
					ObjectID:      triple.objID,
					RawKey:        triple.key,
					RawValue:      triple.val,
					OperationType: database.OpUpdate,
					SequenceOrder: seqUpdate,
				})
				seqUpdate++
			}

			// Deletions
			for _, del := range payload.TrieData.Deletions {
				khBytes, err := decodeHexBytes(del.Key)
				if err != nil || len(khBytes) != 32 {
					s.logger.Warnf("bad delete key hash: %s", del.Key)
					continue
				}
				var kh [32]byte
				copy(kh[:], khBytes)

				// For deletes, the key won't be in current snapshot; look it up in DB to recover (object_id, raw_key)
				var krow database.Key
				if err := s.db.DB().Where("key_hash = ?", kh[:]).First(&krow).Error; err != nil {
					s.logger.Warnf("delete key not found in DB (skipping): %s (err=%v)", del.Key, err)
					continue
				}
				entries = append(entries, database.Entry{
					ObjectID:      krow.ObjectID,
					RawKey:        krow.RawKey,
					RawValue:      nil, // ignored by ApplyOracleFile for delete
					OperationType: database.OpDelete,
					SequenceOrder: seqDelete,
				})
				seqDelete++
			}
		}

		var prevCIDPtr *string
		if payload.PreviousFileReference != "" &&
			payload.PreviousFileReference != config.NullTrieHash {
			pc := payload.PreviousFileReference
			prevCIDPtr = &pc
		}
		var prevRootPtr *string
		if payload.PreviousMerkleRoot != "" &&
			payload.PreviousMerkleRoot != config.NullTrieHash {
			pr := payload.PreviousMerkleRoot
			prevRootPtr = &pr
		}
		trieLib := payload.TrieLibrary
		params := database.ApplyBatchParams{
			CID:                   ipfsCidDecoded,
			PreviousCID:           prevCIDPtr,
			TrieLibrary:           &trieLib,
			CurrentMerkleRoot:     payload.CurrentMerkleRoot,
			PreviousMerkleRoot:    prevRootPtr,
			BlockchainConfirmedAt: &txEvent.EventTimestamp,
			Slot:                  int64(txEvent.EventContext.SlotNumber),
			TxID:                  txHash,
			TxFee: uint32(
				txEvent.EventTransaction.Transaction.Fee(),
			),
			Entries: entries,
		}

		if _, _, err := s.db.ApplyOracleFile(context.Background(), params); err != nil {
			return fmt.Errorf("apply oracle file failed: %v", err)
		}

		s.logger.Infof("Block %d: New Trie Root Hash: %x",
			txEvent.EventContext.BlockNumber, s.db.GetTrieHash())
	}

	return nil
}

func (s *ChainEventProcessorActor) processRollbackEvent(
	rollbackEvent types.IndexerRollbackEvent,
) error {
	s.logger.Warnw("processing rollback event",
		"slot_number", rollbackEvent.SlotNumber,
		"block_hash", rollbackEvent.BlockHash,
		"strategy_id", s.config.ID)

	originalRoot := s.db.GetTrieHash()

	s.logger.Infof("Initiating rollback to slot %d (block %s)",
		rollbackEvent.SlotNumber, rollbackEvent.BlockHash)

	if err := s.db.RollbackCursor(rollbackEvent.SlotNumber); err != nil {
		s.logger.Errorf("Failed to rollback cursor to slot %d: %s",
			rollbackEvent.SlotNumber, err)
		return err
	}

	if err := s.db.Rollback(rollbackEvent.SlotNumber); err != nil {
		s.logger.Errorf("Failed to rollback to slot %d: %s",
			rollbackEvent.SlotNumber, err)
		return err
	}

	rolledBackRoot := s.db.GetTrieHash()

	if !bytes.Equal(originalRoot, rolledBackRoot) {
		s.logger.Infof(
			"Trie state changed after rollback to slot %d (block %s)",
			rollbackEvent.SlotNumber,
			rollbackEvent.BlockHash,
		)
		s.logger.Infof(
			"Trie root changed: %x -> %x",
			originalRoot,
			rolledBackRoot,
		)
	} else {
		s.logger.Debugf("Trie state unchanged after rollback (root: %x)", originalRoot)
	}

	var deletedBlocks []BlockKey
	for blockKey := range s.processedBlockMap {
		// Check if block slot number is greater than rollback slot number
		if blockKey.SlotNumber > rollbackEvent.SlotNumber {
			delete(s.processedBlockMap, blockKey)
			deletedBlocks = append(deletedBlocks, blockKey)
		}
	}

	var deletedTransactionCountEntries []BlockKey
	for blockKey := range s.blockTransactionCountMap {
		if blockKey.SlotNumber > rollbackEvent.SlotNumber {
			delete(s.blockTransactionCountMap, blockKey)
			deletedTransactionCountEntries = append(
				deletedTransactionCountEntries,
				blockKey,
			)
		}
	}

	if len(deletedBlocks) > 0 {
		s.logger.Debugf(
			"Removed %d processed block entries for blocks with slot > %d",
			len(deletedBlocks),
			rollbackEvent.SlotNumber,
		)
		for _, deletedBlock := range deletedBlocks {
			s.logger.Debugf("Removed block: %s", deletedBlock.String())
		}
	}

	if len(deletedTransactionCountEntries) > 0 {
		s.logger.Debugf(
			"Removed %d block transaction count entries for blocks with slot > %d",
			len(deletedTransactionCountEntries),
			rollbackEvent.SlotNumber,
		)
	}

	return nil
}

func (s *ChainEventProcessorActor) triggerIndexerRestart(
	reason string,
	restartSlot uint64,
	restartHash string,
) {
	s.logger.Warnw(
		"triggering indexer restart",
		"reason",
		reason,
		"restart_slot",
		restartSlot,
		"restart_hash",
		restartHash,
	)

	restartRequest := types.IndexerRestartRequest{
		Reason:      reason,
		StrategyPID: s.selfPID, // Include our PID so the manager can restart us
		RestartSlot: restartSlot,
		RestartHash: restartHash,
	}

	if s.engine != nil && s.parentPID != nil {
		s.engine.Send(s.parentPID, restartRequest)
		s.logger.Infow(
			"indexer restart request sent to strategy manager",
			"reason",
			reason,
			"strategy_pid",
			s.selfPID.String(),
			"restart_slot",
			restartSlot,
			"restart_hash",
			restartHash,
		)
	} else {
		s.logger.Error("cannot trigger indexer restart: engine or parent PID not available")
	}
}
