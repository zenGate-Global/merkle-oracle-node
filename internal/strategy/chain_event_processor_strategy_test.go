package strategy

import (
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"

	"zenGate-Global/merkle-oracle-node/internal/logging"

	mpf "github.com/blinklabs-io/merkle-patricia-forestry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/blake2b"
)

func hashBlake2b(value []byte) []byte {
	tmpHash, err := blake2b.New256(nil)
	if err != nil {
		panic(err.Error())
	}
	tmpHash.Write(value)
	return tmpHash.Sum(nil)
}

type testTrie struct {
	trie  *mpf.Trie
	mutex sync.RWMutex
}

func newTestTrie() *testTrie {
	return &testTrie{
		trie: mpf.NewTrie(),
	}
}

func (t *testTrie) Hash() []byte {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	hash := t.trie.Hash().Bytes()
	return append([]byte(nil), hash...)
}

func (t *testTrie) Has(key []byte) bool {
	t.mutex.RLock()
	defer t.mutex.RUnlock()

	return t.trie.Has(key)
}

func (t *testTrie) Update(key []byte, value []byte, slot uint64) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	t.trie.Set(key, value)
	return nil
}

func (t *testTrie) Delete(key []byte) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	_ = t.trie.Delete(key)
	return nil
}

// wraps ChainEventProcessorActor for testing
type testActor struct {
	*ChainEventProcessorActor
}

func (ta *testActor) keyHashHex(objID, key string) string {
	return hex.EncodeToString(hashBlake2b([]byte(objID + ":" + key)))
}

func (ta *testActor) valueHashHex(v interface{}) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hashBlake2b(b)), nil
}

func createTestActor(t *testing.T) *testActor {
	logger := logging.GetLogger()
	return &testActor{
		ChainEventProcessorActor: &ChainEventProcessorActor{
			logger: logger,
		},
	}
}

// creates test data for oracle and cloud data
func createTestData() ([]map[string]interface{}, []map[string]interface{}) {
	oracleData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice",
			"age":       25,
			"email":     "alice@example.com",
		},
		{
			"object_id": "user2",
			"name":      "Bob",
			"age":       30,
		},
		{
			"object_id": "user3",
			"name":      "Charlie",
			"balance":   100.50,
		},
	}

	currentCloudData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice Old",
			"age":       24,         // different age - should be update
			"city":      "Old City", // this key exists in cloud but not in oracle - should be deletion
		},
		{
			"object_id": "user2",
			"name":      "Bob",
			"age":       30, // same data - should be update (since key exists in trie)
		},
		{
			"object_id": "user4", // this user exists in cloud but not in oracle - should be deletion
			"name":      "David",
			"status":    "inactive",
		},
	}

	return oracleData, currentCloudData
}

// prefillTrie adds some existing data to the trie to test insert vs update logic
func prefillTrie(t *testing.T, actor *testActor, memTrie trieLike) {
	// Add some existing keys to test update scenario
	existingData := map[string]string{
		"user1:name": "Old Alice",
		"user1:age":  "24",
		"user2:name": "Bob",
		"user2:age":  "30",
	}

	for keyStr, valueStr := range existingData {
		keyHash := hashBlake2b([]byte(keyStr))
		valueHash := hashBlake2b(
			[]byte(`"` + valueStr + `"`),
		) // JSON encode the value
		err := memTrie.Update(keyHash, valueHash, 0)
		require.NoError(t, err)
	}
}

func TestDiffTrieOps_InsertionsOnly(t *testing.T) {
	actor := createTestActor(t)

	// Create empty trie
	memTrie := newTestTrie()

	oracleData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice",
			"age":       25,
		},
	}

	currentCloudData := []map[string]interface{}{} // empty cloud data

	insertions, updates, deletions := actor.diffTrieOps(
		memTrie,
		oracleData,
		currentCloudData,
	)

	// Should have 2 insertions (name and age), no updates or deletions
	assert.Len(t, insertions, 2)
	assert.Len(t, updates, 0)
	assert.Len(t, deletions, 0)

	// Verify the keys are correct
	expectedKeys := make(map[string]bool)
	expectedKeys[actor.keyHashHex("user1", "name")] = true
	expectedKeys[actor.keyHashHex("user1", "age")] = true

	for _, insertion := range insertions {
		assert.True(
			t,
			expectedKeys[insertion.Key],
			"Unexpected insertion key: %s",
			insertion.Key,
		)
		delete(expectedKeys, insertion.Key)
	}
	assert.Empty(t, expectedKeys, "Expected keys not found in insertions")
}

