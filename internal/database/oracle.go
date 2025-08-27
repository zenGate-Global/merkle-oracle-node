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
	ID          int64     `gorm:"primaryKey;autoIncrement"`
	CID         string    `gorm:"column:cid;uniqueIndex;not null"`
	PreviousCID *string   `gorm:"column:previous_cid;index:oracle_file_prev_idx"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;default:now()"`

	// self ref relation, previous_cid -> oracle_file.cid
	PreviousFile *OracleFile  `gorm:"foreignKey:PreviousCID;references:CID"`
	NextFiles    []OracleFile `gorm:"foreignKey:PreviousCID;references:CID"`

	// 1:1 OracleFile -> Trie (trie.oracle_file_id UNIQUE, ON DELETE CASCADE)
	Trie *Trie `gorm:"foreignKey:OracleFileID;references:ID;constraint:OnDelete:CASCADE"`

	// objs that first/last referenced this file (ON DELETE SET NULL)
	ObjectsFirst []Object `gorm:"foreignKey:FirstSeenFileID;references:ID;constraint:OnDelete:SET NULL"`
	ObjectsLast  []Object `gorm:"foreignKey:LastSeenFileID;references:ID;constraint:OnDelete:SET NULL"`

	ObjectInFiles []ObjectInFile `gorm:"foreignKey:OracleFileID;references:ID;constraint:OnDelete:CASCADE"`
	Objects       []Object       `gorm:"many2many:object_in_file;"`
}

func (OracleFile) TableName() string { return "oracle_file" }

type Trie struct {
	ID                    int64      `gorm:"primaryKey;autoIncrement"`
	OracleFileID          int64      `gorm:"column:oracle_file_id;not null;uniqueIndex"`
	CurrentMerkleRoot     string     `gorm:"column:current_merkle_root;not null"`
	PreviousMerkleRoot    *string    `gorm:"column:previous_merkle_root"`
	TrieLibrary           *string    `gorm:"column:trie_library"`
	BlockchainConfirmedAt *time.Time `gorm:"column:blockchain_confirmed_at;index:trie_confirmed_idx"`
	Slot                  int64      `gorm:"column:slot;not null;index:trie_slot_idx"`
	TxID                  string     `gorm:"column:txid;not null;uniqueIndex"`
	TxFee                 uint32     `gorm:"column:tx_fee;not null"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;default:now()"`

	// belongs to OracleFile (ON DELETE CASCADE)
	OracleFile *OracleFile `gorm:"foreignKey:OracleFileID;references:ID;constraint:OnDelete:CASCADE"`

	// 1:N Trie -> TrieOperation (ON DELETE CASCADE)
	Operations []TrieOperation `gorm:"foreignKey:TrieID;references:ID;constraint:OnDelete:CASCADE"`
}

func (Trie) TableName() string { return "trie" }

type Object struct {
	ID              string      `gorm:"primaryKey"` // domain UUID
	FirstSeenFileID *int64      `gorm:"column:first_seen_file_id"`
	LastSeenFileID  *int64      `gorm:"column:last_seen_file_id"`
	UpdatedAt       time.Time   `gorm:"column:updated_at;not null;default:now()"`
	FirstSeenFile   *OracleFile `gorm:"foreignKey:FirstSeenFileID;references:ID;constraint:OnDelete:SET NULL"`
	LastSeenFile    *OracleFile `gorm:"foreignKey:LastSeenFileID;references:ID;constraint:OnDelete:SET NULL"`
	CreatedAt       time.Time   `gorm:"column:created_at;not null;default:now()"`

	// 1:N Object -> Key (ON DELETE CASCADE)
	Keys []Key `gorm:"foreignKey:ObjectID;references:ID;constraint:OnDelete:CASCADE"`

	ObjectInFiles []ObjectInFile `gorm:"foreignKey:ObjectID;references:ID;constraint:OnDelete:CASCADE"`
	OracleFiles   []OracleFile   `gorm:"many2many:object_in_file;"`
}

func (Object) TableName() string { return "object" }

