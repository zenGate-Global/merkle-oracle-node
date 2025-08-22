package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"golang.org/x/crypto/blake2b"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Entry struct {
	ObjectID      string
	RawKey        string
	RawValue      json.RawMessage // for delete ops, this can be nil/ignored
	OperationType OpType          // "insert" | "update" | "delete"
	SequenceOrder uint32
}

type ApplyBatchParams struct {
	CID                   string
	PreviousCID           *string
	TrieLibrary           *string
	CurrentMerkleRoot     string
	PreviousMerkleRoot    *string
	BlockchainConfirmedAt *time.Time
	Slot                  int64
	Entries               []Entry
}

// ApplyOracleFile ingests one oracle file
func (d *Database) ApplyOracleFile(
	ctx context.Context,
	p ApplyBatchParams,
) (*OracleFile, *Trie, error) {
	if p.CID == "" {
		return nil, nil, errors.New("cid is required")
	}
	if p.CurrentMerkleRoot == "" {
		return nil, nil, errors.New("current merkle root is required")
	}
	if p.Slot <= 0 {
		return nil, nil, errors.New("slot must be > 0")
	}

	var outFile OracleFile
	var outTrie Trie

	err := d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// if file already exists, return it.
		{
			var existing OracleFile
			if err := tx.Where("cid = ?", p.CID).First(&existing).Error; err == nil {
				var tr Trie
				if err := tx.Where("oracle_file_id = ?", existing.ID).First(&tr).Error; err != nil {
					return err
				}
				outFile, outTrie = existing, tr
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		// latest trie (head)
		type headRow struct {
			TrieID         int64
			Slot           int64
			CurrentMerkle  string
			OracleFileID   int64
			OracleFileCID  string
			PreviousMerkle sql.NullString
		}
		var head headRow
		_ = tx.
			Table("trie").
			Select("trie.id as trie_id, trie.slot, trie.current_merkle_root as current_merkle, trie.oracle_file_id, oracle_file.cid as oracle_file_cid, trie.previous_merkle_root as previous_merkle").
			Joins("JOIN oracle_file ON oracle_file.id = trie.oracle_file_id").
			Order("trie.slot DESC, trie.id DESC").
			Limit(1).
			Scan(&head).Error

		// create oracle_file
		of := &OracleFile{
			CID:         p.CID,
			PreviousCID: p.PreviousCID,
		}
		if err := tx.Create(of).Error; err != nil {
			return err
		}

		// create trie row
		tr := &Trie{
			OracleFileID:          of.ID,
			CurrentMerkleRoot:     p.CurrentMerkleRoot,
			PreviousMerkleRoot:    p.PreviousMerkleRoot,
			TrieLibrary:           p.TrieLibrary,
			BlockchainConfirmedAt: p.BlockchainConfirmedAt,
			Slot:                  p.Slot,
		}
		if err := tx.Create(tr).Error; err != nil {
			return err
		}

		// sort ops by insert < update < delete; then by sequence
		rank := map[OpType]int{OpInsert: 1, OpUpdate: 2, OpDelete: 3}
		sort.SliceStable(p.Entries, func(i, j int) bool {
			ri, rj := rank[p.Entries[i].OperationType], rank[p.Entries[j].OperationType]
			if ri == rj {
				return p.Entries[i].SequenceOrder < p.Entries[j].SequenceOrder
			}
			return ri < rj
		})

		// create one trie_operation per (op_type, seq)
		type grp struct {
			op  OpType
			seq uint32
		}
		ops := map[grp]int64{}
		for _, e := range p.Entries {
			g := grp{e.OperationType, e.SequenceOrder}
			if _, ok := ops[g]; ok {
				continue
			}
			op := TrieOperation{
				TrieID:        tr.ID,
				OperationType: e.OperationType,
				SequenceOrder: e.SequenceOrder,
			}
			if err := tx.Create(&op).Error; err != nil {
				return err
			}
			ops[g] = op.ID
		}

		logger := d.logger.With("function", "ApplyOracleFile")

		// apply to in-memory trie
		for _, e := range p.Entries {
			kh := blake2b.Sum256([]byte(e.ObjectID + ":" + e.RawKey))
			switch e.OperationType {
			case OpInsert, OpUpdate:
				canon, err := canonicalizeJSON(e.RawValue)
				if err != nil {
					return err
				}
				vh := blake2b.Sum256(canon)
				if e.OperationType == OpInsert {
					logger.Debugw(
						"TRIE_DEBUG(APPLY): trie set",
						"key",
						hex.EncodeToString(kh[:]),
						"value",
						hex.EncodeToString(vh[:]),
					)
				} else {
					logger.Debugw(
						"TRIE_DEBUG(APPLY): trie update",
						"key",
						hex.EncodeToString(kh[:]),
						"value",
						hex.EncodeToString(vh[:]),
					)
				}
				if err := d.UpdateTrie(kh[:], vh[:], uint64(p.Slot)); err != nil {
					return err
				}
			case OpDelete:
				logger.Debugw(
					"TRIE_DEBUG(APPLY): trie delete",
					"key",
					hex.EncodeToString(kh[:]),
				)
				if err := d.DeleteTrieEntry(kh[:]); err != nil {
					return err
				}
			}
		}

		// object, object_in_file, key, value, tok (trigger updates key state) are persisted
		emitted := map[grp]map[[32]byte]struct{}{}
		for _, e := range p.Entries {
			kh := blake2b.Sum256([]byte(e.ObjectID + ":" + e.RawKey))

			// upsert object and provenance
			obj := &Object{
				ID:              e.ObjectID,
				FirstSeenFileID: &of.ID,
				LastSeenFileID:  &of.ID,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "id"}},
				DoUpdates: clause.Assignments(map[string]any{
					"first_seen_file_id": gorm.Expr("COALESCE(object.first_seen_file_id, ?)", of.ID),
					"last_seen_file_id":  of.ID,
					"updated_at":         gorm.Expr("NOW()"),
				}),
			}).Create(obj).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ObjectInFile{
				ObjectID:     e.ObjectID,
				OracleFileID: of.ID,
			}).Error; err != nil {
				return err
			}

			// ensure key row exists
			k := &Key{KeyHash: kh[:], ObjectID: e.ObjectID, RawKey: e.RawKey}
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key_hash"}},
				DoNothing: true,
			}).Create(k).Error; err != nil {
				return err
			}

			// avoid duplicate (op_type, seq, key_hash) inserts within batch
			g := grp{e.OperationType, e.SequenceOrder}
			if emitted[g] == nil {
				emitted[g] = map[[32]byte]struct{}{}
			}
			if _, dup := emitted[g][kh]; dup {
				continue
			}
			emitted[g][kh] = struct{}{}

			// build tok row; trigger enforces value_hash presence for non-deletes
			tok := TrieOperationKey{
				TrieOperationID: ops[g],
				KeyHash:         kh[:],
			}
			if e.OperationType == OpInsert || e.OperationType == OpUpdate {
				canon, err := canonicalizeJSON(e.RawValue)
				if err != nil {
					return err
				}
				vh := blake2b.Sum256(canon)
				// Upsert value
				val := &Value{ValueHash: vh[:], Raw: datatypes.JSON(canon)}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "value_hash"}},
					DoNothing: true,
				}).Create(val).Error; err != nil {
					return err
				}
				tok.ValueHash = vh[:]
			} else {
				tok.ValueHash = nil // must be NULL for delete
			}
			if err := tx.Create(&tok).Error; err != nil {
				return err
			}
		}

		outFile, outTrie = *of, *tr
		return nil
	})

	return &outFile, &outTrie, err
}

// canonicalizeJSON normalizes JSON primitives to a stable byte representation.
// This project only expects primitives (null, bool, number, string).
func canonicalizeJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := encodeCanonical(v, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeCanonical(v any, b *bytes.Buffer) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		b.WriteString(string(t))
	case string:
		enc, _ := json.Marshal(t) // ensures escaping
		b.Write(enc)
	default:
		return fmt.Errorf("unsupported JSON value type: %T", v)
	}
	return nil
}
