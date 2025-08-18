package strategy

import (
	"sync"
	"zenGate-Global/merkle-oracle-node/internal/database"
	"zenGate-Global/merkle-oracle-node/internal/tx"

	apolloUTxO "github.com/Salvionied/apollo/serialization/UTxO"
)

type PendingTxInfo struct {
	inputs                map[tx.InputKey]struct{}
	submissionBlockHeight uint64
}

type SpentInputInfo struct {
	//nolint:unused
	confirmedAtBlock uint64
	//nolint:unused
	inputs map[tx.InputKey]struct{}
}

// Global pending transactions tracking
var (
	pendingTxInputs = make(map[string]PendingTxInfo)
	pendingTxMutex  sync.RWMutex

	//nolint:unused
	confirmedSpentInputs map[string]SpentInputInfo
	//nolint:unused
	confirmedSpentMutex sync.RWMutex

	globalValidatorUtxo *apolloUTxO.UTxO
	validatorUtxoMutex  sync.RWMutex

	latestTrie      *database.MemoryTrie
	latestTrieMutex sync.RWMutex

	//nolint:unused
	txBuildMutex sync.Mutex
)

// CheckInputConflicts checks if any of the provided inputs conflict with pending transactions
func CheckInputConflicts(inputs ...tx.InputKey) (bool, string) {
	pendingTxMutex.RLock()
	defer pendingTxMutex.RUnlock()

	for _, input := range inputs {
		for txHash, pendingTx := range pendingTxInputs {
			if _, exists := pendingTx.inputs[input]; exists {
				return true, txHash
			}
		}
	}
	return false, ""
}

// AddPendingTransaction adds a transaction to the pending list
func AddPendingTransaction(
	txHash string,
	inputs map[tx.InputKey]struct{},
	blockHeight uint64,
) {
	pendingTxMutex.Lock()
	defer pendingTxMutex.Unlock()

	pendingTxInputs[txHash] = PendingTxInfo{
		inputs:                inputs,
		submissionBlockHeight: blockHeight,
	}
}

// RemovePendingTransaction removes a confirmed transaction from the pending list
func RemovePendingTransaction(txHash string) {
	pendingTxMutex.Lock()
	defer pendingTxMutex.Unlock()

	delete(pendingTxInputs, txHash)
}

// CleanupOldPendingTransactions removes transactions that are older than maxBlocks
func CleanupOldPendingTransactions(
	currentBlockHeight uint64,
	maxBlocks uint64,
) []string {
	pendingTxMutex.Lock()
	defer pendingTxMutex.Unlock()

	var removedTxs []string
	for txHash, pendingTx := range pendingTxInputs {
		if currentBlockHeight > pendingTx.submissionBlockHeight &&
			currentBlockHeight-pendingTx.submissionBlockHeight > maxBlocks {
			delete(pendingTxInputs, txHash)
			removedTxs = append(removedTxs, txHash)
		}
	}
	return removedTxs
}

// GetPendingTransactionCount returns the number of pending transactions
func GetPendingTransactionCount() int {
	pendingTxMutex.RLock()
	defer pendingTxMutex.RUnlock()
	return len(pendingTxInputs)
}

func GetPendingTransaction(txHash string) PendingTxInfo {
	pendingTxMutex.RLock()
	defer pendingTxMutex.RUnlock()
	return pendingTxInputs[txHash]
}

func IsPendingTransaction(txHash string) bool {
	pendingTxMutex.RLock()
	defer pendingTxMutex.RUnlock()
	_, exists := pendingTxInputs[txHash]
	return exists
}

// SetGlobalValidatorUtxo sets the global validator UTXO cache
func SetGlobalValidatorUtxo(utxo *apolloUTxO.UTxO) {
	validatorUtxoMutex.Lock()
	defer validatorUtxoMutex.Unlock()
	globalValidatorUtxo = utxo
}

// GetGlobalValidatorUtxo gets the global validator UTXO cache
func GetGlobalValidatorUtxo() *apolloUTxO.UTxO {
	validatorUtxoMutex.RLock()
	defer validatorUtxoMutex.RUnlock()
	return globalValidatorUtxo
}

// ClearGlobalValidatorUtxo clears the global validator UTXO cache
func ClearGlobalValidatorUtxo() {
	validatorUtxoMutex.Lock()
	defer validatorUtxoMutex.Unlock()
	globalValidatorUtxo = nil
}

// SetLatestTrie sets the latest trie cache
func SetLatestTrie(trie *database.MemoryTrie) {
	latestTrieMutex.Lock()
	defer latestTrieMutex.Unlock()
	latestTrie = trie
}

// GetLatestTrie gets the latest trie cache
func GetLatestTrie() *database.MemoryTrie {
	latestTrieMutex.RLock()
	defer latestTrieMutex.RUnlock()
	return latestTrie
}

// ClearLatestTrie clears the latest trie cache
func ClearLatestTrie() {
	latestTrieMutex.Lock()
	defer latestTrieMutex.Unlock()
	latestTrie = nil
}