type ObjectInFile struct {
	ObjectID     string `gorm:"column:object_id;primaryKey;not null"`
	OracleFileID int64  `gorm:"column:oracle_file_id;primaryKey;not null"`

	// belongs to both sides (ON DELETE CASCADE on both FKs)
	Object     Object     `gorm:"foreignKey:ObjectID;references:ID;constraint:OnDelete:CASCADE"`
	OracleFile OracleFile `gorm:"foreignKey:OracleFileID;references:ID;constraint:OnDelete:CASCADE"`
}

func (ObjectInFile) TableName() string { return "object_in_file" }

type Value struct {
	ValueHash []byte         `gorm:"column:value_hash;type:bytea;primaryKey"  json:"valueHash" swaggertype:"string" format:"byte"`
	Raw       datatypes.JSON `gorm:"column:raw;type:jsonb;not null"           json:"raw"       swaggertype:"object"`
	CreatedAt time.Time      `gorm:"column:created_at;not null;default:now()" json:"createdAt"`

	Keys              []Key              `gorm:"foreignKey:CurrentValueHash;references:ValueHash"`
	TrieOperationKeys []TrieOperationKey `gorm:"foreignKey:ValueHash;references:ValueHash"`
}

func (Value) TableName() string { return "value" }

type Key struct {
	KeyHash          []byte     `gorm:"column:key_hash;type:bytea;primaryKey"`
	ObjectID         string     `gorm:"column:object_id;not null;index:key_object_raw_idx,priority:1"`
	RawKey           string     `gorm:"column:raw_key;not null;index:key_object_raw_idx,priority:2"`
	CurrentValueHash []byte     `gorm:"column:current_value_hash;type:bytea;index:key_current_val_idx"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt        *time.Time `gorm:"column:updated_at"`
	DeletedAt        *time.Time `gorm:"column:deleted_at"`
	IsDeleted        bool       `gorm:"column:is_deleted;not null;default:false"`

	// belongs to Object (ON DELETE CASCADE)
	Object Object `gorm:"foreignKey:ObjectID;references:ID;constraint:OnDelete:CASCADE"`
	// belongs to Value
	CurrentValue *Value `gorm:"foreignKey:CurrentValueHash;references:ValueHash"`

	TrieOperationKeys []TrieOperationKey `gorm:"foreignKey:KeyHash;references:KeyHash;constraint:OnDelete:CASCADE"`
}

func (Key) TableName() string { return "key" }

type TrieOperation struct {
	ID            int64     `gorm:"primaryKey;autoIncrement"`
	TrieID        int64     `gorm:"column:trie_id;not null;index:trie_op_apply_idx,priority:1"`
	OperationType OpType    `gorm:"column:operation_type;type:op_type;not null;index:trie_op_apply_idx,priority:2"`
	SequenceOrder uint32    `gorm:"column:sequence_order;not null;index:trie_op_apply_idx,priority:3"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;default:now()"`

	// belongs to Trie (ON DELETE CASCADE)
	Trie Trie `gorm:"foreignKey:TrieID;references:ID;constraint:OnDelete:CASCADE"`

	// 1:N TrieOperation -> TrieOperationKey (ON DELETE CASCADE)
	Keys []TrieOperationKey `gorm:"foreignKey:TrieOperationID;references:ID;constraint:OnDelete:CASCADE"`
}

func (TrieOperation) TableName() string { return "trie_operation" }

type TrieOperationKey struct {
	TrieOperationID int64     `gorm:"column:trie_operation_id;primaryKey;not null;index:tok_op_key_idx,priority:1"`
	KeyHash         []byte    `gorm:"column:key_hash;type:bytea;primaryKey;not null;index:tok_key_idx;index:tok_op_key_idx,priority:2"`
	ValueHash       []byte    `gorm:"column:value_hash;type:bytea;index:tok_value_idx"`
	CreatedAt       time.Time `gorm:"column:created_at;not null;default:now()"`

	//belongs to all three (CASCADE on trie_operation, CASCADE on key, default on value)
	TrieOperation TrieOperation `gorm:"foreignKey:TrieOperationID;references:ID;constraint:OnDelete:CASCADE"`
	Key           Key           `gorm:"foreignKey:KeyHash;references:KeyHash;constraint:OnDelete:CASCADE"`
	Value         *Value        `gorm:"foreignKey:ValueHash;references:ValueHash"`
}

func (TrieOperationKey) TableName() string { return "trie_operation_key" }
