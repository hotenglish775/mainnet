package state

import (
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"

	"github.com/hotenglish775/mainnet/chain"
	"github.com/hotenglish775/mainnet/parallel"
	"github.com/hotenglish775/mainnet/runtime"
	"github.com/hotenglish775/mainnet/runtime/evm"
	"github.com/hotenglish775/mainnet/runtime/precompiled"
)

// GetHashByNumber is a function type to obtain a block hash by its number.
type GetHashByNumber = func(i uint64) types.Hash

// GetHashByNumberHelper returns a function of GetHashByNumber given a header.
type GetHashByNumberHelper = func(*types.Header) GetHashByNumber

// Executor is responsible for processing blocks and executing transactions.
type Executor struct {
	logger       hclog.Logger
	config       *chain.Params
	state        State
	GetHash      GetHashByNumberHelper
	PostHook     func(txn *Transition)
	parallelExec *parallel.ParallelExecutor
	lock         sync.Mutex
}

// NewExecutor creates a new Executor instance.
func NewExecutor(config *chain.Params, s State, logger hclog.Logger) *Executor {
	return &Executor{
		logger:       logger,
		config:       config,
		state:        s,
		parallelExec: parallel.NewParallelExecutor(),
	}
}

// ProcessBlock processes the block transactions by grouping them and executing in parallel.
func (e *Executor) ProcessBlock(
	parentRoot types.Hash,
	block *types.Block,
	blockCreator types.Address,
) (*Transition, error) {
	// Begin a new state transaction.
	txn, err := e.BeginTxn(parentRoot, block.Header, blockCreator)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Process each transaction in the block
	start := time.Now()
	
	// Wrap each block transaction into a ParallelTx.
	var ptxs []*parallel.ParallelTx
	for _, t := range block.Transactions {
		ptx := &parallel.ParallelTx{
			Tx:       t,
			ReadSet:  extractReadSet(t),
			WriteSet: extractWriteSet(t),
		}
		ptxs = append(ptxs, ptx)
	}

	// Execute transactions in parallel.
	results, err := e.parallelExec.ExecuteTransactions(ptxs)
	if err != nil {
		return nil, fmt.Errorf("parallel execution error: %w", err)
	}

	// Process the results.
	for _, res := range results {
		if res.Err != nil {
			if perr := txn.WriteFailedReceipt(res.Tx.Tx); perr != nil {
				return nil, fmt.Errorf("failed to write failed receipt: %w", perr)
			}
			// Log error details
			e.logger.Error("Transaction failed", "tx", res.Tx.Tx.Hash().Hex(), "error", res.Err)
		} else {
			if err := txn.Write(res.Tx.Tx); err != nil {
				return nil, fmt.Errorf("failed to write transaction: %w", err)
			}
		}
	}

	// Log processing time for debugging/profiling
	e.logger.Debug("block processed", "number", block.Header.Number, "txs", len(block.Transactions), "time", time.Since(start))

	return txn, nil
}

// extractReadSet extracts the ReadSet for a transaction based on ACC-20 standard.
// This returns the storage keys or account identifiers the transaction reads.
func extractReadSet(t *types.Transaction) []string {
	if t == nil {
		return []string{}
	}

	readSet := []string{}
	
	// Extract sender address
	from, err := t.GetFrom()
	if err == nil {
		readSet = append(readSet, from.String())
	}
	
	// Extract recipient address for ACC-20 token transfers
	if t.To != nil {
		readSet = append(readSet, t.To.String())
	}
	
	// Add ACC-20 specific storage keys
	if t.IsContractCreation() {
		// No specific contract reads
	} else if t.To != nil {
		// Common ACC-20 methods like balanceOf, allowance, etc.
		method := getACC20Method(t.Input)
		if method == "balanceOf" || method == "allowance" || method == "totalSupply" || method == "name" || method == "symbol" || method == "decimals" {
			contractAddr := t.To.String()
			readSet = append(readSet, fmt.Sprintf("%s:metadata", contractAddr))
			
			// For balanceOf and allowance, we're reading specific mappings
			if len(t.Input) >= 36 && (method == "balanceOf" || method == "allowance") {
				// Extract account address from input
				accountAddrSlice := t.Input[4:36]
				accountAddr := types.BytesToAddress(accountAddrSlice).String()
				readSet = append(readSet, fmt.Sprintf("%s:balances:%s", contractAddr, accountAddr))
				
				// For allowance, add second address (spender)
				if method == "allowance" && len(t.Input) >= 68 {
					spenderAddrSlice := t.Input[36:68]
					spenderAddr := types.BytesToAddress(spenderAddrSlice).String()
					readSet = append(readSet, fmt.Sprintf("%s:allowances:%s:%s", contractAddr, accountAddr, spenderAddr))
				}
			}
		}
	}
	
	return readSet
}

