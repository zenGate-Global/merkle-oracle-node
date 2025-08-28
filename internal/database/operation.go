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
	TxID                  string
	TxFee                 uint32
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
	if p.TxID == "" {
		return nil, nil, errors.New("txid is required")
	}

	var outFile OracleFile
	var outTrie Trie

	err := d.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// if file already exists, return it.
		{
			var existing OracleFile
			if err := tx.
				Preload("Trie").
				Where("cid = ?", p.CID).
				First(&existing).Error; err == nil {
				outFile = existing
				outTrie = *existing.Trie
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
			Select(`trie.id as trie_id,
			        trie.slot,
			        trie.current_merkle_root as current_merkle,
			        trie.oracle_file_id,
			        oracle_file.cid as oracle_file_cid,
			        trie.previous_merkle_root as previous_merkle`).
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
			TxID:                  p.TxID,
			TxFee:                 p.TxFee,
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
			// if conflict, fetch the id, this avoids conflicts
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&op).Error; err != nil {
				return err
			}
			if op.ID == 0 {
				// already exists, load its ID
				if err := tx.
					Select("id").
					Where("trie_id = ? AND operation_type = ? AND sequence_order = ?",
						tr.ID, e.OperationType, e.SequenceOrder).
					First(&op).Error; err != nil {
					return err
				}
			}
			ops[g] = op.ID
		}

		logger := d.logger.With("function", "ApplyOracleFile")

		// apply to in-memory trie
		for _, e := range p.Entries {
			kh := d.KeyHash(e.ObjectID, e.RawKey)
			switch e.OperationType {
			case OpInsert, OpUpdate:
				var value interface{}
				if err := json.Unmarshal(e.RawValue, &value); err != nil {
					return err
				}
				vh, err := d.ValueHash(value)
				if err != nil {
					return err
				}
				if e.OperationType == OpInsert {
					logger.Debugw(
						"TRIE_DEBUG(APPLY): trie set",
						"key", hex.EncodeToString(kh),
						"value", hex.EncodeToString(vh),
					)
				} else {
					logger.Debugw(
						"TRIE_DEBUG(APPLY): trie update",
						"key", hex.EncodeToString(kh),
						"value", hex.EncodeToString(vh),
					)
				}
				if err := d.UpdateTrie(kh, vh, uint64(p.Slot)); err != nil {
					return err
				}
			case OpDelete:
				logger.Debugw(
					"TRIE_DEBUG(APPLY): trie delete",
					"key", hex.EncodeToString(kh),
				)
				if err := d.DeleteTrieEntry(kh); err != nil {
					return err
				}
			}
		}

		// object, object_in_file, key, value, tok (trigger updates key state) are persisted
		emitted := map[grp]map[[32]byte]struct{}{}
		for _, e := range p.Entries {
			khBytes := d.KeyHash(e.ObjectID, e.RawKey)
			kh := bytes32(khBytes)

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
				var value interface{}
				if err := json.Unmarshal(e.RawValue, &value); err != nil {
					return err
				}
				vhBytes, err := d.ValueHash(value)
				if err != nil {
					return err
				}

				canon, err := CanonicalizeJSON(e.RawValue)
				if err != nil {
					return err
				}

				// Upsert value
				val := &Value{ValueHash: vhBytes, Raw: datatypes.JSON(canon)}
				if err := tx.Clauses(clause.OnConflict{
					Columns:   []clause.Column{{Name: "value_hash"}},
					DoNothing: true,
				}).Create(val).Error; err != nil {
					return err
				}
				tok.ValueHash = vhBytes
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

type keyRow struct {
	KeyHash          []byte `gorm:"column:key_hash"`
	CurrentValueHash []byte `gorm:"column:current_value_hash"`
}

func bytes32(b []byte) (out [32]byte) { copy(out[:], b); return }

// GetCurrentStateMap retrieves all current keys and their value hashes from the database
// and returns them as a map for efficient lookups during trie operations.
func (d *Database) GetCurrentStateMap(
	ctx context.Context,
) (map[[32]byte][32]byte, error) {
	var rows []keyRow
	if err := d.db.WithContext(ctx).
		Model(&Key{}).
		Select("key_hash, current_value_hash").
		Where("deleted_at IS NULL AND current_value_hash IS NOT NULL").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to query current keys: %w", err)
	}

	currentStateMap := make(map[[32]byte][32]byte, len(rows))
	for _, r := range rows {
		if len(r.KeyHash) == 32 && len(r.CurrentValueHash) == 32 {
			currentStateMap[bytes32(r.KeyHash)] = bytes32(r.CurrentValueHash)
		}
	}

	return currentStateMap, nil
}

