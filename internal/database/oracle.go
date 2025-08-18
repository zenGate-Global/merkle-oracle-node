package database

import (
	"time"

	"gorm.io/datatypes"
)

type OpType string

const (
	OpInsert OpType = "insert"
	OpUpdate OpType = "update"
	OpDelete OpType = "delete"
)

type OracleFile struct {
	ID            int64          `gorm:"primaryKey;autoIncrement"`
	CID           string         `gorm:"column:cid;uniqueIndex;not null"`
	PreviousCID   *string        `gorm:"column:previous_cid;index:oracle_file_prev_idx"`
	CreatedAt     time.Time      `gorm:"column:created_at;not null;default:now()"`
	Tries         []Trie         `gorm:"-"`
	ObjectsFirst  []Object       `gorm:"-"`
	ObjectsLast   []Object       `gorm:"-"`
	ObjectInFiles []ObjectInFile `gorm:"-"`
}

func (OracleFile) TableName() string { return "oracle_file" }

type Trie struct {
	ID                    int64      `gorm:"primaryKey;autoIncrement"`
	OracleFileID          int64      `gorm:"column:oracle_file_id;not null;uniqueIndex"`
	OracleFile            OracleFile `gorm:"-"`
	CurrentMerkleRoot     string     `gorm:"column:current_merkle_root;not null"`
	PreviousMerkleRoot    *string    `gorm:"column:previous_merkle_root"`
	TrieLibrary           *string    `gorm:"column:trie_library"`
	BlockchainConfirmedAt *time.Time `gorm:"column:blockchain_confirmed_at;index:trie_confirmed_idx"`
	Slot                  int64      `gorm:"column:slot;not null;index:trie_slot_idx"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;default:now()"`

	Operations []TrieOperation `gorm:"-"`
}

func (Trie) TableName() string { return "trie" }

type Object struct {
	ID              string      `gorm:"primaryKey"` // domain UUID
	FirstSeenFileID *int64      `gorm:"column:first_seen_file_id"`
	LastSeenFileID  *int64      `gorm:"column:last_seen_file_id"`
	FirstSeenFile   *OracleFile `gorm:"-"`
	LastSeenFile    *OracleFile `gorm:"-"`
	CreatedAt       time.Time   `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt       time.Time   `gorm:"column:updated_at;not null;default:now()"`

	Keys          []Key          `gorm:"-"`
	ObjectInFiles []ObjectInFile `gorm:"-"`
}

func (Object) TableName() string { return "object" }

type ObjectInFile struct {
	ObjectID     string     `gorm:"column:object_id;primaryKey;not null"`
	OracleFileID int64      `gorm:"column:oracle_file_id;primaryKey;not null"`
	Object       Object     `gorm:"-"`
	OracleFile   OracleFile `gorm:"-"`
}

func (ObjectInFile) TableName() string { return "object_in_file" }

type Value struct {
	ValueHash []byte         `gorm:"column:value_hash;type:bytea;primaryKey"`
	Raw       datatypes.JSON `gorm:"column:raw;type:jsonb;not null"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:now()"`
}

func (Value) TableName() string { return "value" }

type Key struct {
	KeyHash          []byte     `gorm:"column:key_hash;type:bytea;primaryKey"`
	ObjectID         string     `gorm:"column:object_id;not null;index:key_object_raw_idx,priority:1;uniqueIndex:uniq_key_per_object,priority:1"`
	Object           Object     `gorm:"-"`
	RawKey           string     `gorm:"column:raw_key;not null;index:key_object_raw_idx,priority:2;uniqueIndex:uniq_key_per_object,priority:2"`
	CurrentValueHash []byte     `gorm:"column:current_value_hash;type:bytea;index:key_current_val_idx"`
	CurrentValue     *Value     `gorm:"-"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt        *time.Time `gorm:"column:updated_at"`
	DeletedAt        *time.Time `gorm:"column:deleted_at"`
	IsDeleted        bool       `gorm:"column:is_deleted;not null;default:false"`
}

func (Key) TableName() string { return "key" }

type TrieOperation struct {
	ID            int64     `gorm:"primaryKey;autoIncrement"`
	TrieID        int64     `gorm:"column:trie_id;not null;index:trie_op_apply_idx,priority:1;uniqueIndex:uniq_trie_op,priority:1"`
	Trie          Trie      `gorm:"-"`
	OperationType OpType    `gorm:"column:operation_type;type:op_type;not null;index:trie_op_apply_idx,priority:2;uniqueIndex:uniq_trie_op,priority:2"`
	SequenceOrder uint32    `gorm:"column:sequence_order;not null;index:trie_op_apply_idx,priority:3;uniqueIndex:uniq_trie_op,priority:3"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;default:now()"`

	Keys []TrieOperationKey `gorm:"-"`
}

func (TrieOperation) TableName() string { return "trie_operation" }

type TrieOperationKey struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	TrieOperationID int64     `gorm:"column:trie_operation_id;not null;index:tok_op_key_idx,priority:1;uniqueIndex:uniq_tok_op_key,priority:1"`
	KeyHash         []byte    `gorm:"column:key_hash;type:bytea;not null;index:tok_key_idx;index:tok_op_key_idx,priority:2;uniqueIndex:uniq_tok_op_key,priority:2"`
	ValueHash       []byte    `gorm:"column:value_hash;type:bytea;index:tok_value_idx"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;default:now()"`

	TrieOperation TrieOperation `gorm:"-"`
	Key           Key           `gorm:"-"`
	Value         *Value        `gorm:"-"`
}

func (TrieOperationKey) TableName() string { return "trie_operation_key" }
