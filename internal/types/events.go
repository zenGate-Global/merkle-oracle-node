package types

import (
	"time"

	"github.com/anthdm/hollywood/actor"
	addevent "github.com/blinklabs-io/adder/event"
)

// Strategy configuration for merkle oracle node
type StrategyConfig struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	StartBlockSlot uint64 `json:"start_block_slot"`
	StartBlockHash string `json:"start_block_hash"`
}

// Message to set parent PID for strategy actors
type SetParentPID struct {
	PID *actor.PID
}

// Events that can be sent to strategies
type IndexerBlockEvent struct {
	BlockEvent addevent.BlockEvent
	Timestamp  time.Time
	TipReached bool
}

type IndexerTransactionEvent struct {
	EventTransaction addevent.TransactionEvent
	EventContext     addevent.TransactionContext
	EventTimestamp   time.Time
	TipReached       bool
}

type IndexerRollbackEvent struct {
	SlotNumber uint64
	BlockHash  string
}

type IndexerStatusEvent struct {
	CursorSlot  uint64
	CursorHash  string
	BlockNumber uint64
	TipSlot     uint64
	TipHash     string
	TipReached  bool
	IsRollback  bool
}

type IndexerRestartRequest struct {
	Reason      string
	StrategyPID *actor.PID // PID of the strategy that triggered the restart
	RestartSlot uint64     // Slot to restart from
	RestartHash string     // Block hash to restart from
}

type IndexerRestartComplete struct {
	Success bool
	Error   string
}

// ImmediatePublishRequest is sent from the API to trigger an immediate on-chain publish
// with custom oracle data, bypassing the updateInterval check.
type ImmediatePublishRequest struct {
	// Data is the oracle data payload to publish. Each map should contain:
	// - "object_id" (string): unique identifier for the object
	// - Additional key-value pairs representing the object's data
	// Example: [{"object_id": "abc123", "price": 100, "name": "item"}]
	Data []map[string]interface{}
	// ResponseChan receives the publish result (optional, for synchronous calls)
	ResponseChan chan ImmediatePublishResponse
}

// ImmediatePublishResponse contains the result of an immediate publish request.
type ImmediatePublishResponse struct {
	Success bool   `json:"success"`
	TxHash  string `json:"txHash,omitempty"`
	Error   string `json:"error,omitempty"`
}
