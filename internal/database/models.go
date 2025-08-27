package database

import "gorm.io/gorm"

const sqlCreateEnum = `
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'op_type') THEN
    CREATE TYPE op_type AS ENUM ('insert','update','delete');
  END IF;
END$$;
`

const sqlIndexes = `
CREATE UNIQUE INDEX IF NOT EXISTS uix_oracle_file_cid ON oracle_file (cid);
CREATE INDEX IF NOT EXISTS oracle_file_prev_idx ON oracle_file (previous_cid);
CREATE INDEX IF NOT EXISTS trie_confirmed_idx  ON trie (blockchain_confirmed_at);
CREATE INDEX IF NOT EXISTS trie_slot_idx       ON trie (slot);
CREATE INDEX IF NOT EXISTS value_raw_gin       ON value USING GIN (raw jsonb_path_ops);
CREATE INDEX IF NOT EXISTS key_object_raw_idx  ON key (object_id, raw_key);
CREATE INDEX IF NOT EXISTS key_current_val_idx ON key (current_value_hash);
CREATE INDEX IF NOT EXISTS key_not_deleted_idx ON key (key_hash) WHERE is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS key_active_by_object_idx ON key (object_id) WHERE deleted_at IS NULL AND is_deleted = FALSE;
CREATE INDEX IF NOT EXISTS trie_op_apply_idx   ON trie_operation (trie_id, operation_type, sequence_order);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_trie_op ON trie_operation (trie_id, operation_type, sequence_order);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_tok_op_key ON trie_operation_key (trie_operation_id, key_hash);
`

const sqlForeignKeys = `
-- foreign keys (almost all ON DELETE CASCADE)
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_trie_oracle_file') THEN
    ALTER TABLE trie ADD CONSTRAINT fk_trie_oracle_file FOREIGN KEY (oracle_file_id) REFERENCES oracle_file(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_object_first_seen') THEN
    ALTER TABLE object ADD CONSTRAINT fk_object_first_seen FOREIGN KEY (first_seen_file_id) REFERENCES oracle_file(id) ON DELETE SET NULL;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_object_last_seen') THEN
    ALTER TABLE object ADD CONSTRAINT fk_object_last_seen FOREIGN KEY (last_seen_file_id) REFERENCES oracle_file(id) ON DELETE SET NULL;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_oif_object') THEN
    ALTER TABLE object_in_file ADD CONSTRAINT fk_oif_object FOREIGN KEY (object_id) REFERENCES object(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_oif_oracle_file') THEN
    ALTER TABLE object_in_file ADD CONSTRAINT fk_oif_oracle_file FOREIGN KEY (oracle_file_id) REFERENCES oracle_file(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_key_object') THEN
    ALTER TABLE key ADD CONSTRAINT fk_key_object FOREIGN KEY (object_id) REFERENCES object(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_key_value') THEN
    ALTER TABLE key ADD CONSTRAINT fk_key_value FOREIGN KEY (current_value_hash) REFERENCES value(value_hash) ON DELETE RESTRICT DEFERRABLE INITIALLY DEFERRED;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_trie_op_trie') THEN
    ALTER TABLE trie_operation ADD CONSTRAINT fk_trie_op_trie FOREIGN KEY (trie_id) REFERENCES trie(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_tok_trie_op') THEN
    ALTER TABLE trie_operation_key ADD CONSTRAINT fk_tok_trie_op FOREIGN KEY (trie_operation_id) REFERENCES trie_operation(id) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_tok_key') THEN
    ALTER TABLE trie_operation_key ADD CONSTRAINT fk_tok_key FOREIGN KEY (key_hash) REFERENCES key(key_hash) ON DELETE CASCADE;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'fk_tok_value') THEN
    ALTER TABLE trie_operation_key ADD CONSTRAINT fk_tok_value FOREIGN KEY (value_hash) REFERENCES value(value_hash) ON DELETE SET NULL;
  END IF;
END$$;
`

