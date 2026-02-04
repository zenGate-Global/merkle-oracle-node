package database

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
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

func (d *Database) KeyHash(objectID, key string) []byte {
	hash := blake2b.Sum256([]byte(objectID + ":" + key))
	return hash[:]
}

func (d *Database) ValueHash(value interface{}) ([]byte, error) {
	marshaled, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	canon, err := CanonicalizeJSON(json.RawMessage(marshaled))
	if err != nil {
		return nil, err
	}

	hash := blake2b.Sum256(canon)
	return hash[:], nil
}

func (d *Database) KeyHashHex(objectID, key string) string {
	return hex.EncodeToString(d.KeyHash(objectID, key))
}

func (d *Database) ValueHashHex(value interface{}) (string, error) {
	hashBytes, err := d.ValueHash(value)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hashBytes), nil
}

var jsonNumberRE = regexp.MustCompile(
	`^[+-]?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$`,
)

// CanonicalizeJSON normalizes JSON primitives to a stable byte representation.
// numbers become fraction *strings* ("num/den"), integers become "n/1",
// numeric strings become fraction strings too. Non-numeric strings remain strings.
func CanonicalizeJSON(raw json.RawMessage) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := EncodeCanonical(v, &buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func EncodeCanonical(v any, b *bytes.Buffer) error {
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
		fs, err := fractionStringFromJSONNumber(t)
		if err != nil {
			return fmt.Errorf("invalid json number %q: %w", string(t), err)
		}
		enc, _ := json.Marshal(fs)
		b.Write(enc)

	case string:
		// if whole string is a number, normalize it
		if fs, ok := fractionStringIfNumeric(t); ok {
			enc, _ := json.Marshal(fs)
			b.Write(enc)
			return nil
		}
		enc, _ := json.Marshal(t)
		b.Write(enc)

	case []interface{}:
		b.WriteByte('[')
		for i, elem := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := EncodeCanonical(elem, b); err != nil {
				return err
			}
		}
		b.WriteByte(']')

	case map[string]interface{}:
		b.WriteByte('{')
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			keyEnc, _ := json.Marshal(k)
			b.Write(keyEnc)
			b.WriteByte(':')
			if err := EncodeCanonical(t[k], b); err != nil {
				return err
			}
		}
		b.WriteByte('}')

	default:
		return fmt.Errorf("unsupported JSON value type: %T", v)
	}
	return nil
}

// fractionStringFromJSONNumber converts a json.Number to a reduced "num/den" string.
// accepts integers, decimals, and scientific notation like 1e-3.
func fractionStringFromJSONNumber(n json.Number) (string, error) {
	return fractionStringFromString(n.String())
}

// fractionStringIfNumeric returns "num/den" if s is *exactly* a JSON number; else false.
// (e.g., "32.06" -> "1603/50", "2" -> "2/1", "I like 2" -> false)
func fractionStringIfNumeric(s string) (string, bool) {
	fs, err := fractionStringFromString(s)
	if err != nil {
		return "", false
	}
	return fs, true
}

func fractionStringFromString(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !jsonNumberRE.MatchString(s) {
		return "", fmt.Errorf("not a pure JSON number")
	}
	r, ok := parseJSONNumberToRat(s)
	if !ok {
		return "", fmt.Errorf("failed to parse %q", s)
	}
	return r.Num().String() + "/" + r.Denom().String(), nil
}

// parseJSONNumberToRat parses a JSON number string (int/dec/sci) into a reduced *big.Rat*.
func parseJSONNumberToRat(s string) (*big.Rat, bool) {
	// sign
	sign := 1
	if strings.HasPrefix(s, "+") {
		s = s[1:]
	} else if strings.HasPrefix(s, "-") {
		sign = -1
		s = s[1:]
	}

	// split exponent
	var exp int64
	if i := strings.IndexAny(s, "eE"); i != -1 {
		e, err := strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil {
			return nil, false
		}
		exp = e
		s = s[:i]
	}

	// split fractional part
	decimals := 0
	if j := strings.IndexByte(s, '.'); j != -1 {
		decimals = len(s) - j - 1
		s = s[:j] + s[j+1:] // remove dot
	}

	// strip leading zeros from integer digits (but keep at least "0")
	digits := strings.TrimLeft(s, "0")
	if digits == "" {
		digits = "0"
	}

	num := new(big.Int)
	if _, ok := num.SetString(digits, 10); !ok {
		return nil, false
	}
	if sign < 0 && num.Sign() != 0 {
		num.Neg(num)
	}

	den := pow10(decimals)

	if exp >= 0 {
		num.Mul(num, pow10(int(exp)))
	} else if exp < 0 {
		den.Mul(den, pow10(int(-exp)))
	}

	r := new(big.Rat).SetFrac(num, den) // reduced, denom > 0
	return r, true
}

func pow10(n int) *big.Int {
	if n <= 0 {
		return big.NewInt(1)
	}
	ten := big.NewInt(10)
	out := new(big.Int).SetInt64(1)
	for i := 0; i < n; i++ {
		out.Mul(out, ten)
	}
	return out
}

// GetTrie returns the in-memory trie instance
func (d *Database) GetTrie() *mpf.Trie {
	trieMtx.RLock()
	defer trieMtx.RUnlock()
	return memoryTrie
}
