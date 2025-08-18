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
				if err := s.db.GarbageCollectTrie(msg.BlockEvent.Block.SlotNumber() - 129600); err != nil {
					s.logger.Errorf("failed to garbage collect accounts: %v", err)
				}
				s.logger.Info("garbage collection complete")
			}
		}

		// if err := s.processBlockEvent(msg); err != nil {
		// 	metrics.RecordProcessingError("ChainEventProcessor", "BlockProcessing")
		// 	s.logger.Errorf("error processing block event: %v", err)
		// } else {
		// 	metrics.MetricEmissionsEventsProcessed.Inc()
		// 	s.lastSucessfullyIndexedSlot = msg.BlockEvent.Block.SlotNumber()
		// 	s.lastSucessfullyIndexedBlockHash = msg.BlockEvent.Block.Hash().String()
		// }

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

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "TransactionPanic")
					s.setProcessingFailed(msg.EventContext.SlotNumber, fmt.Sprintf("panic in processTransactionEvent: %v", r))
				}
			}()

			if err := s.processTransactionEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "TransactionProcessing")
				s.setProcessingFailed(msg.EventContext.SlotNumber, fmt.Sprintf("error in processTransactionEvent: %v", err))
			} else {
				// metrics.MetricEmissionsEventsProcessed.Inc()

				blockTransactionCount, exists := s.blockTransactionCountMap[msg.EventContext.BlockNumber]
				if exists {
					// decr tx count to track when all txns are processed
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
					// this should never happen
					s.logger.Errorf("block transaction count not found for block %d", msg.EventContext.BlockNumber)
				}
			}
		}()

		// once all txns are processed, we know merkle trie is caught up
		// txns can now be built without issue

		if s.blockTransactionCountMap[msg.EventContext.BlockNumber] == 0 {
			s.logger.Debug("All transactions for block processed, attempting transaction building")
			if err := s.processBlockEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "BlockProcessing")
				s.logger.Errorf("error processing block event: %v", err)
			} else {
				// metrics.MetricEventsProcessed.Inc()
				s.lastSucessfullyIndexedSlot = msg.EventContext.BlockNumber
				s.lastSucessfullyIndexedBlockHash = msg.EventTransaction.BlockHash
			}
		}

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

		func() {
			defer func() {
				if r := recover(); r != nil {
					metrics.RecordProcessingError("ChainEventProcessor", "RollbackPanic")
					s.setProcessingFailed(msg.SlotNumber, fmt.Sprintf("panic in processRollbackEvent: %v", r))
				}
			}()

			if err := s.processRollbackEvent(msg); err != nil {
				metrics.RecordProcessingError("ChainEventProcessor", "RollbackProcessing")
				s.setProcessingFailed(msg.SlotNumber, fmt.Sprintf("error in processRollbackEvent: %v", err))
			}
		}()

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
	removedTxs := CleanupOldPendingTransactions(
		currentBlockHeight,
		s.appCfg.Indexer.TxExpirationBlockNumber,
	)
	removedTxnsCount := len(removedTxs)
	if removedTxnsCount > 0 {
		s.logger.Infof(
			"Cleaned up %d old pending transactions",
			removedTxnsCount,
		)
	}

	pendingCount := GetPendingTransactionCount()
	if pendingCount > 0 {
		s.logger.Debugw("Current pending transactions", "count", pendingCount)
	}

	memTrie, err := s.db.GetInMemoryTrie()
	if err != nil {
		return fmt.Errorf("failed to get in-memory trie: %w", err)
	}

	trieLatestRoot := memTrie.Hash()

	singletonPolicyId := s.appCfg.Contract.SingletonPolicyId
	singletonName := s.appCfg.Contract.SingletonName
	singletonId := singletonPolicyId + hex.EncodeToString([]byte(singletonName))

	// Try to use cached validator UTXO first
	var validatorOutRef *apolloUTxO.UTxO
	cachedValidatorUtxo := GetGlobalValidatorUtxo()

	if cachedValidatorUtxo != nil {
		s.logger.Debugf("Using cached validator UTXO")
		validatorOutRef = &apolloUTxO.UTxO{
			Input:  cachedValidatorUtxo.Input,
			Output: cachedValidatorUtxo.Output,
		}
	} else {
		s.logger.Debugf("Cached validator UTXO not available, querying...")

		var err error

		validatorOutRef, err = s.provider.GetUtxoByUnit(context.TODO(), singletonId)
		if err != nil {
			return fmt.Errorf("failed to fetch contract utxo: %v", err)
		}
	}

	validatorDatum := validatorOutRef.Output.GetDatum().ToDatum()
	decodedValidatorDatumCbor, err := gouroborosCbor.Encode(validatorDatum)
	if err != nil {
		return fmt.Errorf("failed to marshal validator datum to CBOR: %v", err)
	}

	decodedValidatorDatum, err := tx.DecodeMerkleOracleDatum(
		hex.EncodeToString(decodedValidatorDatumCbor),
	)
	if err != nil {
		return fmt.Errorf("error decoding contract utxo datum: %v", err)
	}

	if pendingCount == 0 &&
		!bytes.Equal(decodedValidatorDatum.MerkleRoot, trieLatestRoot) {
		s.trieRootMismatchCount++
		// Record trie root mismatch in metrics
		metrics.MetricTrieRootMismatches.Inc()
		s.logger.Warnf(
			"Block %d: validator datum trie root mismatch. Got: %x, Want: %x. Mismatch count: %d",
			currentBlockHeight,
			trieLatestRoot,
			decodedValidatorDatum.MerkleRoot,
			s.trieRootMismatchCount,
		)

		// if s.trieRootMismatchCount >= 3 {
		// 	s.logger.Errorf(
		// 		"Block %d: validator datum trie root mismatch for 3 consecutive blocks. Got: %x, Want: %x",
		// 		currentBlockHeight,
		// 		decodedValidatorDatum.MerkleRoot,
		// 		trieLatestRoot,
		// 	)
		// 	s.triggerIndexerRestart(
		// 		"trie_root_mismatch",
		// 		s.lastSucessfullyIndexedSlot,
		// 		s.lastSucessfullyIndexedBlockHash,
		// 	)
		// 	// no need to return error here, as we will restart the indexer
		// 	return nil
		// }
	} else {
		s.trieRootMismatchCount = 0
	}

	blockTimestamp := blockEvent.EventTimestamp
	s.logger.Infof("Block timestamp: %s", blockTimestamp.Format(time.RFC3339))
	oracleUpdateInterval := s.appCfg.Oracle.UpdateInterval

	previousUpdateTime := time.UnixMilli(decodedValidatorDatum.CreatedAt)
	s.logger.Infof(
		"Previous update time: %s",
		previousUpdateTime.Format(time.RFC3339),
	)

	oracleUpdateTime := previousUpdateTime.Add(oracleUpdateInterval)
	s.logger.Infof(
		"Oracle update time: %s",
		oracleUpdateTime.Format(time.RFC3339),
	)

	currentMerkleRoot := hex.EncodeToString(decodedValidatorDatum.MerkleRoot)

	// if its null then we can ignore the update after constraint
	if blockTimestamp.Before(oracleUpdateTime) &&
		currentMerkleRoot != config.NullTrieHash {
		s.logger.Infof(
			"Block timestamp is before oracle update time, skipping update",
		)
		return nil
	}

	oracleData, err := s.oracleDataProvider.Fetch(context.Background(), "10")
	if err != nil {
		return fmt.Errorf("failed to fetch oracle data: %v", err)
	}

	s.logger.Infof("Fetched %d oracle data items", len(oracleData))

	// Get current cloud data using IPFS CID from validator datum
	ipfsCidHex := hex.EncodeToString(decodedValidatorDatum.IpfsCid)
	ipfsCidDecoded := tx.DecodeHexIfValid(ipfsCidHex)

	var currentCloudData []map[string]interface{}
	// TODO: get previous file reference from DB
	var previousFileReference string

	if ipfsCidHex != config.NullTrieHash {
		s.logger.Infof(
			"Reading current cloud data from IPFS CID: %s",
			ipfsCidDecoded,
		)
		cloudData, err := s.cloud.Read(cloud.Ref(ipfsCidDecoded))
		if err != nil {
			s.logger.Warnf("Failed to read current cloud data: %v", err)
			// Continue with empty current data if we can't read existing
		} else {
			var currentPayload struct {
				Data                  []map[string]interface{} `json:"data"`
				CurrentMerkleRoot     string                   `json:"currentMerkleRoot"`
				PreviousFileReference string                   `json:"previousFileReference"`
			}

			if err := json.Unmarshal(cloudData, &currentPayload); err != nil {
				s.logger.Warnf("Failed to decode current cloud data JSON: %v", err)
			} else {
				currentCloudData = currentPayload.Data
				ipfsCidDecoded := tx.DecodeHexIfValid(ipfsCidHex)
				previousFileReference = ipfsCidDecoded
				s.logger.Infof("Loaded %d items from current cloud data", len(currentCloudData))
			}
		}
	}

	// Convert oracle data to same format as cloud data
	oracleDataMap := make([]map[string]interface{}, len(oracleData))
	for i, item := range oracleData {
		// Convert oprovider.Item (map[string]string) to map[string]interface{}
		convertedItem := make(map[string]interface{}, len(item))
		for k, v := range item {
			convertedItem[k] = v
		}

		// Ensure every new object has an object_id property with a UUID
		// Existing objects are assumed to already have it
		if _, hasObjectID := convertedItem["object_id"]; !hasObjectID {
			if drumID, ok := convertedItem["drumId"].(string); ok {
				// Search for the raw value in the 'key' table
				var keyResult database.Key
				err := s.db.DB().
					Joins("join value on key.current_value_hash = value.value_hash").
					Where("value.raw = ?", fmt.Sprintf(`"%s"`, drumID)).
					First(&keyResult).
					Error
				if err == nil {
					convertedItem["object_id"] = keyResult.ObjectID
				} else {
					convertedItem["object_id"] = uuid.New().String()
				}
			} else {
				convertedItem["object_id"] = uuid.New().String()
			}
			s.logger.Debugf(
				"Generated new object_id for oracle item %d: %s",
				i,
				convertedItem["object_id"],
			)
		}

		oracleDataMap[i] = convertedItem
		s.logger.Debugf("Oracle item %d: %+v", i, convertedItem)
	}

	// Track current cloud data keys for deletion detection
	currentKeys := make(map[string]bool)
	if len(currentCloudData) > 0 {
		for _, item := range currentCloudData {
			objID, ok := item["object_id"].(string)
			if !ok {
				s.logger.Warnf(
					"object_id is not a string or is missing in current cloud data, skipping item: %+v",
					item,
				)
				continue
			}

			for key := range item {
				if key == "object_id" {
					continue
				}
				keyHash := s.db.Hash([]byte(objID + ":" + key))
				keyHex := hex.EncodeToString(keyHash)
				currentKeys[keyHex] = true
			}
		}
	}

	// Calculate insertions and updates from oracle data
	var insertions []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	var updates []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}

	oracleKeys := make(map[string]bool)

	// Process oracle data to determine insertions vs updates
	for _, item := range oracleDataMap {
		objID, ok := item["object_id"].(string)
		if !ok {
			s.logger.Warnf(
				"object_id is not a string or is missing, skipping item: %+v",
				item,
			)
			continue
		}

		for key, value := range item {
			if key == "object_id" {
				continue
			}

			// Compute key hash = blake2b256(object_id + ":" + raw_key)
			keyHash := s.db.Hash([]byte(objID + ":" + key))
			keyHex := hex.EncodeToString(keyHash)
			oracleKeys[keyHex] = true

			// Convert value to JSON bytes then hash it
			valueBytes, err := json.Marshal(value)
			if err != nil {
				s.logger.Errorf(
					"failed to marshal value for key %s: %v",
					key,
					err,
				)
				continue
			}
			valueHash := s.db.Hash(valueBytes)
			valueHex := hex.EncodeToString(valueHash)

			// Check if key exists in trie
			if memTrie.Has(keyHash) {
				// It's an update
				updates = append(updates, struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				}{
					Key:   keyHex,
					Value: valueHex,
				})
			} else {
				// It's an insertion
				insertions = append(insertions, struct {
					Key   string `json:"key"`
					Value string `json:"value"`
				}{
					Key:   keyHex,
					Value: valueHex,
				})
			}
		}
	}

	// Calculate deletions (keys in current cloud data but not in oracle data)
	var deletions []struct {
		Key string `json:"key"`
	}

	for keyHex := range currentKeys {
		if !oracleKeys[keyHex] {
			// This key was in current data but not in oracle data - it's deleted
			deletions = append(deletions, struct {
				Key string `json:"key"`
			}{
				Key: keyHex,
			})
		}
	}

	s.logger.Infow("Calculated trie operations",
		"insertions", len(insertions),
		"updates", len(updates),
		"deletions", len(deletions))

	currentMerkleRoot = hex.EncodeToString(memTrie.Hash())

	// Create upload payload in the required format
	uploadPayload := struct {
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
	}{
		Data:                  oracleDataMap,
		CurrentMerkleRoot:     "",
		PreviousMerkleRoot:    currentMerkleRoot,
		PreviousFileReference: previousFileReference,
		TrieLibrary:           "merkle-oracle-node",
		CreatedAt:             blockEvent.EventTimestamp.Unix(),
	}

	uploadPayload.TrieData.Insertions = insertions
	uploadPayload.TrieData.Updates = updates
	uploadPayload.TrieData.Deletions = deletions

	// Apply the trie operations to update the local trie
	currentSlot := uint64(blockEvent.EventContext.SlotNumber)

	// Process insertions
	for _, insertion := range insertions {
		keyBytes, err := hex.DecodeString(insertion.Key)
		if err != nil {
			return fmt.Errorf(
				"failed to decode insertion key %s: %v",
				insertion.Key,
				err,
			)
		}

		valueBytes, err := hex.DecodeString(insertion.Value)
		if err != nil {
			return fmt.Errorf(
				"failed to decode insertion value %s: %v",
				insertion.Value,
				err,
			)
		}

		s.logger.Infow(
			"TRIE_DEBUG(BUILD): trie insert",
			"key",
			insertion.Key,
			"value",
			insertion.Value,
		)
		if err := memTrie.Update(keyBytes, valueBytes, currentSlot); err != nil {
			return fmt.Errorf("failed to insert trie entry: %v", err)
		}
	}

	// Process updates
	for _, update := range updates {
		keyBytes, err := hex.DecodeString(update.Key)
		if err != nil {
			return fmt.Errorf(
				"failed to decode update key %s: %v",
				update.Key,
				err,
			)
		}

		valueBytes, err := hex.DecodeString(update.Value)
		if err != nil {
			return fmt.Errorf(
				"failed to decode update value %s: %v",
				update.Value,
				err,
			)
		}

		s.logger.Infow(
			"TRIE_DEBUG(BUILD): trie update",
			"key",
			update.Key,
			"value",
			update.Value,
		)
		if err := memTrie.Update(keyBytes, valueBytes, currentSlot); err != nil {
			return fmt.Errorf("failed to update trie entry: %v", err)
		}
	}

	// Process deletions
	for _, deletion := range deletions {
		keyBytes, err := hex.DecodeString(deletion.Key)
		if err != nil {
			return fmt.Errorf(
				"failed to decode deletion key %s: %v",
				deletion.Key,
				err,
			)
		}

		s.logger.Infow("TRIE_DEBUG(BUILD): trie delete", "key", deletion.Key)
		if err := memTrie.Delete(keyBytes); err != nil {
			return fmt.Errorf("failed to delete trie entry: %v", err)
		}
	}

	s.logger.Infow("Applied trie operations successfully",
		"insertions", len(insertions),
		"updates", len(updates),
		"deletions", len(deletions),
		"slot", currentSlot)

	// Get the updated trie root after applying operations
	updatedTrieRoot := memTrie.Hash()
	s.logger.Infof("Updated Trie Root Hash: %x", updatedTrieRoot)

	uploadPayload.CurrentMerkleRoot = hex.EncodeToString(updatedTrieRoot)

	// Marshal to JSON and upload to cloud
	payloadBytes, err := json.Marshal(uploadPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal upload payload: %v", err)
	}

	s.logger.Infof(
		"Uploading data to cloud, payload size: %d bytes",
		len(payloadBytes),
	)
	cloudRef, err := s.cloud.Upload(payloadBytes)
	if err != nil {
		return fmt.Errorf("failed to upload data to cloud: %v", err)
	}

	s.logger.Infof(
		"Successfully uploaded data to cloud with reference: %s",
		string(cloudRef),
	)

	// Convert cloud reference to bytes for transaction building
	cloudRefBytes := []byte(string(cloudRef))

	txObj, err := tx.BuildRecreateTx(
		s.appCfg,
		s.provider,
		validatorOutRef,
		updatedTrieRoot,
		cloudRefBytes,
	)
	if err != nil {
		return fmt.Errorf("failed to build tx: %v", err)
	}

	txInputs := make([]tx.InputKey, len(txObj.TransactionBody.Inputs))
	inputMap := make(map[tx.InputKey]struct{})

	for idx, input := range txObj.TransactionBody.Inputs {
		txInputs[idx] = tx.InputKey{
			TxId:  hex.EncodeToString(input.TransactionId),
			Index: input.Index,
		}

		inputMap[txInputs[idx]] = struct{}{}
	}

	hasConflict, conflictingTxHash := CheckInputConflicts(txInputs...)
	if hasConflict {
		s.logger.Warnw(
			"Transaction inputs conflict with pending transaction, skipping",
			"conflicting_tx",
			conflictingTxHash,
			"potential_inputs",
			len(txInputs),
		)
		return nil
	}

	txBytes, _ := txObj.Bytes()

	s.logger.Infof("txBytes: %x", txBytes)

	txHash, err := provider.SubmitTx(s.appCfg, s.provider, txBytes)
	if err != nil {
		if strings.Contains(err.Error(), "OutsideValidityIntervalUTxO") {
			s.logger.Warnw(
				"Transaction validity interval expired, rebuilding and resubmitting",
				"original_error",
				err.Error(),
			)

			newTxObj, rebuildErr := tx.BuildRecreateTx(
				s.appCfg,
				s.provider,
				validatorOutRef,
				updatedTrieRoot,
				cloudRefBytes,
			)
			if rebuildErr != nil {
				return fmt.Errorf(
					"failed to rebuild tx after validity interval expiration: %v",
					rebuildErr,
				)
			}

			newTxBytes, _ := newTxObj.Bytes()
			txHash, err = provider.SubmitTx(s.appCfg, s.provider, newTxBytes)
			if err != nil {
				return fmt.Errorf("failed to submit rebuilt tx: %v", err)
			}
		} else {
			return fmt.Errorf("failed to submit tx: %v", err)
		}
	}

	s.logger.Infof(
		"[Block %d] Transaction submitted %s",
		currentBlockHeight,
		txHash,
	)

	// if s.discordClient == nil {
	// 	discordClient, err := webhook.NewWithURL(
	// 		s.appCfg.Logging.NotificationDiscordWebhookURL,
	// 	)
	// 	if err != nil {
	// 		s.logger.Errorf("Failed to initialize Discord client: %v", err)
	// 	} else {
	// 		s.discordClient = discordClient
	// 	}
	// }

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
	var decodedValidatorDatumCbor []byte

	// Locate the singleton output, cache UTXO, and capture datum CBOR
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

				validatorDatum := apolloUtxo.Output.GetDatum().ToDatum()
				var err error
				decodedValidatorDatumCbor, err = gouroborosCbor.Encode(
					validatorDatum,
				)
				if err != nil {
					return fmt.Errorf(
						"failed to marshal validator datum to CBOR: %v",
						err,
					)
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

	// Must have a spend redeemer with RecreateAction
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

		decodedValidatorDatum, err := tx.DecodeMerkleOracleDatum(
			hex.EncodeToString(decodedValidatorDatumCbor),
		)
		if err != nil {
			return fmt.Errorf("error decoding contract utxo datum: %v", err)
		}
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

		decodeHexBytes := func(s string) ([]byte, error) {
			if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
				s = s[2:]
			}
			return hex.DecodeString(s)
		}

		// Build a map of key_hash -> (object_id, raw_key, raw_value) from the full snapshot `data`
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

		// Compose ApplyBatchParams and ingest (handles genesis + updates + reorgs)
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
	// Log rollback initiation
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

	// if err := s.db.PurgeHistory(rollbackEvent.SlotNumber - rollbackSlots); err != nil {
	// 	s.logger.Warnf("Failed to purge stale trie and account state: %s", err)
	// } else {
	// 	s.logger.Debugf("Successfully purged trie history before slot %d",
	// 		rollbackEvent.SlotNumber-rollbackSlots)
	// }

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

//nolint:unused
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