// extractWriteSet extracts the WriteSet for a transaction based on ACC-20 standard.
// This returns the storage keys or account identifiers the transaction modifies.
func extractWriteSet(t *types.Transaction) []string {
	if t == nil {
		return []string{}
	}

	writeSet := []string{}
	
	// Extract sender address
	from, err := t.GetFrom()
	if err == nil {
		writeSet = append(writeSet, from.String())
	}
	
	// Extract recipient address for ACC-20 token transfers
	if t.To != nil {
		writeSet = append(writeSet, t.To.String())
	}
	
	// Add ACC-20 specific storage keys
	if t.IsContractCreation() {
		// Contract creation modifies the contract's storage
		// Since we don't know the contract address yet, we'll use a placeholder
		writeSet = append(writeSet, "new_contract")
	} else if t.To != nil {
		// Common ACC-20 methods that modify state
		method := getACC20Method(t.Input)
		if method == "transfer" || method == "transferFrom" || method == "approve" || method == "mint" || method == "burn" {
			contractAddr := t.To.String()
			
			// For transfer and transferFrom, we're modifying balances
			if len(t.Input) >= 36 && (method == "transfer" || method == "transferFrom" || method == "mint" || method == "burn") {
				// Extract recipient address from input
				recipientAddrSlice := t.Input[4:36]
				recipientAddr := types.BytesToAddress(recipientAddrSlice).String()
				writeSet = append(writeSet, fmt.Sprintf("%s:balances:%s", contractAddr, recipientAddr))
				
				// For transferFrom and approve, add sender's allowance
				if method == "transferFrom" && len(t.Input) >= 68 {
					senderAddrSlice := t.Input[36:68]
					senderAddr := types.BytesToAddress(senderAddrSlice).String()
					writeSet = append(writeSet, fmt.Sprintf("%s:balances:%s", contractAddr, senderAddr))
					writeSet = append(writeSet, fmt.Sprintf("%s:allowances:%s:%s", contractAddr, senderAddr, from.String()))
				}
			}
			
			// For approve, we're modifying allowances
			if method == "approve" && len(t.Input) >= 36 {
				// Extract spender address from input
				spenderAddrSlice := t.Input[4:36]
				spenderAddr := types.BytesToAddress(spenderAddrSlice).String()
				writeSet = append(writeSet, fmt.Sprintf("%s:allowances:%s:%s", contractAddr, from.String(), spenderAddr))
			}
		}
	}
	
	return writeSet
}

// getACC20Method identifies the ACC-20 method being called based on transaction input data
func getACC20Method(input []byte) string {
	if len(input) < 4 {
		return ""
	}
	
	// Method ID is first 4 bytes of the hash of the method signature
	methodID := input[:4]
	
	// Common ACC-20 method IDs
	switch string(methodID) {
	case "\xa9\x05\x9c\xbb": // transfer(address,uint256)
		return "transfer"
	case "\x23\xb8\x72\xdd": // transferFrom(address,address,uint256)
		return "transferFrom"
	case "\x09\x5e\xa7\xb3": // approve(address,uint256)
		return "approve"
	case "\x70\xa0\x82\x31": // balanceOf(address)
		return "balanceOf"
	case "\xdd\x62\xed\x3e": // allowance(address,address)
		return "allowance"
	case "\x18\x16\x0d\xdd": // totalSupply()
		return "totalSupply"
	case "\x40\xc1\x0f\x19": // mint(address,uint256)
		return "mint"
	case "\x42\x96\x6c\x68": // burn(uint256)
		return "burn"
	case "\x06\xfd\xde\x03": // name()
		return "name"
	case "\x95\xd8\x9b\x41": // symbol()
		return "symbol"
	case "\x31\x3c\xe5\x67": // decimals()
		return "decimals"
	default:
		return "unknown"
	}
}

