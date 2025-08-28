package strategy

import (
	"encoding/hex"
	"strings"
	"testing"

	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/logging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestActor(t *testing.T) *ChainEventProcessorActor {
	t.Helper()
	return &ChainEventProcessorActor{
		logger: logging.GetLogger(),
		db:     &database.Database{},
	}
}

func buildMockCurrentState(
	t *testing.T,
	actor *ChainEventProcessorActor,
	existingData map[string]interface{},
) map[string]string {
	t.Helper()
	currentState := make(map[string]string)
	for keyStr, value := range existingData {
		parts := strings.Split(keyStr, ":")
		require.Len(t, parts, 2, "Invalid key format: %s", keyStr)
		objID, key := parts[0], parts[1]

		keyHex := actor.db.KeyHashHex(objID, key)
		valueHex, err := actor.db.ValueHashHex(value)
		require.NoError(t, err)
		currentState[keyHex] = valueHex
	}
	return currentState
}

func getDefaultMockState(
	t *testing.T,
	actor *ChainEventProcessorActor,
) map[string]string {
	existingData := map[string]interface{}{
		"user1:name": "Old Alice",
		"user1:age":  24,
		"user2:name": "Bob",
		"user2:age":  30,
	}
	return buildMockCurrentState(t, actor, existingData)
}

func mustValueHash(
	t *testing.T,
	actor *ChainEventProcessorActor,
	v interface{},
) string {
	t.Helper()
	hash, err := actor.db.ValueHashHex(v)
	require.NoError(t, err)
	return hash
}

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

func TestDiffTrieOps_InsertionsOnly(t *testing.T) {
	actor := newTestActor(t)

	currentState := make(map[string]string)
	oracleData := []map[string]interface{}{
		{"object_id": "user1", "name": "Alice", "age": 25},
	}

	insertions, updates, deletions := actor.diffTrieOpsFromHexState(
		currentState,
		oracleData,
	)

	assert.Len(t, insertions, 2)
	assert.Len(t, updates, 0)
	assert.Len(t, deletions, 0)

	expectedKeys := map[string]bool{
		actor.db.KeyHashHex("user1", "name"): true,
		actor.db.KeyHashHex("user1", "age"):  true,
	}
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
	actor := newTestActor(t)

	currentState := getDefaultMockState(t, actor)

	oracleData := []map[string]interface{}{
		{"object_id": "user1", "name": "Alice Updated", "age": 26},
		{"object_id": "user2", "name": "Bob", "age": 30},
	}

	insertions, updates, deletions := actor.diffTrieOpsFromHexState(
		currentState,
		oracleData,
	)

	assert.Len(t, insertions, 0)
	assert.Len(t, updates, 2)
	assert.Len(t, deletions, 0)

	expectedKeys := map[string]bool{
		actor.db.KeyHashHex("user1", "name"): true,
		actor.db.KeyHashHex("user1", "age"):  true,
	}
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
	actor := newTestActor(t)

	existingData := map[string]interface{}{
		"user1:name": "Alice",
		"user1:age":  25,
	}
	currentState := buildMockCurrentState(t, actor, existingData)

	oracleData := []map[string]interface{}{} // empty oracle data

	insertions, updates, deletions := actor.diffTrieOpsFromHexState(
		currentState,
		oracleData,
	)

	assert.Len(t, insertions, 0)
	assert.Len(t, updates, 0)
	assert.Len(t, deletions, 2)

	expectedKeys := map[string]bool{
		actor.db.KeyHashHex("user1", "name"): true,
		actor.db.KeyHashHex("user1", "age"):  true,
	}
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
	actor := newTestActor(t)

	existingData := map[string]interface{}{
		"user1:name":   "Alice Old",
		"user1:age":    24,
		"user1:city":   "Old City",
		"user2:name":   "Bob",
		"user2:age":    30,
		"user4:name":   "David",
		"user4:status": "inactive",
	}
	currentState := buildMockCurrentState(t, actor, existingData)

	oracleData, _ := createTestData()

	insertions, updates, deletions := actor.diffTrieOpsFromHexState(
		currentState,
		oracleData,
	)

	assert.Greater(t, len(insertions), 0, "Should have some insertions")
	assert.Greater(t, len(updates), 0, "Should have some updates")
	assert.Greater(t, len(deletions), 0, "Should have some deletions")

	user1NameKey := actor.db.KeyHashHex("user1", "name")
	user1EmailKey := actor.db.KeyHashHex("user1", "email")
	user3NameKey := actor.db.KeyHashHex("user3", "name")

	found := false
	for _, update := range updates {
		if update.Key == user1NameKey {
			found = true
			break
		}
	}
	assert.True(t, found, "user1:name should be in updates")

	found = false
	for _, insertion := range insertions {
		if insertion.Key == user1EmailKey {
			found = true
			break
		}
	}
	assert.True(t, found, "user1:email should be in insertions")

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
	actor := newTestActor(t)
	currentState := make(map[string]string)

	t.Run("Empty data", func(t *testing.T) {
		insertions, updates, deletions := actor.diffTrieOpsFromHexState(
			currentState,
			[]map[string]interface{}{},
		)
		assert.Empty(t, insertions)
		assert.Empty(t, updates)
		assert.Empty(t, deletions)
	})

	t.Run("Missing object_id", func(t *testing.T) {
		oracleData := []map[string]interface{}{
			{"name": "Alice", "age": 25},     // no object_id
			{"object_id": "", "name": "Bob"}, // empty object_id
		}
		insertions, updates, deletions := actor.diffTrieOpsFromHexState(
			currentState,
			oracleData,
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
				"object_id": 123,
				"name":      "Alice",
			}, // numeric object_id should be ignored
		}
		insertions, updates, deletions := actor.diffTrieOpsFromHexState(
			currentState,
			oracleData,
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
	actor := newTestActor(t)
	currentState := make(map[string]string)

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

	insertions, updates, deletions := actor.diffTrieOpsFromHexState(
		currentState,
		oracleData,
	)

	assert.Len(t, insertions, 6) // name, age, balance, active, tags, metadata
	assert.Empty(t, updates)
	assert.Empty(t, deletions)

	for _, insertion := range insertions {
		assert.NotEmpty(t, insertion.Key, "Key should not be empty")
		assert.NotEmpty(t, insertion.Value, "Value should not be empty")

		_, err := hex.DecodeString(insertion.Key)
		assert.NoError(t, err, "Key should be valid hex")

		_, err = hex.DecodeString(insertion.Value)
		assert.NoError(t, err, "Value should be valid hex")
	}
}

func TestValueHashHex(t *testing.T) {
	actor := newTestActor(t)

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
			hash, err := actor.db.ValueHashHex(tc.value)
			assert.NoError(t, err)
			assert.NotEmpty(t, hash)

			_, err = hex.DecodeString(hash)
			assert.NoError(t, err)

			hash2, err := actor.db.ValueHashHex(tc.value)
			assert.NoError(t, err)
			assert.Equal(t, hash, hash2, "Same value should produce same hash")
		})
	}
}

