package database

import (
	"errors"
	"sync"

	mpf "github.com/blinklabs-io/merkle-patricia-forestry"
	"golang.org/x/crypto/blake2b"
)

var (
	trieMtx    sync.RWMutex
	memoryTrie *mpf.Trie
)

// LoadTrieFromDB rebuilds the in-memory trie from current key/value state
func (d *Database) LoadTrieFromDB() error {
	type leaf struct {
		KeyHash          []byte `gorm:"column:key_hash"`
		CurrentValueHash []byte `gorm:"column:current_value_hash"`
	}

	var leaves []leaf
	if err := d.db.Table((Key{}).TableName()).
		Select("key_hash, current_value_hash").
		Where("is_deleted = ? AND current_value_hash IS NOT NULL", false).
		Scan(&leaves).Error; err != nil {
		return err
	}

	trieMtx.Lock()
	defer trieMtx.Unlock()
	memoryTrie = mpf.NewTrie()
	for _, l := range leaves {
		if len(l.KeyHash) == 0 || len(l.CurrentValueHash) == 0 {
			continue
		}
		memoryTrie.Set(l.KeyHash, l.CurrentValueHash)
	}
	return nil
}

func (d *Database) UpdateTrie(key []byte, value []byte, slot uint64) error {
	trieMtx.Lock()
	defer trieMtx.Unlock()

	if memoryTrie == nil {
		memoryTrie = mpf.NewTrie()
	}
	memoryTrie.Set(key, value)
	return nil
}

func (d *Database) DeleteTrieEntry(key []byte) error {
	trieMtx.Lock()
	defer trieMtx.Unlock()

	if memoryTrie != nil {
		return memoryTrie.Delete(key)
	}
	return nil
}

// GetTrieHash returns the current root hash of the trie
func (d *Database) GetTrieHash() []byte {
	trieMtx.RLock()
	defer trieMtx.RUnlock()

	if memoryTrie == nil {
		return nil
	}
	hash := memoryTrie.Hash().Bytes()
	return append([]byte(nil), hash...)
}

// ProveTrie generates a proof for a given key
func (d *Database) ProveTrie(key []byte) (*mpf.Proof, error) {
	trieMtx.RLock()
	defer trieMtx.RUnlock()

	if memoryTrie == nil {
		return nil, errors.New("trie not initialised")
	}
	return memoryTrie.Prove(key)
}

// Hash hashes a key using Blake2b
func (d *Database) Hash(value []byte) []byte {
	tmpHash, err := blake2b.New256(nil)
	if err != nil {
		panic(err.Error())
	}
	tmpHash.Write(value)
	return tmpHash.Sum(nil)
}

// GetTrie returns the in-memory trie instance
func (d *Database) GetTrie() *mpf.Trie {
	trieMtx.RLock()
	defer trieMtx.RUnlock()
	return memoryTrie
}

func (d *Database) GarbageCollectTrie(cutoff uint64) error {
	// TODO: implement garbage collection
	return nil
}
