package database

import (
	"fmt"
	"sync"

	mpf "github.com/blinklabs-io/merkle-patricia-forestry"
)

// MemoryTrie provides in-memory access to trie data, loads from DB but keeps updates in memory only
type MemoryTrie struct {
	db    *Database
	trie  *mpf.Trie
	mutex sync.RWMutex
}

// GetInMemoryTrie creates a new MemoryTrie instance loaded from the database
func (d *Database) GetInMemoryTrie() (*MemoryTrie, error) {
	memTrie := &MemoryTrie{
		db:   d,
		trie: mpf.NewTrie(),
	}

	// Load trie structure from database
	if err := memTrie.loadTrieFromDB(); err != nil {
		return nil, fmt.Errorf("failed to load trie from database: %w", err)
	}

	return memTrie, nil
}

// loadTrieFromDB loads trie structure from database
func (mt *MemoryTrie) loadTrieFromDB() error {
	// Load active key/value pairs from Keys table
	type leaf struct {
		KeyHash          []byte `gorm:"column:key_hash"`
		CurrentValueHash []byte `gorm:"column:current_value_hash"`
	}

	var leaves []leaf
	if err := mt.db.db.Table((Key{}).TableName()).
		Select("key_hash, current_value_hash").
		Where("is_deleted = ? AND current_value_hash IS NOT NULL", false).
		Scan(&leaves).Error; err != nil {
		return err
	}

	mt.trie = mpf.NewTrie()
	for _, l := range leaves {
		if len(l.KeyHash) == 0 || len(l.CurrentValueHash) == 0 {
			continue
		}
		mt.trie.Set(l.KeyHash, l.CurrentValueHash)
	}

	return nil
}

// Update adds or updates an account in memory only (does not persist to database)
func (mt *MemoryTrie) Update(key []byte, value []byte, slot uint64) error {
	mt.mutex.Lock()
	defer mt.mutex.Unlock()

	// Update trie in memory
	mt.trie.Set(key, value)

	return nil
}

func (mt *MemoryTrie) Delete(key []byte) error {
	mt.mutex.Lock()
	defer mt.mutex.Unlock()

	_ = mt.trie.Delete(key)

	return nil
}

func (mt *MemoryTrie) Hash() []byte {
	mt.mutex.RLock()
	defer mt.mutex.RUnlock()

	hash := mt.trie.Hash().Bytes()
	return append([]byte(nil), hash...)
}

func (mt *MemoryTrie) Prove(key []byte) (*mpf.Proof, error) {
	mt.mutex.RLock()
	defer mt.mutex.RUnlock()

	return mt.trie.Prove(key)
}

func (mt *MemoryTrie) Has(key []byte) bool {
	mt.mutex.RLock()
	defer mt.mutex.RUnlock()

	return mt.trie.Has(key)
}

func (mt *MemoryTrie) GetTrie() *mpf.Trie {
	mt.mutex.RLock()
	defer mt.mutex.RUnlock()
	return mt.trie
}
