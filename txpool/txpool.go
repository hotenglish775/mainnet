package txpool

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/0xPolygon/polygon-edge/types"
)

const (
	// MaxTxPoolSize defines the max number of transactions the pool can hold
	MaxTxPoolSize = 100000
	// TxExpiration is the time a transaction remains in the pool before it is dropped
	TxExpiration = 30 * time.Minute
	// ParallelExecutionThreshold defines when dynamic parallel processing kicks in
	ParallelExecutionThreshold = 5000
	// MinGasPrice is the minimum gas price required for a transaction
	MinGasPrice = 1
	// MaxRejectedTxsSize limits the size of rejected transactions cache
	MaxRejectedTxsSize = 10000
	// MaxFutureTimeDrift is the maximum time drift allowed for transaction timestamp
	MaxFutureTimeDrift = 15 * time.Minute
)

// TxPool holds all pending and queued transactions
type TxPool struct {
	mu            sync.RWMutex
	pending       map[types.Address][]*types.Transaction
	allTxs        map[types.Hash]*types.Transaction
	rejectedTxs   map[types.Hash]struct{}
	blockGasLimit uint64
	nonceCache    map[types.Address]uint64
	lastCleanup   time.Time
}

// NewTxPool creates a new TxPool
func NewTxPool(blockGasLimit uint64) *TxPool {
	return &TxPool{
		pending:       make(map[types.Address][]*types.Transaction),
		allTxs:        make(map[types.Hash]*types.Transaction),
		rejectedTxs:   make(map[types.Hash]struct{}),
		nonceCache:    make(map[types.Address]uint64),
		blockGasLimit: blockGasLimit,
		lastCleanup:   time.Now(),
	}
}

// AddTransaction adds a new transaction to the pool if valid
func (tp *TxPool) AddTransaction(tx *types.Transaction) error {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	// Check if transaction already exists
	if _, exists := tp.allTxs[tx.Hash()]; exists {
		return nil // Already exists
	}

	// Check if it was previously rejected
	if _, rejected := tp.rejectedTxs[tx.Hash()]; rejected {
		return errors.New("transaction was previously rejected")
	}

	// Validate transaction nonce, gas, signature
	if err := validateTransaction(tx, tp.blockGasLimit); err != nil {
		// Add to rejected transactions with limited size
		if len(tp.rejectedTxs) >= MaxRejectedTxsSize {
			tp.pruneRejectedTxs()
		}
		tp.rejectedTxs[tx.Hash()] = struct{}{}
		return err
	}

	// Extract sender
	from, err := tx.Sender()
	if err != nil {
		tp.rejectedTxs[tx.Hash()] = struct{}{}
		return err
	}

	// Check if the pool size exceeds the limit
	if len(tp.allTxs) >= MaxTxPoolSize {
		return types.ErrTxPoolOverflow
	}

	// Validate timestamp is not too far in the future
	txTime := time.Unix(int64(tx.Timestamp), 0)
	if txTime.After(time.Now().Add(MaxFutureTimeDrift)) {
		tp.rejectedTxs[tx.Hash()] = struct{}{}
		return errors.New("transaction timestamp too far in the future")
	}

	// Check for transaction replacement (same nonce, higher gas price)
	existingTxs := tp.pending[from]
	for i, existingTx := range existingTxs {
		if existingTx.Nonce == tx.Nonce {
			// Only replace if new gas price is higher
			if tx.GasPrice.Cmp(existingTx.GasPrice) > 0 {
				// Remove the existing transaction
				delete(tp.allTxs, existingTx.Hash())
				// Replace in the pending list
				existingTxs[i] = tx
				tp.pending[from] = existingTxs
				tp.allTxs[tx.Hash()] = tx
				return nil
			}
			return errors.New("replacement transaction underpriced")
		}
	}

	// Add new transaction
	tp.pending[from] = append(tp.pending[from], tx)
	tp.allTxs[tx.Hash()] = tx

	// Sort transactions by nonce
	tp.sortAccountTransactions(from)

	return nil
}

// GetPending returns all valid pending transactions
func (tp *TxPool) GetPending() map[types.Address][]*types.Transaction {
	tp.mu.RLock()
	defer tp.mu.RUnlock()

	cpy := make(map[types.Address][]*types.Transaction)
	for addr, txs := range tp.pending {
		if len(txs) > 0 {
			cpy[addr] = append([]*types.Transaction{}, txs...)
		}
	}
	return cpy
}