// GetStateMapAtSlot retrieves the state of the trie at a specific slot.
// It reconstructs the key-value map by looking at the last operation for each key up to the given slot.
func (d *Database) GetStateMapAtSlot(
	ctx context.Context,
	slot uint64,
) (map[[32]byte][32]byte, error) {

	type keyVal struct {
		KeyHash   []byte `gorm:"column:key_hash"`
		ValueHash []byte `gorm:"column:value_hash"`
	}

	var results []keyVal

	// finds the latest operation for each key at or before specified slot
	rawQuery := `
		SELECT key_hash, value_hash
		FROM (
			SELECT DISTINCT ON (tok.key_hash)
				   tok.key_hash, 
				   tok.value_hash, 
				   o.operation_type
			FROM trie_operation_key tok
			JOIN trie_operation o ON o.id = tok.trie_operation_id
			JOIN trie t ON t.id = o.trie_id
			WHERE t.slot <= ?
			ORDER BY
				tok.key_hash,
				t.slot DESC,
				t.id DESC,
				CASE o.operation_type
					WHEN 'insert' THEN 1
					WHEN 'update' THEN 2
					WHEN 'delete' THEN 3
				END DESC,
				o.sequence_order DESC,
				o.id DESC
		) last_ops
		WHERE operation_type <> 'delete' AND value_hash IS NOT NULL
	`

	if err := d.db.WithContext(ctx).Raw(rawQuery, slot).Scan(&results).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to query state at slot %d: %w",
			slot,
			err,
		)
	}

	stateMap := make(map[[32]byte][32]byte, len(results))
	for _, r := range results {
		if len(r.KeyHash) == 32 && len(r.ValueHash) == 32 {
			stateMap[bytes32(r.KeyHash)] = bytes32(r.ValueHash)
		}
	}

	return stateMap, nil
}

func (d *Database) GetOracleFileByCID(
	ctx context.Context,
	cid string,
) (*OracleFile, error) {
	var oracleFile OracleFile
	if err := d.db.WithContext(ctx).
		Preload("Trie").
		Preload("ObjectsFirst").
		Preload("ObjectsLast").
		Where("cid = ?", cid).
		First(&oracleFile).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to find oracle file with CID %s: %w",
			cid,
			err,
		)
	}
	return &oracleFile, nil
}

func (d *Database) GetTrieByOracleFileID(
	ctx context.Context,
	oracleFileID int64,
) (*Trie, error) {
	var trie Trie
	if err := d.db.WithContext(ctx).
		Preload("Operations").
		Where("oracle_file_id = ?", oracleFileID).
		First(&trie).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to find trie for oracle file ID %d: %w",
			oracleFileID,
			err,
		)
	}
	return &trie, nil
}

func (d *Database) GetObjectByID(
	ctx context.Context,
	id string,
) (*Object, error) {
	var obj Object
	if err := d.db.WithContext(ctx).
		Preload("Keys", "deleted_at IS NULL AND NOT is_deleted").
		First(&obj, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to find object with ID %s: %w", id, err)
	}
	return &obj, nil
}

type GetAllObjectIDsResult struct {
	ObjectIDs []string `json:"objectIds"`
	Total     int64    `json:"total"`
	Limit     int      `json:"limit"`
	Offset    int      `json:"offset"`
}