func TestKeyHashHex(t *testing.T) {
	actor := newTestActor(t)

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
			hash := actor.db.KeyHashHex(tc.objID, tc.key)
			assert.NotEmpty(t, hash)

			_, err := hex.DecodeString(hash)
			assert.NoError(t, err)

			hash2 := actor.db.KeyHashHex(tc.objID, tc.key)
			assert.Equal(
				t,
				hash,
				hash2,
				"Same objID/key should produce same hash",
			)

			if tc.objID != "" && tc.key != "" {
				differentHash := actor.db.KeyHashHex(tc.objID+"_diff", tc.key)
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

func TestValidateTrieOperations_ValidCase(t *testing.T) {
	actor := newTestActor(t)

	currentData := []map[string]interface{}{
		{
			"object_id": "user1",
			"name":      "Alice Updated",
			"age":       26,
			"email":     "alice@example.com",
		},
		{"object_id": "user2", "name": "Bob"},
	}

	previousDataRaw := map[string]interface{}{
		"user1:name":   "Alice",
		"user1:age":    25,
		"user2:name":   "Bob",
		"user3:status": "inactive",
	}
	previousState := buildMockCurrentState(t, actor, previousDataRaw)

	actualInsertions := []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{
		{
			Key:   actor.db.KeyHashHex("user1", "email"),
			Value: mustValueHash(t, actor, "alice@example.com"),
		},
	}
	actualUpdates := []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{
		{
			Key:   actor.db.KeyHashHex("user1", "name"),
			Value: mustValueHash(t, actor, "Alice Updated"),
		},
		{
			Key:   actor.db.KeyHashHex("user1", "age"),
			Value: mustValueHash(t, actor, 26),
		},
	}
	actualDeletions := []struct {
		Key string `json:"key"`
	}{
		{Key: actor.db.KeyHashHex("user3", "status")},
	}

	report, err := actor.validateTrieOperations(
		currentData,
		previousState,
		actualInsertions,
		actualUpdates,
		actualDeletions,
	)

	assert.NoError(t, err)
	assert.True(t, report.Valid)
	assert.Empty(t, report.MissingInsertions)
	assert.Empty(t, report.UnexpectedInsertions)
	assert.Empty(t, report.MissingUpdates)
	assert.Empty(t, report.UnexpectedUpdates)
	assert.Empty(t, report.MissingDeletions)
	assert.Empty(t, report.UnexpectedDeletions)
	assert.Empty(t, report.Errors)
}

func TestValidateTrieOperations_InvalidCase(t *testing.T) {
	actor := newTestActor(t)

	currentData := []map[string]interface{}{
		{"object_id": "user1", "name": "Alice", "age": 25},
	}
	previousState := make(map[string]string)

	// wrong: provide updates instead of insertions
	actualInsertions := []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{}
	actualUpdates := []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}{
		{
			Key:   actor.db.KeyHashHex("user1", "name"),
			Value: mustValueHash(t, actor, "Alice"),
		},
		{
			Key:   actor.db.KeyHashHex("user1", "age"),
			Value: mustValueHash(t, actor, 25),
		},
	}
	actualDeletions := []struct {
		Key string `json:"key"`
	}{}

	report, err := actor.validateTrieOperations(
		currentData,
		previousState,
		actualInsertions,
		actualUpdates,
		actualDeletions,
	)

	assert.NoError(t, err)
	assert.False(t, report.Valid)
	assert.Len(t, report.MissingInsertions, 2)
	assert.Len(t, report.UnexpectedUpdates, 2)
}