func TestDiffTrieOps_UpdatesOnly(t *testing.T) {
	actor := createTestActor(t)

	// Create trie with existing data
	memTrie := newTestTrie()
	prefillTrie(t, actor, memTrie)

	oracleData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice Updated",
			"age":       26, // updated age
		},
	}

	currentCloudData := []map[string]interface{}{} // empty cloud data

	insertions, updates, deletions := actor.diffTrieOps(
		memTrie,
		oracleData,
		currentCloudData,
	)

	// Should have 2 updates (name and age), no insertions or deletions
	assert.Len(t, insertions, 0)
	assert.Len(t, updates, 2)
	assert.Len(t, deletions, 0)

	// Verify the keys are correct
	expectedKeys := make(map[string]bool)
	expectedKeys[actor.keyHashHex("user1", "name")] = true
	expectedKeys[actor.keyHashHex("user1", "age")] = true

	for _, update := range updates {
		assert.True(
			t,
			expectedKeys[update.Key],
			"Unexpected update key: %s",
			update.Key,
		)
		delete(expectedKeys, update.Key)
	}
	assert.Empty(t, expectedKeys, "Expected keys not found in updates")
}

func TestDiffTrieOps_DeletionsOnly(t *testing.T) {
	actor := createTestActor(t)

	// Create empty trie
	memTrie := newTestTrie()

	oracleData := []map[string]interface{}{} // empty oracle data

	currentCloudData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice",
			"age":       25,
		},
	}

	insertions, updates, deletions := actor.diffTrieOps(
		memTrie,
		oracleData,
		currentCloudData,
	)

	// Should have 2 deletions (name and age), no insertions or updates
	assert.Len(t, insertions, 0)
	assert.Len(t, updates, 0)
	assert.Len(t, deletions, 2)

	// Verify the keys are correct
	expectedKeys := make(map[string]bool)
	expectedKeys[actor.keyHashHex("user1", "name")] = true
	expectedKeys[actor.keyHashHex("user1", "age")] = true

	for _, deletion := range deletions {
		assert.True(
			t,
			expectedKeys[deletion.Key],
			"Unexpected deletion key: %s",
			deletion.Key,
		)
		delete(expectedKeys, deletion.Key)
	}
	assert.Empty(t, expectedKeys, "Expected keys not found in deletions")
}

func TestDiffTrieOps_MixedOperations(t *testing.T) {
	actor := createTestActor(t)

	// Create trie with some existing data
	memTrie := newTestTrie()
	prefillTrie(t, actor, memTrie)

	oracleData, currentCloudData := createTestData()

	insertions, updates, deletions := actor.diffTrieOps(
		memTrie,
		oracleData,
		currentCloudData,
	)

	// Verify we have the expected operations
	assert.Greater(t, len(insertions), 0, "Should have some insertions")
	assert.Greater(t, len(updates), 0, "Should have some updates")
	assert.Greater(t, len(deletions), 0, "Should have some deletions")

	// Verify specific expectations:
	// user1:name and user1:age should be updates (exist in prefilled trie)
	// user1:email should be insertion (new key)
	// user2:name and user2:age should be updates (exist in prefilled trie)
	// user3:name and user3:balance should be insertions (new user)
	// user1:city and user4:* should be deletions (in cloud but not oracle)

	// Check some specific keys
	user1NameKey := actor.keyHashHex("user1", "name")
	user1EmailKey := actor.keyHashHex("user1", "email")
	user3NameKey := actor.keyHashHex("user3", "name")

	// user1:name should be in updates
	found := false
	for _, update := range updates {
		if update.Key == user1NameKey {
			found = true
			break
		}
	}
	assert.True(t, found, "user1:name should be in updates")

	// user1:email should be in insertions
	found = false
	for _, insertion := range insertions {
		if insertion.Key == user1EmailKey {
			found = true
			break
		}
	}
	assert.True(t, found, "user1:email should be in insertions")

	// user3:name should be in insertions
	found = false
	for _, insertion := range insertions {
		if insertion.Key == user3NameKey {
			found = true
			break
		}
	}
	assert.True(t, found, "user3:name should be in insertions")
}