// GetAllObjectIDs returns a paginated list of all tracked object IDs
func (d *Database) GetAllObjectIDs(
	ctx context.Context,
	limit, offset int,
) (*GetAllObjectIDsResult, error) {
	var objectIDs []string
	var total int64

	// Get total count
	if err := d.db.WithContext(ctx).
		Model(&Object{}).
		Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count objects: %w", err)
	}

	// Get paginated object IDs
	if err := d.db.WithContext(ctx).
		Model(&Object{}).
		Select("id").
		Order("id ASC").
		Limit(limit).
		Offset(offset).
		Pluck("id", &objectIDs).Error; err != nil {
		return nil, fmt.Errorf("failed to get object IDs: %w", err)
	}

	return &GetAllObjectIDsResult{
		ObjectIDs: objectIDs,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	}, nil
}

// GetObjectCurrentValuesWithTimestamp returns the current key-value pairs for an object
// along with the timestamp and slot of the latest data operation. The
// returned timestamp/slot represent most recent blockchain operation affecting this object.
func (d *Database) GetObjectCurrentValuesWithTimestamp(
	ctx context.Context,
	id string,
) (*ObjectValuesWithTimestamp, error) {
	type row struct {
		RawKey        string         `gorm:"column:raw_key"`
		Raw           datatypes.JSON `gorm:"column:raw"`
		DataTimestamp time.Time      `gorm:"column:data_timestamp"`
		DataSlot      int64          `gorm:"column:data_slot"`
	}

	var rows []row
	const query = `
		WITH latest_data AS (
			SELECT 
				COALESCE(t.blockchain_confirmed_at, t.created_at) AS data_timestamp,
				t.slot AS data_slot
			FROM trie t
			JOIN trie_operation o ON o.trie_id = t.id
			JOIN trie_operation_key tok ON tok.trie_operation_id = o.id
			JOIN key k2 ON k2.key_hash = tok.key_hash
			WHERE k2.object_id = ?
			ORDER BY COALESCE(t.blockchain_confirmed_at, t.created_at) DESC, t.slot DESC, t.id DESC
			LIMIT 1
		)
		SELECT 
			key.raw_key, 
			value.raw,
			COALESCE(ld.data_timestamp, statement_timestamp()) AS data_timestamp,
			COALESCE(ld.data_slot, 0) AS data_slot
		FROM key
		JOIN value ON value.value_hash = key.current_value_hash
		LEFT JOIN latest_data ld ON true
		WHERE key.object_id = ? 
		  AND key.deleted_at IS NULL 
		  AND NOT key.is_deleted
	`

	if err := d.db.WithContext(ctx).Raw(query, id, id).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to get current values with timestamp for object %s: %w",
			id,
			err,
		)
	}

	// If no rows, we still want to return a timestamp and slot for consistency
	if len(rows) == 0 {
		type metaRow struct {
			DataTimestamp time.Time `gorm:"column:data_timestamp"`
			DataSlot      int64     `gorm:"column:data_slot"`
		}

		var meta metaRow
		const metaQuery = `
			SELECT 
				COALESCE(t.blockchain_confirmed_at, t.created_at) AS data_timestamp,
				t.slot AS data_slot
			FROM trie t
			JOIN trie_operation o ON o.trie_id = t.id
			JOIN trie_operation_key tok ON tok.trie_operation_id = o.id
			JOIN key k ON k.key_hash = tok.key_hash
			WHERE k.object_id = ?
			ORDER BY COALESCE(t.blockchain_confirmed_at, t.created_at) DESC, t.slot DESC, t.id DESC
			LIMIT 1
		`
		if err := d.db.WithContext(ctx).Raw(metaQuery, id).Scan(&meta).Error; err != nil {
			// Fallback to statement time and slot 0 if we can't get data
			meta.DataTimestamp = time.Now().UTC()
			meta.DataSlot = 0
		}

		return &ObjectValuesWithTimestamp{
			Values:    make(map[string]any),
			Timestamp: meta.DataTimestamp,
			Slot:      meta.DataSlot,
		}, nil
	}

	out := make(map[string]any, len(rows))
	var timestamp time.Time
	var slot int64

	for i, r := range rows {
		var v any
		dec := json.NewDecoder(bytes.NewReader(r.Raw))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf(
				"failed to unmarshal value for key %s: %w",
				r.RawKey,
				err,
			)
		}
		out[r.RawKey] = v

		// Grab timestamp/slot from first row (all rows have identical values)
		if i == 0 {
			timestamp = r.DataTimestamp
			slot = r.DataSlot
		}
	}

	return &ObjectValuesWithTimestamp{
		Values:    out,
		Timestamp: timestamp.UTC(),
		Slot:      slot,
	}, nil
}

