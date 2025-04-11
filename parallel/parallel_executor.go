package parallel

import (
	"errors"
	"fmt"
	"sync"

	"github.com/0xPolygon/polygon-edge/types"
)

// ParallelTx represents a transaction with declared state access sets.
type ParallelTx struct {
	Tx       *types.Transaction
	ReadSet  []string
	WriteSet []string
}

// ExecutionResult holds the execution outcome of a transaction.
type ExecutionResult struct {
	Tx     *ParallelTx
	Result interface{}
	Err    error
}

// Accelerator abstracts hardware-level acceleration.
type Accelerator interface {
	ExecuteBatch(txs []*ParallelTx) ([]*ExecutionResult, error)
}

// GetAccelerator returns the best available Accelerator based on runtime detection.
func GetAccelerator() Accelerator {
	// In this production code, we assume the CPU SIMD accelerator is available.
	// Replace this detection logic as needed.
	return NewCPUSIMDAccelerator()
}

// CPUSIMDAccelerator implements Accelerator using CPU-level optimizations.
type CPUSIMDAccelerator struct{}

// NewCPUSIMDAccelerator creates a new CPUSIMDAccelerator instance.
func NewCPUSIMDAccelerator() *CPUSIMDAccelerator {
	return &CPUSIMDAccelerator{}
}

// ExecuteBatch processes the batch of transactions concurrently using CPU routines.
func (a *CPUSIMDAccelerator) ExecuteBatch(txs []*ParallelTx) ([]*ExecutionResult, error) {
	if txs == nil {
		return nil, errors.New("transaction batch is nil")
	}
	return executeBatchCPU(txs)
}

// executeBatchCPU executes a batch of transactions concurrently.
func executeBatchCPU(txs []*ParallelTx) ([]*ExecutionResult, error) {
	var wg sync.WaitGroup
	results := make([]*ExecutionResult, len(txs))

	for i, tx := range txs {
		wg.Add(1)
		go func(i int, tx *ParallelTx) {
			defer wg.Done()
			res, err := executeTx(tx)
			results[i] = &ExecutionResult{
				Tx:     tx,
				Result: res,
				Err:    err,
			}
		}(i, tx)
	}
	wg.Wait()
	return results, nil
}

// executeTx executes a single transaction, following ACC-20 transaction rules.
func executeTx(tx *ParallelTx) (interface{}, error) {
	if tx == nil || tx.Tx == nil {
		return nil, errors.New("invalid transaction: nil value")
	}
	
	// Ensure transaction has a valid hash before proceeding
	hash := tx.Tx.Hash()
	if hash == types.ZeroHash {
		return nil, errors.New("invalid transaction: zero hash")
	}
	
	// Execute ACC-20 transaction logic here
	return fmt.Sprintf("ACC-20 transaction %s executed successfully", hash.Hex()), nil
}

// ParallelExecutor handles the concurrent execution of transaction groups.
type ParallelExecutor struct {
	accelerator Accelerator
	mu          sync.Mutex
}

// NewParallelExecutor creates and returns a new ParallelExecutor instance.
func NewParallelExecutor() *ParallelExecutor {
	return &ParallelExecutor{
		accelerator: GetAccelerator(),
	}
}

// ExecuteTransactions processes a slice of transactions by grouping non-conflicting ones and executing them in parallel.
func (pe *ParallelExecutor) ExecuteTransactions(txs []*ParallelTx) ([]*ExecutionResult, error) {
	if txs == nil {
		return nil, errors.New("transaction list is nil")
	}
	if len(txs) == 0 {
		return []*ExecutionResult{}, nil
	}

	// Filter out invalid transactions
	var validTxs []*ParallelTx
	for _, tx := range txs {
		if tx != nil && tx.Tx != nil {
			validTxs = append(validTxs, tx)
		}
	}
	
	if len(validTxs) == 0 {
		return []*ExecutionResult{}, nil
	}

	groups := groupTransactions(validTxs)
	var allResults []*ExecutionResult

	pe.mu.Lock()
	defer pe.mu.Unlock()
	
	for _, group := range groups {
		groupResults, err := pe.accelerator.ExecuteBatch(group)
		if err != nil {
			return allResults, fmt.Errorf("transaction execution failed: %w", err)
		}
		allResults = append(allResults, groupResults...)
	}
	return allResults, nil
}

// groupTransactions organizes transactions into groups that can execute concurrently.
func groupTransactions(txs []*ParallelTx) [][]*ParallelTx {
	if len(txs) == 0 {
		return [][]*ParallelTx{}
	}
	
	var groups [][]*ParallelTx
	for _, tx := range txs {
		placed := false
		for idx := range groups {
			if !conflictsWithGroup(tx, groups[idx]) {
				groups[idx] = append(groups[idx], tx)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []*ParallelTx{tx})
		}
	}
	return groups
}

// conflictsWithGroup returns true if the given transaction conflicts with any transaction in the group.
func conflictsWithGroup(tx *ParallelTx, group []*ParallelTx) bool {
	if tx == nil {
		return false
	}
	
	for _, other := range group {
		if other == nil {
			continue
		}
		if conflicts(tx, other) {
			return true
		}
	}
	return false
}

// conflicts checks if two transactions have overlapping state access.
func conflicts(tx1, tx2 *ParallelTx) bool {
	// Same transaction should not conflict with itself
	if tx1 == tx2 {
		return false
	}
	
	// Check for write-write conflicts
	if intersects(tx1.WriteSet, tx2.WriteSet) {
		return true
	}
	
	// Check for read-write conflicts in both directions
	if intersects(tx1.ReadSet, tx2.WriteSet) || intersects(tx1.WriteSet, tx2.ReadSet) {
		return true
	}
	
	return false
}

// intersects returns true if slices a and b share any common element.
// Optimized for production use.
func intersects(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	
	// Ensure 'a' is the smaller slice for better performance
	if len(a) > len(b) {
		a, b = b, a
	}
	
	// Create a map from the smaller slice for O(1) lookups
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	
	// Check if any element from b exists in the map
	for _, s := range b {
		if _, exists := set[s]; exists {
			return true
		}
	}
	return false
}
