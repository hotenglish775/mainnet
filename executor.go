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
	"github.com/hotenglish775/mainnet/state/runtime"
	"github.com/hotenglish775/mainnet/state/runtime/evm"
	"github.com/hotenglish775/mainnet/state/runtime/precompiled"
)

// GetHashByNumber defines a function to get a block hash by its number.
type GetHashByNumber = func(i uint64) types.Hash

// GetHashByNumberHelper returns a GetHashByNumber function for a header.
type GetHashByNumberHelper = func(*types.Header) GetHashByNumber

// Executor processes blocks and applies transactions to state.
type Executor struct {
	logger       hclog.Logger
	config       *chain.Params
	state        State
	GetHash      GetHashByNumberHelper
	PostHook     func(txn *Transition)
	parallelExec *parallel.ParallelExecutor
}

// NewExecutor creates a new Executor instance.
func NewExecutor(config *chain.Params, s State, logger hclog.Logger) *Executor {
	if config == nil {
		panic("config cannot be nil")
	}
	if s == nil {
		panic("state cannot be nil")
	}
	
	return &Executor{
		logger:       logger.Named("executor"),
		config:       config,
		state:        s,
		parallelExec: parallel.NewParallelExecutor(),
	}
}

// ProcessBlock processes the transactions in a block by grouping them and executing in parallel.
func (e *Executor) ProcessBlock(
	parentRoot types.Hash,
	block *types.Block,
	blockCreator types.Address,
) (*Transition, error) {
	if block == nil {
		return nil, fmt.Errorf("block cannot be nil")
	}
	
	startTime := time.Now()
	defer func() {
		e.logger.Debug("block processing completed", 
			"number", block.Header.Number, 
			"hash", block.Header.Hash, 
			"txs", len(block.Transactions),
			"time", time.Since(startTime))
	}()
	
	txn, err := e.BeginTxn(parentRoot, block.Header, blockCreator)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}

	if len(block.Transactions) == 0 {
		return txn, nil
	}

	ptxs := make([]*parallel.ParallelTx, 0, len(block.Transactions))
	for _, t := range block.Transactions {
		if t == nil {
			continue
		}
		
		ptx := &parallel.ParallelTx{
			Tx:       t,
			ReadSet:  extractReadSet(t),
			WriteSet: extractWriteSet(t),
		}
		ptxs = append(ptxs, ptx)
	}

	results, err := e.parallelExec.ExecuteTransactions(ptxs)
	if err != nil {
		return nil, fmt.Errorf("parallel execution error: %w", err)
	}

	for _, res := range results {
		if res == nil || res.Tx == nil || res.Tx.Tx == nil {
			e.logger.Warn("skipping nil execution result")
			continue
		}
		
		if res.Err != nil {
			if perr := txn.WriteFailedReceipt(res.Tx.Tx); perr != nil {
				return nil, fmt.Errorf("failed to write failed receipt: %w", perr)
			}
			e.logger.Error("transaction failed", 
				"tx", res.Tx.Tx.Hash().Hex(), 
				"error", res.Err)
		} else {
			if err := txn.Write(res.Tx.Tx); err != nil {
				return nil, fmt.Errorf("failed to write transaction: %w", err)
			}
		}
	}

	return txn, nil
}

// extractReadSet extracts the state keys the transaction will read.
func extractReadSet(t *types.Transaction) []string {
	if t == nil {
		return []string{}
	}
	
	// Extract read set from transaction data
	// Parse ACC-20 method calls to determine which state keys will be read
	readSet := make([]string, 0)
	
	// Add sender address as it's always read for nonce and balance checks
	sender, _ := t.GetSender()
	readSet = append(readSet, sender.String())
	
	// Add recipient address if it's a value transfer or contract call
	if t.To != nil {
		readSet = append(readSet, t.To.String())
	}
	
	// Parse the transaction input for ACC-20 methods that read state
	if t.Input != nil && len(t.Input) >= 4 {
		methodID := t.Input[:4]
		
		// Common ACC-20 method IDs that read state:
		// balanceOf, allowance, totalSupply, etc.
		switch {
		case isBalanceOfMethod(methodID):
			if len(t.Input) >= 36 {
				account := extractAddressFromInput(t.Input, 4)
				readSet = append(readSet, fmt.Sprintf("acc20:balance:%s", account))
			}
		case isAllowanceMethod(methodID):
			if len(t.Input) >= 68 {
				owner := extractAddressFromInput(t.Input, 4)
				spender := extractAddressFromInput(t.Input, 36)
				readSet = append(readSet, fmt.Sprintf("acc20:allowance:%s:%s", owner, spender))
			}
		case isTotalSupplyMethod(methodID):
			if t.To != nil {
				readSet = append(readSet, fmt.Sprintf("acc20:supply:%s", t.To.String()))
			}
		}
	}
	
	return readSet
}