// GetObjectValuesAtSlotWithTimestamp returns the key-value pairs for an object as they existed at a specific slot,
// along with the corresponding timestamp information. The returned timestamp is latest timestamp among
// operations contributing to this snapshot at the specified slot boundary.
func (d *Database) GetObjectValuesAtSlotWithTimestamp(
	ctx context.Context,
	id string,
	slot int64,
) (*ObjectValuesWithTimestamp, error) {
	type row struct {
		RawKey    string         `gorm:"column:raw_key"`
		Raw       datatypes.JSON `gorm:"column:raw"`
		Timestamp time.Time      `gorm:"column:timestamp"`
	}

	var rows []row
	const rawQuery = `
		WITH last_ops AS (
			SELECT DISTINCT ON (tok.key_hash)
				tok.key_hash,
				tok.value_hash,
				o.operation_type,
				k.raw_key,
				COALESCE(t.blockchain_confirmed_at, t.created_at) as timestamp
			FROM trie_operation_key tok
			JOIN trie_operation o ON o.id = tok.trie_operation_id
			JOIN trie t           ON t.id = o.trie_id
			JOIN key  k           ON k.key_hash = tok.key_hash
			WHERE k.object_id = ? AND t.slot <= ?
			ORDER BY
				tok.key_hash,
				t.slot DESC,
				t.id DESC,
				CASE o.operation_type
					WHEN 'insert' THEN 1
					WHEN 'update' THEN 2
					WHEN 'delete' THEN 3
				END DESC,
				o.sequence_order DESC,
				o.id DESC
		)
		SELECT lo.raw_key, v.raw, lo.timestamp
		FROM last_ops lo
		JOIN value v ON v.value_hash = lo.value_hash
		WHERE lo.operation_type <> 'delete' AND lo.value_hash IS NOT NULL;
	`
	if err := d.db.WithContext(ctx).Raw(rawQuery, id, slot).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to get values for object %s at slot %d: %w",
			id, slot, err,
		)
	}

	out := make(map[string]any, len(rows))
	var latestTimestamp time.Time

	for _, r := range rows {
		var v any
		dec := json.NewDecoder(bytes.NewReader(r.Raw))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf(
				"failed to unmarshal value for key %s: %w",
				r.RawKey,
				err,
			)
		}
		out[r.RawKey] = v
		if r.Timestamp.After(latestTimestamp) {
			latestTimestamp = r.Timestamp
		}
	}

	// If no timestamp found, try to get the timestamp for this specific slot
	if latestTimestamp.IsZero() {
		var slotTimestamp time.Time
		const slotQuery = `
			SELECT COALESCE(t.blockchain_confirmed_at, t.created_at) as timestamp
			FROM trie t
			WHERE t.slot = ?
			ORDER BY t.id DESC
			LIMIT 1
		`
		if err := d.db.WithContext(ctx).Raw(slotQuery, slot).Scan(&slotTimestamp).Error; err == nil {
			latestTimestamp = slotTimestamp
		} else {
			latestTimestamp = time.Now().UTC() // Keep as fallback for when no DB timestamp is available
		}
	}

	return &ObjectValuesWithTimestamp{
		Values:    out,
		Timestamp: latestTimestamp,
		Slot:      slot,
	}, nil
}

