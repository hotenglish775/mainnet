package txpool

import (
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
)

// TxPool holds all pending and queued transactions
type TxPool struct {
	mu           sync.RWMutex
	pending      map[types.Address][]*types.Transaction
	allTxs       map[types.Hash]*types.Transaction
	rejectedTxs  map[types.Hash]struct{}
	blockGasLimit uint64
}

// NewTxPool creates a new TxPool
func NewTxPool(blockGasLimit uint64) *TxPool {
	return &TxPool{
		pending:       make(map[types.Address][]*types.Transaction),
		allTxs:        make(map[types.Hash]*types.Transaction),
		rejectedTxs:   make(map[types.Hash]struct{}),
		blockGasLimit: blockGasLimit,
	}
}

// AddTransaction adds a new transaction to the pool if valid
func (tp *TxPool) AddTransaction(tx *types.Transaction) error {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	if _, exists := tp.allTxs[tx.Hash()]; exists {
		return nil // Already exists
	}

	// Validate transaction nonce, gas, signature
	if err := validateTransaction(tx); err != nil {
		tp.rejectedTxs[tx.Hash()] = struct{}{}
		return err
	}

	from, err := tx.Sender()
	if err != nil {
		tp.rejectedTxs[tx.Hash()] = struct{}{}
		return err
	}

	tp.pending[from] = append(tp.pending[from], tx)
	tp.allTxs[tx.Hash()] = tx
	return nil
}

// GetPending returns all valid pending transactions
func (tp *TxPool) GetPending() map[types.Address][]*types.Transaction {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	cpy := make(map[types.Address][]*types.Transaction)
	for addr, txs := range tp.pending {
		cpy[addr] = append([]*types.Transaction{}, txs...)
	}
	return cpy
}

// RemoveStale removes transactions that are expired
func (tp *TxPool) RemoveStale() {
	tp.mu.Lock()
	defer tp.mu.Unlock()

	now := time.Now()
	for addr, txs := range tp.pending {
		filtered := txs[:0]
		for _, tx := range txs {
			if now.Sub(time.Unix(int64(tx.Timestamp), 0)) < TxExpiration {
				filtered = append(filtered, tx)
			} else {
				delete(tp.allTxs, tx.Hash())
			}
		}
		tp.pending[addr] = filtered
	}
}

// EnableParallelExecution returns true if tx count passes threshold
func (tp *TxPool) EnableParallelExecution() bool {
	tp.mu.RLock()
	defer tp.mu.RUnlock()
	return len(tp.allTxs) >= ParallelExecutionThreshold
}

// validateTransaction ensures transaction integrity
func validateTransaction(tx *types.Transaction) error {
	if tx.Gas == 0 || tx.GasPrice == nil || tx.To == nil {
		return types.ErrInvalidTx
	}
	if _, err := tx.Sender(); err != nil {
		return err
	}
	return nil
}