const sqlTokFunction = `
CREATE OR REPLACE FUNCTION tok_enforce_and_apply() RETURNS trigger AS $$
DECLARE
  op op_type;
BEGIN
  SELECT operation_type INTO op
  FROM trie_operation
  WHERE id = NEW.trie_operation_id;

  -- Enforce presence/absence of value_hash by op type
  IF op IN ('insert','update') AND NEW.value_hash IS NULL THEN
    RAISE EXCEPTION 'value_hash must be set for %', op;
  ELSIF op = 'delete' AND NEW.value_hash IS NOT NULL THEN
    RAISE EXCEPTION 'value_hash must be NULL for delete';
  END IF;

  -- Maintain current state in key
  IF op = 'delete' THEN
    UPDATE key
       SET current_value_hash = NULL,
           is_deleted         = TRUE,
           deleted_at         = now(),
           updated_at         = now()
     WHERE key_hash = NEW.key_hash;
  ELSE
    UPDATE key
       SET current_value_hash = NEW.value_hash,
           is_deleted         = FALSE,
           deleted_at         = NULL,
           updated_at         = now()
     WHERE key_hash = NEW.key_hash;
  END IF;

  RETURN NEW;
END
$$ LANGUAGE plpgsql;
`

const sqlTokTrigger = `
DROP TRIGGER IF EXISTS trg_tok_enforce_and_apply ON trie_operation_key;
CREATE TRIGGER trg_tok_enforce_and_apply
AFTER INSERT ON trie_operation_key
FOR EACH ROW EXECUTE FUNCTION tok_enforce_and_apply();
`