// GetObjectValuesAtTimestampWithSlot returns the key-value pairs for an object as they existed at a specific timestamp,
// along with slot information. The returned slot is the maximum slot among last operations contributing to this snapshot.
// Note: Different keys may have their last operations at different slots <= timestamp
// the returned slot represents the highest slot number among all operations that contribute to the snapshot state.
func (d *Database) GetObjectValuesAtTimestampWithSlot(
	ctx context.Context,
	id string,
	timestamp time.Time,
) (*ObjectValuesWithTimestamp, error) {
	type row struct {
		RawKey string         `gorm:"column:raw_key"`
		Raw    datatypes.JSON `gorm:"column:raw"`
		Slot   int64          `gorm:"column:slot"`
	}

	var rows []row
	const rawQuery = `
		WITH last_ops AS (
			SELECT DISTINCT ON (tok.key_hash)
				tok.key_hash,
				tok.value_hash,
				o.operation_type,
				k.raw_key,
				t.slot
			FROM trie_operation_key tok
			JOIN trie_operation o ON o.id = tok.trie_operation_id
			JOIN trie t           ON t.id = o.trie_id
			JOIN key  k           ON k.key_hash = tok.key_hash
			WHERE k.object_id = ?
				AND COALESCE(t.blockchain_confirmed_at, t.created_at) <= ?
			ORDER BY
				tok.key_hash,
				COALESCE(t.blockchain_confirmed_at, t.created_at) DESC,
				t.slot DESC,
				CASE o.operation_type
				WHEN 'insert' THEN 1
				WHEN 'update' THEN 2
				WHEN 'delete' THEN 3
				END DESC,
				o.sequence_order DESC,
				o.id DESC
		)
		SELECT lo.raw_key, v.raw, lo.slot
		FROM last_ops lo
		JOIN value v ON v.value_hash = lo.value_hash
		WHERE lo.operation_type <> 'delete' AND lo.value_hash IS NOT NULL;
	`
	if err := d.db.WithContext(ctx).Raw(rawQuery, id, timestamp).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to get values for object %s at timestamp %s: %w",
			id, timestamp.Format(time.RFC3339), err,
		)
	}

	out := make(map[string]any, len(rows))
	var maxSlot int64

	for _, r := range rows {
		var v any
		dec := json.NewDecoder(bytes.NewReader(r.Raw))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf(
				"failed to unmarshal value for key %s: %w",
				r.RawKey,
				err,
			)
		}
		out[r.RawKey] = v
		if r.Slot > maxSlot {
			maxSlot = r.Slot
		}
	}

	return &ObjectValuesWithTimestamp{
		Values:    out,
		Timestamp: timestamp,
		Slot:      maxSlot,
	}, nil
}

type CostStatistics struct {
	TotalFees         uint64  `json:"totalFees"`
	AverageFee        float64 `json:"averageFee"`
	MinFee            uint32  `json:"minFee"`
	MaxFee            uint32  `json:"maxFee"`
	TotalTransactions int64   `json:"totalTransactions"`
	LatestSlot        int64   `json:"latestSlot"`
	EarliestSlot      int64   `json:"earliestSlot"`
}

type ObjectValuesWithTimestamp struct {
	Values    map[string]any `json:"values"`
	Timestamp time.Time      `json:"timestamp"`
	Slot      int64          `json:"slot"`
}

// GetCostStatistics returns transaction fee statistics from the trie table
func (d *Database) GetCostStatistics(
	ctx context.Context,
) (*CostStatistics, error) {
	var stats CostStatistics

	err := d.db.WithContext(ctx).
		Model(&Trie{}).
		Select(`
			COALESCE(SUM(tx_fee), 0) as total_fees,
			COALESCE(AVG(tx_fee), 0) as average_fee,
			COALESCE(MIN(tx_fee), 0) as min_fee,
			COALESCE(MAX(tx_fee), 0) as max_fee,
			COUNT(*) as total_transactions,
			COALESCE(MAX(slot), 0) as latest_slot,
			COALESCE(MIN(slot), 0) as earliest_slot
		`).
		Scan(&stats).Error

	if err != nil {
		return nil, fmt.Errorf("failed to get cost statistics: %w", err)
	}

	return &stats, nil
}

