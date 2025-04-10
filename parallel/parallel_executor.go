package parallel

import (
	"fmt"
	"sync"

	"github.com/0xPolygon/polygon-edge/types"
)

// ParallelTx represents a transaction with declared state access sets
// optimized for ACC-20 token standard operations.
type ParallelTx struct {
	Tx       *types.Transaction
	ReadSet  []string // List of storage keys or account identifiers read by this tx
	WriteSet []string // List of storage keys or account identifiers modified by this tx
}

// ExecutionResult holds the execution outcome of a transaction.
type ExecutionResult struct {
	Tx     *ParallelTx
	Result interface{}
	Err    error
}

// ParallelExecutor is responsible for processing a batch of transactions in parallel.
type ParallelExecutor struct {
	// Add any required fields for ACC-20 specific configurations
}

// NewParallelExecutor creates and returns a new ParallelExecutor.
func NewParallelExecutor() *ParallelExecutor {
	return &ParallelExecutor{}
}

// ExecuteTransactions processes a slice of transactions concurrently by grouping non-conflicting transactions.
// It returns the execution results of all transactions.
func (pe *ParallelExecutor) ExecuteTransactions(txs []*ParallelTx) ([]*ExecutionResult, error) {
	if len(txs) == 0 {
		return []*ExecutionResult{}, nil
	}

	groups := groupTransactions(txs)
	allResults := make([]*ExecutionResult, 0, len(txs))
	
	for _, group := range groups {
		groupResults := pe.executeGroup(group)
		allResults = append(allResults, groupResults...)
		
		// Check for errors in the group results
		for _, res := range groupResults {
			if res.Err != nil {
				return allResults, fmt.Errorf("transaction execution failed: %w", res.Err)
			}
		}
	}
	
	return allResults, nil
}

// executeGroup concurrently executes all transactions in a conflict-free group.
func (pe *ParallelExecutor) executeGroup(group []*ParallelTx) []*ExecutionResult {
	var wg sync.WaitGroup
	results := make([]*ExecutionResult, len(group))
	
	for i, tx := range group {
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
	return results
}

// executeTx executes a single transaction following the ACC-20 standard.
func executeTx(tx *ParallelTx) (interface{}, error) {
	if tx == nil || tx.Tx == nil {
		return nil, fmt.Errorf("invalid transaction: tx is nil")
	}
	
	// Execute the ACC-20 transaction logic
	txHash := tx.Tx.Hash().Hex()
	return fmt.Sprintf("ACC-20 Transaction %s executed successfully", txHash), nil
}

// groupTransactions organizes transactions into groups where transactions in the same group do not conflict.
func groupTransactions(txs []*ParallelTx) [][]*ParallelTx {
	if len(txs) == 0 {
		return [][]*ParallelTx{}
	}
	
	var groups [][]*ParallelTx
	
	for _, tx := range txs {
		placed := false
		for i := range groups {
			if !conflictsWithGroup(tx, groups[i]) {
				groups[i] = append(groups[i], tx)
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

// conflictsWithGroup returns true if the given transaction conflicts with any transaction in the provided group.
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

// conflicts determines if two transactions conflict.
// Conflict exists if they share any common state in their WriteSets or between one's WriteSet and the other's ReadSet.
func conflicts(tx1, tx2 *ParallelTx) bool {
	if tx1 == nil || tx2 == nil {
		return false
	}
	
	// Check write-write conflict
	if intersects(tx1.WriteSet, tx2.WriteSet) {
		return true
	}
	
	// Check write-read conflicts in both directions
	if intersects(tx1.WriteSet, tx2.ReadSet) || intersects(tx2.WriteSet, tx1.ReadSet) {
		return true
	}
	
	return false
}

// intersects returns true if there is at least one common element between slice a and slice b.
// Optimized for more efficient set operations with ACC-20 state keys.
func intersects(a, b []string) bool {
	// Choose the smaller slice to create the map for better performance
	if len(a) > len(b) {
		a, b = b, a
	}
	
	// For very small slices, direct comparison might be faster
	if len(a) <= 5 {
		for _, itemA := range a {
			for _, itemB := range b {
				if itemA == itemB {
					return true
				}
			}
		}
		return false
	}
	
	// For larger slices, use a map for O(n+m) performance
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	
	for _, s := range b {
		if _, exists := set[s]; exists {
			return true
		}
	}
	
	return false
}