const sqlRollbackFunction = `
CREATE OR REPLACE FUNCTION rollback_to_slot(cutoff_slot BIGINT)
RETURNS VOID AS $$
DECLARE
  dummy integer;
BEGIN
  PERFORM pg_advisory_xact_lock(987654321);

  WITH
  doomed_trie AS (
    SELECT id, oracle_file_id
    FROM trie
    WHERE slot > cutoff_slot
  ),
  doomed_ops AS (
    SELECT o.id
    FROM trie_operation o
    JOIN doomed_trie dt ON dt.id = o.trie_id
  ),
  doomed_files AS (
    SELECT DISTINCT oracle_file_id AS id FROM doomed_trie
  ),
  affected_keys AS (
    SELECT DISTINCT tok.key_hash
    FROM trie_operation_key tok
    JOIN doomed_ops do2 ON do2.id = tok.trie_operation_id
  ),
  del_tok AS (
    DELETE FROM trie_operation_key tok
    USING doomed_ops do2
    WHERE tok.trie_operation_id = do2.id
    RETURNING 1
  ),
  del_op AS (
    DELETE FROM trie_operation o
    USING doomed_trie dt
    WHERE o.trie_id = dt.id
    RETURNING 1
  ),
  del_trie AS (
    DELETE FROM trie t
    USING doomed_trie dt
    WHERE t.id = dt.id
    RETURNING 1
  ),
  del_oif AS (
    DELETE FROM object_in_file oif
    USING doomed_files df
    WHERE oif.oracle_file_id = df.id
    RETURNING 1
  ),
  del_of AS (
    DELETE FROM oracle_file ofi
    USING doomed_files df
    WHERE ofi.id = df.id
    RETURNING 1
  ),
  last_ops AS (
    SELECT DISTINCT ON (tok.key_hash)
           tok.key_hash,
           op.operation_type,
           tok.value_hash
    FROM trie_operation_key tok
    JOIN trie_operation op ON op.id = tok.trie_operation_id
    JOIN trie t            ON t.id  = op.trie_id
    WHERE t.slot <= cutoff_slot
    ORDER BY tok.key_hash,
             t.slot DESC,
             CASE op.operation_type
               WHEN 'insert' THEN 1
               WHEN 'update' THEN 2
               WHEN 'delete' THEN 3
             END DESC,
             op.sequence_order DESC,
             op.id DESC
  ),
  up_keys AS (
    UPDATE key k
    SET current_value_hash = CASE WHEN l.operation_type = 'delete' THEN NULL ELSE l.value_hash END,
        is_deleted         = CASE WHEN l.operation_type = 'delete' THEN TRUE ELSE FALSE END,
        deleted_at         = CASE WHEN l.operation_type = 'delete' THEN now() ELSE NULL END,
        updated_at         = now()
    FROM last_ops l
    WHERE k.key_hash = l.key_hash
      AND k.key_hash IN (SELECT key_hash FROM affected_keys)
    RETURNING 1
  ),
  up_missing AS (
    UPDATE key k
    SET current_value_hash = NULL,
        is_deleted         = TRUE,
        deleted_at         = now(),
        updated_at         = now()
    WHERE k.key_hash IN (
      SELECT ak.key_hash FROM affected_keys ak
      EXCEPT
      SELECT lo.key_hash FROM last_ops lo
    )
    RETURNING 1
  ),
  upd_obj_seen AS (
    UPDATE object o
    SET first_seen_file_id = sub.first_file_id,
        last_seen_file_id  = sub.last_file_id,
        updated_at         = now()
    FROM (
      SELECT oi.object_id,
             (SELECT of2.id
                FROM object_in_file oi2
                JOIN oracle_file of2 ON of2.id = oi2.oracle_file_id
               WHERE oi2.object_id = oi.object_id
               ORDER BY of2.created_at ASC
               LIMIT 1) AS first_file_id,
             (SELECT of3.id
                FROM object_in_file oi3
                JOIN oracle_file of3 ON of3.id = oi3.oracle_file_id
               WHERE oi3.object_id = oi.object_id
               ORDER BY of3.created_at DESC
               LIMIT 1) AS last_file_id
      FROM object_in_file oi
      GROUP BY oi.object_id
    ) sub
    WHERE o.id = sub.object_id
    RETURNING 1
  ),
  null_obj_seen AS (
    UPDATE object o
    SET first_seen_file_id = NULL,
        last_seen_file_id  = NULL,
        updated_at         = now()
    WHERE NOT EXISTS (
      SELECT 1 FROM object_in_file oi WHERE oi.object_id = o.id
    )
    RETURNING 1
  ),
  gc_values AS (
    DELETE FROM value v
    WHERE NOT EXISTS (
            SELECT 1
            FROM key k
            WHERE k.current_value_hash = v.value_hash
          )
      AND NOT EXISTS (
            SELECT 1
            FROM trie_operation_key tok
            WHERE tok.value_hash = v.value_hash
          )
    RETURNING 1
  )
  SELECT 1 INTO dummy;
END;
$$ LANGUAGE plpgsql;
`

func Migrate(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		// enum must exist before AutoMigrate (since TrieOperation.OperationType uses it)
		if err := tx.Exec(sqlCreateEnum).Error; err != nil {
			return err
		}

		// create tables from structs
		if err := tx.AutoMigrate(
			&Cursor{},
			&OracleFile{},
			&Trie{},
			&Object{},
			&ObjectInFile{},
			&Value{},
			&Key{},
			&TrieOperation{},
			&TrieOperationKey{},
		); err != nil {
			return err
		}

		// index creation
		if err := tx.Exec(sqlIndexes).Error; err != nil {
			return err
		}

		// fk creation
		if err := tx.Exec(sqlForeignKeys).Error; err != nil {
			return err
		}

		// trigger and trigger function
		if err := tx.Exec(sqlTokFunction).Error; err != nil {
			return err
		}
		if err := tx.Exec(sqlTokTrigger).Error; err != nil {
			return err
		}

		// 6) rollback function
		if err := tx.Exec(sqlRollbackFunction).Error; err != nil {
			return err
		}

		return nil
	})
}