// extractWriteSet extracts the state keys the transaction will write.
func extractWriteSet(t *types.Transaction) []string {
	if t == nil {
		return []string{}
	}
	
	// Extract write set from transaction data
	// Parse ACC-20 method calls to determine which state keys will be written
	writeSet := make([]string, 0)
	
	// Add sender address as it's always written (nonce update)
	sender, _ := t.GetSender()
	writeSet = append(writeSet, sender.String())
	
	// Add recipient address if it's a value transfer
	if t.To != nil && t.Value.Sign() > 0 {
		writeSet = append(writeSet, t.To.String())
	}
	
	// Parse the transaction input for ACC-20 methods that write state
	if t.Input != nil && len(t.Input) >= 4 {
		methodID := t.Input[:4]
		
		// Common ACC-20 method IDs that write state:
		// transfer, approve, transferFrom, etc.
		switch {
		case isTransferMethod(methodID):
			if len(t.Input) >= 36 {
				to := extractAddressFromInput(t.Input, 4)
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", sender.String()))
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", to))
			}
		case isApproveMethod(methodID):
			if len(t.Input) >= 36 {
				spender := extractAddressFromInput(t.Input, 4)
				writeSet = append(writeSet, fmt.Sprintf("acc20:allowance:%s:%s", sender.String(), spender))
			}
		case isTransferFromMethod(methodID):
			if len(t.Input) >= 68 {
				from := extractAddressFromInput(t.Input, 4)
				to := extractAddressFromInput(t.Input, 36)
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", from))
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", to))
				writeSet = append(writeSet, fmt.Sprintf("acc20:allowance:%s:%s", from, sender.String()))
			}
		case isMintMethod(methodID):
			if t.To != nil && len(t.Input) >= 36 {
				to := extractAddressFromInput(t.Input, 4)
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", to))
				writeSet = append(writeSet, fmt.Sprintf("acc20:supply:%s", t.To.String()))
			}
		case isBurnMethod(methodID):
			if t.To != nil && len(t.Input) >= 36 {
				account := extractAddressFromInput(t.Input, 4)
				writeSet = append(writeSet, fmt.Sprintf("acc20:balance:%s", account))
				writeSet = append(writeSet, fmt.Sprintf("acc20:supply:%s", t.To.String()))
			}
		}
	}
	
	return writeSet
}

// Helper functions for extracting data from transaction input
func extractAddressFromInput(input []byte, offset int) string {
	if len(input) < offset+32 {
		return ""
	}
	// Extract 20-byte address from 32-byte parameter
	addrBytes := input[offset+12 : offset+32]
	return fmt.Sprintf("0x%x", addrBytes)
}

// Helper functions to identify ACC-20 method signatures
func isTransferMethod(methodID []byte) bool {
	// Transfer method signature: 0xa9059cbb
	return methodID[0] == 0xa9 && methodID[1] == 0x05 && methodID[2] == 0x9c && methodID[3] == 0xbb
}

func isApproveMethod(methodID []byte) bool {
	// Approve method signature: 0x095ea7b3
	return methodID[0] == 0x09 && methodID[1] == 0x5e && methodID[2] == 0xa7 && methodID[3] == 0xb3
}

func isTransferFromMethod(methodID []byte) bool {
	// TransferFrom method signature: 0x23b872dd
	return methodID[0] == 0x23 && methodID[1] == 0xb8 && methodID[2] == 0x72 && methodID[3] == 0xdd
}

func isBalanceOfMethod(methodID []byte) bool {
	// BalanceOf method signature: 0x70a08231
	return methodID[0] == 0x70 && methodID[1] == 0xa0 && methodID[2] == 0x82 && methodID[3] == 0x31
}

func isAllowanceMethod(methodID []byte) bool {
	// Allowance method signature: 0xdd62ed3e
	return methodID[0] == 0xdd && methodID[1] == 0x62 && methodID[2] == 0xed && methodID[3] == 0x3e
}

func isTotalSupplyMethod(methodID []byte) bool {
	// TotalSupply method signature: 0x18160ddd
	return methodID[0] == 0x18 && methodID[1] == 0x16 && methodID[2] == 0x0d && methodID[3] == 0xdd
}

func isMintMethod(methodID []byte) bool {
	// Mint method signature: 0x40c10f19
	return methodID[0] == 0x40 && methodID[1] == 0xc1 && methodID[2] == 0x0f && methodID[3] == 0x19
}

func isBurnMethod(methodID []byte) bool {
	// Burn method signature: 0x42966c68
	return methodID[0] == 0x42 && methodID[1] == 0x96 && methodID[2] == 0x6c && methodID[3] == 0x68
}

// BeginTxn initializes a new Transition using a state snapshot.
func (e *Executor) BeginTxn(
	parentRoot types.Hash,
	header *types.Header,
	coinbaseReceiver types.Address,
) (*Transition, error) {
	if header == nil {
		return nil, fmt.Errorf("header cannot be nil")
	}

	forkConfig := e.config.Forks.At(header.Number)

	auxSnap, err := e.state.NewSnapshotAt(parentRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create state snapshot: %w", err)
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
		logger:      e.logger.Named("transition"),
		ctx:         txCtx,
		state:       newTxn,
		snap:        auxSnap,
		getHash:     e.GetHash(header),
		auxState:    e.state,
		config:      forkConfig,
		gasPool:     uint64(txCtx.GasLimit),
		receipts:    make([]*types.Receipt, 0),
		totalGas:    0,
		evm:         evm.NewEVM(),
		precompiles: precompiled.NewPrecompiled(),
		PostHook:    e.PostHook,
	}

	return txn, nil
}