// GetActiveObjectIDs returns object IDs that have at least one non-deleted key
func (d *Database) GetActiveObjectIDs(
	ctx context.Context,
	limit, offset int,
) (*GetAllObjectIDsResult, error) {

	var total int64
	// Count distinct active object_ids directly from key
	if err := d.db.WithContext(ctx).
		Table("key").
		Where("deleted_at IS NULL AND NOT is_deleted").
		Distinct("object_id").
		Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count active objects: %w", err)
	}

	var ids []string
	// Page distinct active object_ids
	if err := d.db.WithContext(ctx).
		Table("key").
		Where("deleted_at IS NULL AND NOT is_deleted").
		Distinct().
		Order("object_id ASC").
		Limit(limit).
		Offset(offset).
		Pluck("object_id", &ids).Error; err != nil {
		return nil, fmt.Errorf("failed to get active object IDs: %w", err)
	}

	return &GetAllObjectIDsResult{
		ObjectIDs: ids,
		Total:     total,
		Limit:     limit,
		Offset:    offset,
	}, nil
}

// GetDeletedObjectIDs returns object IDs that have no non-deleted keys
func (d *Database) GetDeletedObjectIDs(
	ctx context.Context,
	limit, offset int,
) (*GetAllObjectIDsResult, error) {

	var totalObjects int64
	if err := d.db.WithContext(ctx).
		Model(&Object{}).
		Count(&totalObjects).Error; err != nil {
		return nil, fmt.Errorf("failed to count objects: %w", err)
	}

	var activeObjects int64
	if err := d.db.WithContext(ctx).
		Table("key").
		Where("deleted_at IS NULL AND NOT is_deleted").
		Distinct("object_id").
		Count(&activeObjects).Error; err != nil {
		return nil, fmt.Errorf("failed to count active objects: %w", err)
	}
	totalDeleted := totalObjects - activeObjects

	// Page deleted object IDs (objects with NO active keys)
	var ids []string
	// NOT EXISTS uses the partial index above on (object_id) for fast checks
	if err := d.db.WithContext(ctx).
		Table(`"object"`). // quoted because the table is named "object"
		Where(`NOT EXISTS (
			SELECT 1
			  FROM key
			 WHERE key.object_id = "object".id
			   AND key.deleted_at IS NULL
			   AND NOT key.is_deleted
		)`).
		Order(`"object".id ASC`).
		Limit(limit).
		Offset(offset).
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("failed to get deleted object IDs: %w", err)
	}

	return &GetAllObjectIDsResult{
		ObjectIDs: ids,
		Total:     totalDeleted,
		Limit:     limit,
		Offset:    offset,
	}, nil
}

type ValueWithTimestamp struct {
	Value     interface{} `json:"value"`
	KeyHash   []byte      `json:"keyHash"`
	Timestamp time.Time   `json:"timestamp"`
	Slot      int64       `json:"slot"`
}

type ValueHashWithTimestamp struct {
	ValueHash []byte    `json:"valueHash"`
	Timestamp time.Time `json:"timestamp"`
	Slot      int64     `json:"slot"`
	Proof     string    `json:"proof"`
}

