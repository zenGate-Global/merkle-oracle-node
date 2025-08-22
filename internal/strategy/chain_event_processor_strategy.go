package strategy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
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

	"github.com/anthdm/hollywood/actor"
	"github.com/blinklabs-io/gouroboros/protocol/common"
	"github.com/disgoorg/disgo/webhook"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	connector "github.com/zenGate-Global/cardano-connector-go"
	"go.uber.org/zap"
	"golang.org/x/crypto/blake2b"

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

func trim0x(s string) string {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		return s[2:]
	}
	return s
}

func decodeHexBytes(s string) ([]byte, error) {
	return hex.DecodeString(trim0x(s))
}

func (s *ChainEventProcessorActor) keyHashHex(objID, key string) string {
	return hex.EncodeToString(s.db.Hash([]byte(objID + ":" + key)))
}

func (s *ChainEventProcessorActor) valueHashHex(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(s.db.Hash(b)), nil
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

// getOrFetchValidatorUTxO fetches/caches the singleton UTxO
func (s *ChainEventProcessorActor) getOrFetchValidatorUTxO(
	ctx context.Context,
) (*apolloUTxO.UTxO, error) {
	if cu := GetGlobalValidatorUtxo(); cu != nil {
		s.logger.Debug("Using cached validator UTXO")
		// shallow copy
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

// Computes trie diffs (insert/update/delete) between oracleData and currentCloudData
func (s *ChainEventProcessorActor) diffTrieOps(
	mem trieLike,
	oracleData, currentCloudData []map[string]interface{},
) (insertions []KVHex, updates []KVHex, deletions []KeyHex) {

	// get current keys from cloud snapshot
	currentKeys := make(map[string]bool)
	for _, item := range currentCloudData {
		objID, ok := item["object_id"].(string)
		if !ok || objID == "" {
			continue
		}
		for k := range item {
			if k == "object_id" {
				continue
			}
			keyHex := s.keyHashHex(objID, k)
			currentKeys[keyHex] = true
		}
	}

	oracleKeys := make(map[string]bool)

	// decide insert vs update using in-memory trie
	for _, item := range oracleData {
		objID, ok := item["object_id"].(string)
		if !ok || objID == "" {
			continue
		}
		for k, v := range item {
			if k == "object_id" {
				continue
			}
			keyHash := s.db.Hash([]byte(objID + ":" + k))
			keyHex := hex.EncodeToString(keyHash)
			valHex, err := s.valueHashHex(v)
			if err != nil {
				s.logger.Errorf(
					"failed to hash value for %s:%s: %v",
					objID,
					k,
					err,
				)
				continue
			}
			oracleKeys[keyHex] = true
			if mem.Has(keyHash) {
				updates = append(updates, KVHex{Key: keyHex, Value: valHex})
			} else {
				insertions = append(insertions, KVHex{Key: keyHex, Value: valHex})
			}
		}
	}

	// missing keys means deletions
	for keyHex := range currentKeys {
		if !oracleKeys[keyHex] {
			deletions = append(deletions, KeyHex{Key: keyHex})
		}
	}

	return
}

// apply the hex-encoded trie ops to the in-memory trie in a single place
func (s *ChainEventProcessorActor) applyTrieOperations(
	mem trieLike,
	insertions []KVHex,
	updates []KVHex,
	deletions []KeyHex,
	slot uint64,
) error {
	for _, in := range insertions {
		kb, err := decodeHexBytes(in.Key)
		if err != nil {
			return fmt.Errorf("decode insertion key %s: %w", in.Key, err)
		}
		vb, err := decodeHexBytes(in.Value)
		if err != nil {
			return fmt.Errorf("decode insertion value %s: %w", in.Value, err)
		}
		s.logger.Debugw(
			"TRIE_DEBUG(BUILD): trie insert",
			"key",
			in.Key,
			"value",
			in.Value,
		)
		if err := mem.Update(kb, vb, slot); err != nil {
			return fmt.Errorf("trie insert failed: %w", err)
		}
	}
	for _, up := range updates {
		kb, err := decodeHexBytes(up.Key)
		if err != nil {
			return fmt.Errorf("decode update key %s: %w", up.Key, err)
		}
		vb, err := decodeHexBytes(up.Value)
		if err != nil {
			return fmt.Errorf("decode update value %s: %w", up.Value, err)
		}
		s.logger.Debugw(
			"TRIE_DEBUG(BUILD): trie update",
			"key",
			up.Key,
			"value",
			up.Value,
		)
		if err := mem.Update(kb, vb, slot); err != nil {
			return fmt.Errorf("trie update failed: %w", err)
		}
	}
	for _, del := range deletions {
		kb, err := decodeHexBytes(del.Key)
		if err != nil {
			return fmt.Errorf("decode deletion key %s: %w", del.Key, err)
		}
		s.logger.Debugw("TRIE_DEBUG(BUILD): trie delete", "key", del.Key)
		if err := mem.Delete(kb); err != nil {
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
	lastSucessfullyIndexedSlot      uint64
	lastSucessfullyIndexedBlockHash string

	processedBlockMap map[string]bool

	blockTransactionCountMap map[uint64]uint64
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
			lastSucessfullyIndexedSlot:      cfg.StartBlockSlot,
			lastSucessfullyIndexedBlockHash: cfg.StartBlockHash,
			processedBlockMap:               make(map[string]bool),
			blockTransactionCountMap:        make(map[uint64]uint64),
		}
	}
}

// createBlockKey creates a composite key from block number and block hash
// This ensures we can distinguish between different blocks with the same number due to rollbacks
func (s *ChainEventProcessorActor) createBlockKey(
	blockNumber uint64,
	blockHash string,
) string {
	return fmt.Sprintf("%d:%s", blockNumber, blockHash)
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

		s.blockTransactionCountMap[msg.BlockEvent.Block.BlockNumber()] = msg.BlockEvent.TransactionCount

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
			// garbage collect around 1% of the time
			if rand.Intn(100) == 0 {
				s.logger.Info("garbage collecting accounts")
				//TODO: implement gc for trie
				if err := s.db.GarbageCollectTrie(msg.BlockEvent.Block.SlotNumber() - 129600); err != nil {
					s.logger.Errorf("failed to garbage collect accounts: %v", err)
				}
				s.logger.Info("garbage collection complete")
			}
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

			blockTransactionCount, exists := s.blockTransactionCountMap[msg.EventContext.BlockNumber]
			if exists {
				s.blockTransactionCountMap[msg.EventContext.BlockNumber] = blockTransactionCount - 1
				if blockTransactionCount-1 == 0 {
					blockHash, _ := hex.DecodeString(msg.EventTransaction.BlockHash)
					if err := s.db.AddCursorPoint(common.Point{
						Hash: blockHash,
						Slot: msg.EventContext.SlotNumber,
					}); err != nil {
						s.logger.Errorf("failed to update cursor: %v", err)
					}
				}
			} else {
				s.logger.Errorf("block transaction count not found for block %d", msg.EventContext.BlockNumber)
			}

			// build after all block transactinos are processed to make sure we build with updated trie WRT the utxo being consumed
			if s.blockTransactionCountMap[msg.EventContext.BlockNumber] == 0 {
				s.logger.Debug("All transactions for block processed, attempting transaction building")
				if err := s.processBlockEvent(msg); err != nil {
					return fmt.Errorf("block processing: %w", err)
				}
				s.lastSucessfullyIndexedSlot = msg.EventContext.BlockNumber
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
	blockKey := s.createBlockKey(blockNumber, blockHash)

	if _, exists := s.processedBlockMap[blockKey]; exists {
		s.logger.Infof(
			"Block %d:%s already processed, skipping block",
			blockNumber,
			blockHash,
		)
		return nil
	}

	s.processedBlockMap[blockKey] = true

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
				s.lastSucessfullyIndexedSlot,
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

	var currentCloudData []map[string]interface{}
	// TODO: get previous file reference from DB
	previousFileReference := ""
	if ipfsCidHex != config.NullTrieHash {
		s.logger.Infof(
			"Reading current cloud data from IPFS CID: %s",
			ipfsCidDecoded,
		)
		if cloudData, readErr := s.cloud.Read(cloud.Ref(ipfsCidDecoded)); readErr != nil {
			s.logger.Warnf("Failed to read current cloud data: %v", readErr)
		} else {
			var payload struct {
				Data                  []map[string]interface{} `json:"data"`
				CurrentMerkleRoot     string                   `json:"currentMerkleRoot"`
				PreviousFileReference string                   `json:"previousFileReference"`
			}
			if err := json.Unmarshal(cloudData, &payload); err != nil {
				s.logger.Warnf("Failed to decode current cloud data JSON: %v", err)
			} else {
				currentCloudData = payload.Data
				previousFileReference = ipfsCidDecoded
				s.logger.Infof("Loaded %d items from current cloud data", len(currentCloudData))
			}
		}
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

	ins, ups, dels := s.diffTrieOps(memTrie, oracleDataMap, currentCloudData)
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
		TrieLibrary:           "merkle-oracle-node",
		CreatedAt:             blockEvent.EventTimestamp.Unix(),
	}
	uploadPayload.TrieData.Insertions = ins
	uploadPayload.TrieData.Updates = ups
	uploadPayload.TrieData.Deletions = dels

	// apply ops to local trie
	currentSlot := uint64(blockEvent.EventContext.SlotNumber)
	if err := s.applyTrieOperations(memTrie, ins, ups, dels, currentSlot); err != nil {
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

	submit := func() (string, error) {
		txBytes, _ := txObj.Bytes()

		if hasConflict, conflicting := CheckInputConflicts(inputs...); hasConflict {
			return "", fmt.Errorf(
				"tx inputs conflict with pending tx: %s",
				conflicting,
			)
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
			return fmt.Errorf("rebuild tx: %w", err)
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

		txHash, err = submit()
	}
	if err != nil {
		return fmt.Errorf("submit tx: %w", err)
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
		cloudData, err := s.cloud.Read(cloud.Ref(ipfsCidDecoded))
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
				kh := blake2b.Sum256([]byte(objID + ":" + k))

				// Marshal raw value back to JSON (primitives only)
				valBytes, err := json.Marshal(v)
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
			Entries:               entries,
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

	var deletedBlocks []string
	for blockKey := range s.processedBlockMap {
		// extract block number from composite key (format: "blockNumber:blockHash")
		if colonIndex := strings.Index(blockKey, ":"); colonIndex != -1 {
			if blockNumberStr := blockKey[:colonIndex]; blockNumberStr != "" {
				if blockNumber, err := strconv.ParseUint(blockNumberStr, 10, 64); err == nil {
					if blockNumber > rollbackEvent.SlotNumber {
						delete(s.processedBlockMap, blockKey)
						deletedBlocks = append(deletedBlocks, blockKey)
					}
				}
			}
		}
	}

	if len(deletedBlocks) > 0 {
		s.logger.Debugf("Removed %d processed block entries for blocks > %d",
			len(deletedBlocks), rollbackEvent.SlotNumber)
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