// RemoveStale removes transactions that are expired
func (tp *TxPool) RemoveStale() {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	now := time.Now()
	for addr, txs := range tp.pending {
		var filtered []*types.Transaction
		for _, tx := range txs {
			if now.Sub(time.Unix(int64(tx.Timestamp), 0)) < TxExpiration {
				filtered = append(filtered, tx)
			} else {
				delete(tp.allTxs, tx.Hash())
			}
		}
		
		if len(filtered) > 0 {
			tp.pending[addr] = filtered
		} else {
			delete(tp.pending, addr) // Remove empty entries
		}
	}

	// Also clean up rejected txs periodically
	if now.Sub(tp.lastCleanup) > TxExpiration {
		tp.pruneRejectedTxs()
		tp.lastCleanup = now
	}
}

// EnableParallelExecution returns true if tx count passes threshold
func (tp *TxPool) EnableParallelExecution() bool {
	tp.mu.RLock()
	txCount := len(tp.allTxs)
	tp.mu.RUnlock()
	return txCount >= ParallelExecutionThreshold
}

// validateTransaction ensures transaction integrity
func validateTransaction(tx *types.Transaction, blockGasLimit uint64) error {
	// Basic validity checks
	if tx.Gas == 0 || tx.GasPrice == nil || tx.To == nil {
		return types.ErrInvalidTx
	}

	// Check gas limits
	if tx.Gas > blockGasLimit {
		return types.ErrGasLimitExceeded
	}

	// Check minimum gas price
	if tx.GasPrice.Uint64() < MinGasPrice {
		return errors.New("gas price too low")
	}

	// Verify signature
	if _, err := tx.Sender(); err != nil {
		return err
	}

	return nil
}

// RemoveTransactions removes a list of transactions from the pool
func (tp *TxPool) RemoveTransactions(txs []*types.Transaction) {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	for _, tx := range txs {
		hash := tx.Hash()
		if _, exists := tp.allTxs[hash]; exists {
			from, err := tx.Sender()
			if err == nil {
				// Remove from pending
				pendingList := tp.pending[from]
				for i, pendingTx := range pendingList {
					if pendingTx.Hash() == hash {
						tp.pending[from] = append(pendingList[:i], pendingList[i+1:]...)
						break
					}
				}
				
				// If no more transactions for this address, clean up
				if len(tp.pending[from]) == 0 {
					delete(tp.pending, from)
				}
			}
			// Remove from allTxs
			delete(tp.allTxs, hash)
		}
	}
}

// UpdateNonce updates the cached nonce for an account
func (tp *TxPool) UpdateNonce(addr types.Address, nonce uint64) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	
	tp.nonceCache[addr] = nonce
	
	// Clean up transactions with lower nonces
	txs := tp.pending[addr]
	var validTxs []*types.Transaction
	
	for _, tx := range txs {
		if tx.Nonce >= nonce {
			validTxs = append(validTxs, tx)
		} else {
			delete(tp.allTxs, tx.Hash())
		}
	}
	
	if len(validTxs) > 0 {
		tp.pending[addr] = validTxs
	} else {
		delete(tp.pending, addr)
	}
}

// GetNonce returns the expected next nonce for an account
func (tp *TxPool) GetNonce(addr types.Address) uint64 {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	
	if nonce, exists := tp.nonceCache[addr]; exists {
		return nonce
	}
	
	return 0 // Default nonce if not found
}

// sortAccountTransactions sorts transactions for an account by nonce
func (tp *TxPool) sortAccountTransactions(addr types.Address) {
	txs := tp.pending[addr]
	if len(txs) <= 1 {
		return
	}
	
	sort.SliceStable(txs, func(i, j int) bool {
		return txs[i].Nonce < txs[j].Nonce
	})
	
	tp.pending[addr] = txs
}

// pruneRejectedTxs removes old entries from the rejected txs cache
func (tp *TxPool) pruneRejectedTxs() {
	// Simple strategy: just reset if too large
	if len(tp.rejectedTxs) >= MaxRejectedTxsSize {
		tp.rejectedTxs = make(map[types.Hash]struct{})
	}
}

// Size returns the number of transactions in the pool
func (tp *TxPool) Size() int {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return len(tp.allTxs)
}

// Clear empties all transactions from the pool
func (tp *TxPool) Clear() {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	
	tp.pending = make(map[types.Address][]*types.Transaction)
	tp.allTxs = make(map[types.Hash]*types.Transaction)
	// We keep the rejected txs to prevent immediate resubmission
}