// GetValueByKey returns the current value for a specific object ID and raw key,
// along with the timestamp and slot of the latest operation affecting this key.
func (d *Database) GetValueByKey(
	ctx context.Context,
	objectID, rawKey string,
) (*ValueWithTimestamp, error) {
	type row struct {
		Raw           datatypes.JSON `gorm:"column:raw"`
		DataTimestamp time.Time      `gorm:"column:data_timestamp"`
		DataSlot      int64          `gorm:"column:data_slot"`
	}

	var result row
	const query = `
		WITH latest_data AS (
			SELECT 
				COALESCE(t.blockchain_confirmed_at, t.created_at) AS data_timestamp,
				t.slot AS data_slot
			FROM trie t
			JOIN trie_operation o ON o.trie_id = t.id
			JOIN trie_operation_key tok ON tok.trie_operation_id = o.id
			JOIN key k2 ON k2.key_hash = tok.key_hash
			WHERE k2.object_id = ? AND k2.raw_key = ?
			ORDER BY COALESCE(t.blockchain_confirmed_at, t.created_at) DESC, t.slot DESC, t.id DESC
			LIMIT 1
		)
		SELECT 
			value.raw,
			COALESCE(ld.data_timestamp, statement_timestamp()) AS data_timestamp,
			COALESCE(ld.data_slot, 0) AS data_slot
		FROM key
		JOIN value ON value.value_hash = key.current_value_hash
		LEFT JOIN latest_data ld ON true
		WHERE key.object_id = ? 
		  AND key.raw_key = ?
		  AND key.deleted_at IS NULL 
		  AND NOT key.is_deleted
	`

	if err := d.db.WithContext(ctx).Raw(query, objectID, rawKey, objectID, rawKey).Scan(&result).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to get value for object %s key %s: %w",
			objectID, rawKey, err,
		)
	}

	if len(result.Raw) == 0 {
		return nil, nil
	}

	var v interface{}
	dec := json.NewDecoder(bytes.NewReader(result.Raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf(
			"failed to unmarshal value for key %s: %w",
			rawKey, err,
		)
	}

	kh := d.KeyHash(objectID, rawKey)

	return &ValueWithTimestamp{
		Value:     v,
		KeyHash:   kh,
		Timestamp: result.DataTimestamp.UTC(),
		Slot:      result.DataSlot,
	}, nil
}

// GetValueHashByKeyHash returns the current value hash for a specific key hash,
// along with the timestamp and slot of the latest operation affecting this key.
func (d *Database) GetValueHashByKeyHash(
	ctx context.Context,
	keyHash []byte,
) (*ValueHashWithTimestamp, error) {
	type row struct {
		ValueHash     []byte    `gorm:"column:current_value_hash"`
		DataTimestamp time.Time `gorm:"column:data_timestamp"`
		DataSlot      int64     `gorm:"column:data_slot"`
	}

	var result row
	const query = `
		WITH latest_data AS (
			SELECT 
				COALESCE(t.blockchain_confirmed_at, t.created_at) AS data_timestamp,
				t.slot AS data_slot
			FROM trie t
			JOIN trie_operation o ON o.trie_id = t.id
			JOIN trie_operation_key tok ON tok.trie_operation_id = o.id
			WHERE tok.key_hash = ?
			ORDER BY COALESCE(t.blockchain_confirmed_at, t.created_at) DESC, t.slot DESC, t.id DESC
			LIMIT 1
		)
		SELECT 
			key.current_value_hash,
			COALESCE(ld.data_timestamp, statement_timestamp()) AS data_timestamp,
			COALESCE(ld.data_slot, 0) AS data_slot
		FROM key
		LEFT JOIN latest_data ld ON true
		WHERE key.key_hash = ?
		  AND key.deleted_at IS NULL 
		  AND NOT key.is_deleted
	`

	if err := d.db.WithContext(ctx).Raw(query, keyHash, keyHash).Scan(&result).Error; err != nil {
		return nil, fmt.Errorf(
			"failed to get value hash for key hash %x: %w",
			keyHash, err,
		)
	}

	if len(result.ValueHash) == 0 {
		return nil, nil
	}

	proof, err := d.ProveTrie(keyHash)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to prove trie for key hash %x: %w",
			keyHash, err,
		)
	}

	proofBytes, err := proof.MarshalCBOR()
	if err != nil {
		return nil, fmt.Errorf(
			"failed to marshal proof for key hash %x: %w",
			keyHash, err,
		)
	}

	return &ValueHashWithTimestamp{
		ValueHash: result.ValueHash,
		Timestamp: result.DataTimestamp.UTC(),
		Slot:      result.DataSlot,
		Proof:     hex.EncodeToString(proofBytes),
	}, nil
}