func TestDiffTrieOps_EdgeCases(t *testing.T) {
	actor := createTestActor(t)

	memTrie := newTestTrie()

	t.Run("Empty data", func(t *testing.T) {
		insertions, updates, deletions := actor.diffTrieOps(
			memTrie,
			[]map[string]interface{}{},
			[]map[string]interface{}{},
		)
		assert.Empty(t, insertions)
		assert.Empty(t, updates)
		assert.Empty(t, deletions)
	})

	t.Run("Missing object_id", func(t *testing.T) {
		oracleData := []map[string]interface{}{
			{
				"name": "Alice", // no object_id
				"age":  25,
			},
			{
				"object_id": "", // empty object_id
				"name":      "Bob",
			},
		}

		insertions, updates, deletions := actor.diffTrieOps(
			memTrie,
			oracleData,
			[]map[string]interface{}{},
		)
		assert.Empty(
			t,
			insertions,
			"Should ignore items without valid object_id",
		)
		assert.Empty(t, updates)
		assert.Empty(t, deletions)
	})

	t.Run("Non-string object_id", func(t *testing.T) {
		oracleData := []map[string]interface{}{
			{
				"object_id": 123, // numeric object_id should be ignored
				"name":      "Alice",
			},
		}

		insertions, updates, deletions := actor.diffTrieOps(
			memTrie,
			oracleData,
			[]map[string]interface{}{},
		)
		assert.Empty(
			t,
			insertions,
			"Should ignore items with non-string object_id",
		)
		assert.Empty(t, updates)
		assert.Empty(t, deletions)
	})
}

func TestDiffTrieOps_ValueHashing(t *testing.T) {
	actor := createTestActor(t)

	memTrie := newTestTrie()

	oracleData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice",
			"age":       25,
			"balance":   100.50,
			"active":    true,
			"tags":      []string{"admin", "user"},
			"metadata":  map[string]interface{}{"created": "2023-01-01"},
		},
	}

	insertions, updates, deletions := actor.diffTrieOps(
		memTrie,
		oracleData,
		[]map[string]interface{}{},
	)

	assert.Len(t, insertions, 6) // name, age, balance, active, tags, metadata
	assert.Empty(t, updates)
	assert.Empty(t, deletions)

	// Verify that values are properly JSON-encoded and hashed
	for _, insertion := range insertions {
		assert.NotEmpty(t, insertion.Key, "Key should not be empty")
		assert.NotEmpty(t, insertion.Value, "Value should not be empty")

		// Verify key is valid hex
		_, err := hex.DecodeString(insertion.Key)
		assert.NoError(t, err, "Key should be valid hex")

		// Verify value is valid hex
		_, err = hex.DecodeString(insertion.Value)
		assert.NoError(t, err, "Value should be valid hex")
	}
}

func TestValueHashHex(t *testing.T) {
	actor := createTestActor(t)

	testCases := []struct {
		name  string
		value interface{}
	}{
		{"string", "hello"},
		{"int", 42},
		{"float", 3.14},
		{"bool", true},
		{"array", []string{"a", "b", "c"}},
		{"object", map[string]interface{}{"key": "value"}},
		{"null", nil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := actor.valueHashHex(tc.value)
			assert.NoError(t, err)
			assert.NotEmpty(t, hash)

			// Verify it's valid hex
			_, err = hex.DecodeString(hash)
			assert.NoError(t, err)

			// Verify consistency
			hash2, err := actor.valueHashHex(tc.value)
			assert.NoError(t, err)
			assert.Equal(t, hash, hash2, "Same value should produce same hash")
		})
	}
}

func TestKeyHashHex(t *testing.T) {
	actor := createTestActor(t)

	testCases := []struct {
		objID string
		key   string
	}{
		{"user1", "name"},
		{"user1", "age"},
		{"account123", "balance"},
		{"", "key"},   // empty objID
		{"objID", ""}, // empty key
	}

	for _, tc := range testCases {
		t.Run(tc.objID+"_"+tc.key, func(t *testing.T) {
			hash := actor.keyHashHex(tc.objID, tc.key)
			assert.NotEmpty(t, hash)

			// Verify it's valid hex
			_, err := hex.DecodeString(hash)
			assert.NoError(t, err)

			// Verify consistency
			hash2 := actor.keyHashHex(tc.objID, tc.key)
			assert.Equal(
				t,
				hash,
				hash2,
				"Same objID/key should produce same hash",
			)

			// Verify different inputs produce different hashes
			if tc.objID != "" && tc.key != "" {
				differentHash := actor.keyHashHex(tc.objID+"_diff", tc.key)
				assert.NotEqual(
					t,
					hash,
					differentHash,
					"Different inputs should produce different hashes",
				)
			}
		})
	}
}
