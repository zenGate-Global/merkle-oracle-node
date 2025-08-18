package database

import "fmt"

// RollbackCursor removes all cursor entries with slots greater than the provided slot
func (d *Database) RollbackCursor(slot uint64) error {
	result := d.db.Where("slot > ?", slot).Delete(&Cursor{})
	if result.Error != nil {
		return fmt.Errorf("failed to rollback cursor entries: %w", result.Error)
	}
	return nil
}

// Rollback removes all entries with slots greater than the provided slot
func (d *Database) Rollback(slot uint64) error {
	if err := d.db.Exec("SELECT rollback_to_slot(?)", slot).Error; err != nil {
		return err
	}
	// reload trie since rollback could have deleted entries
	if err := d.LoadTrieFromDB(); err != nil {
		return err
	}
	return nil
}