// BeginTxn creates a new state transition (transaction) using a snapshot of the current state.
func (e *Executor) BeginTxn(parentRoot types.Hash, header *types.Header, coinbaseReceiver types.Address) (*Transition, error) {
	forkConfig := e.config.Forks.At(header.Number)

	auxSnap, err := e.state.NewSnapshotAt(parentRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create new snapshot: %w", err)
	}

	newTxn := NewTxn(auxSnap)

	txCtx := runtime.TxContext{
		Coinbase:   coinbaseReceiver,
		Timestamp:  int64(header.Timestamp),
		Number:     int64(header.Number),
		Difficulty: types.BytesToHash(new(big.Int).SetUint64(header.Difficulty).Bytes()),
		GasLimit:   int64(header.GasLimit),
		ChainID:    int64(e.config.ChainID),
	}

	txn := &Transition{
		logger:      e.logger,
		ctx:         txCtx,
		state:       newTxn,
		snap:        auxSnap,
		getHash:     e.GetHash(header),
		auxState:    e.state,
		config:      forkConfig,
		gasPool:     uint64(txCtx.GasLimit),
		receipts:    []*types.Receipt{},
		totalGas:    0,
		evm:         evm.NewEVM(),
		precompiles: precompiled.NewPrecompiled(),
		PostHook:    e.PostHook,
	}

	return txn, nil
}

// State represents the blockchain state
type State interface {
	NewSnapshotAt(root types.Hash) (Snapshot, error)
	// Other methods as needed
}

// Snapshot represents a point-in-time snapshot of the blockchain state
type Snapshot interface {
	// Methods needed for state snapshots
}

// NewTxn creates a new transaction object from a snapshot
func NewTxn(snap Snapshot) *Txn {
	return &Txn{
		snap: snap,
	}
}

// Txn represents a transaction object
type Txn struct {
	snap Snapshot
	// Other fields as needed
}

// Transition represents a state transition process
type Transition struct {
	logger      hclog.Logger
	ctx         runtime.TxContext
	state       *Txn
	snap        Snapshot
	getHash     GetHashByNumber
	auxState    State
	config      chain.ForksInTime
	gasPool     uint64
	receipts    []*types.Receipt
	totalGas    uint64
	evm         *evm.EVM
	precompiles *precompiled.Precompiled
	PostHook    func(txn *Transition)
}

// Write processes a transaction and writes it to the chain
func (t *Transition) Write(tx *types.Transaction) error {
	// Implementation would include:
	// 1. Validate transaction
	// 2. Apply transaction changes to state
	// 3. Create receipt
	// 4. Update gas used
	
	// For brevity, simplified implementation:
	receipt := &types.Receipt{
		TxHash:            tx.Hash(),
		GasUsed:           tx.Gas,
		TransactionType:   tx.Type,
		ContractAddress:   nil, // Set if contract creation
		Status:            1,   // 1 for success
	}
	
	// Add receipt to list
	t.receipts = append(t.receipts, receipt)
	t.totalGas += tx.Gas
	
	// Call post hook if defined
	if t.PostHook != nil {
		t.PostHook(t)
	}
	
	return nil
}

// WriteFailedReceipt creates a receipt for a failed transaction
func (t *Transition) WriteFailedReceipt(tx *types.Transaction) error {
	// Create receipt for failed transaction
	receipt := &types.Receipt{
		TxHash:            tx.Hash(),
		GasUsed:           tx.Gas,
		TransactionType:   tx.Type,
		ContractAddress:   nil,
		Status:            0, // 0 for failure
	}
	
	// Add receipt to list
	t.receipts = append(t.receipts, receipt)
	t.totalGas += tx.Gas
	
	return nil
}
