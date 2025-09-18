package types

import (
	"time"

	"github.com/anthdm/hollywood/actor"
	input_chainsync "github.com/blinklabs-io/adder/input/chainsync"
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
	BlockEvent input_chainsync.BlockEvent
	Timestamp  time.Time
	TipReached bool
}

type IndexerTransactionEvent struct {
	EventTransaction input_chainsync.TransactionEvent
	EventContext     input_chainsync.TransactionContext
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
