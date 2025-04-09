package blockchain

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/0xPolygon/polygon-edge/blockchain/types"
	"github.com/0xPolygon/polygon-edge/consensus"
	"github.com/0xPolygon/polygon-edge/types/txpool"
	"github.com/0xPolygon/polygon-edge/utils"
)

var (
	// ErrNoBlocks is returned when the blockchain has no blocks
	ErrNoBlocks = errors.New("blockchain contains no blocks")
	// ErrInvalidBlock is returned when a block fails validation
	ErrInvalidBlock = errors.New("invalid block")
	// ErrExecutionCancelled is returned when block execution is cancelled
	ErrExecutionCancelled = errors.New("block execution cancelled")
	// MaxParallelTxs limits the number of concurrent transaction executions
	MaxParallelTxs = runtime.NumCPU() * 4
)

// BlockTimeManager provides dynamic block time control
type BlockTimeManager interface {
	GetCurrentBlockTime() time.Duration
}

// AntiRugpullGuard validates transactions for malicious behavior
type AntiRugpullGuard interface {
	IsSafe(tx *types.Transaction) bool
}

// ParallelTxExecutor handles parallel processing logic
type ParallelTxExecutor struct {
	workerLimit chan struct{}
}

// NewParallelTxExecutor creates a new executor with concurrency limits
func NewParallelTxExecutor() *ParallelTxExecutor {
	return &ParallelTxExecutor{
		workerLimit: make(chan struct{}, MaxParallelTxs),
	}
}

func (p *ParallelTxExecutor) Execute(ctx context.Context, txs []*types.Transaction) ([]*types.Receipt, error) {
	if len(txs) == 0 {
		return []*types.Receipt{}, nil
	}

	receipts := make([]*types.Receipt, len(txs))
	errChan := make(chan error, 1)
	var completed int32
	
	for i, tx := range txs {
		// Check for cancellation before starting each transaction
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case p.workerLimit <- struct{}{}: // Acquire worker slot
			// Continue with execution
		}

		go func(index int, transaction *types.Transaction) {
			defer func() {
				<-p.workerLimit // Release worker slot
				
				// If all transactions are processed, close error channel
				if atomic.AddInt32(&completed, 1) == int32(len(txs)) {
					close(errChan)
				}
			}()

			// Check context cancellation
			if ctx.Err() != nil {
				select {
				case errChan <- ctx.Err():
				default:
					// Channel already has an error
				}
				return
			}

			// Execute transaction (replace with real execution logic)
			receipt := &types.Receipt{
				TxHash:  transaction.Hash,
				Success: true,
				GasUsed: transaction.Gas,
			}

			// Store result at the correct index (no mutex needed)
			receipts[index] = receipt
		}(i, tx)
	}

	// Wait for completion or error
	if err := <-errChan; err != nil {
		return nil, err
	}
	
	return receipts, nil
}

// Blockchain represents the main blockchain structure
type Blockchain struct {
	chain          []*types.Block
	latestBlock    atomic.Value // *types.Block
	txPool         txpool.TxPool
	engine         consensus.Engine
	blockTime      BlockTimeManager
	rugpull        AntiRugpullGuard
	parallelExec   *ParallelTxExecutor
	lock           sync.RWMutex
	tickerLock     sync.Mutex
	currentTicker  *time.Ticker
	stopChan       chan struct{}
}

// NewBlockchain creates a new blockchain instance
func NewBlockchain(engine consensus.Engine, txPool txpool.TxPool, blockTime BlockTimeManager, rugpull AntiRugpullGuard) (*Blockchain, error) {
	// Validate required interfaces
	if engine == nil || txPool == nil || blockTime == nil || rugpull == nil {
		return nil, errors.New("missing required dependencies")
	}

	bc := &Blockchain{
		chain:        make([]*types.Block, 0),
		txPool:       txPool,
		engine:       engine,
		blockTime:    blockTime,
		rugpull:      rugpull,
		parallelExec: NewParallelTxExecutor(),
		stopChan:     make(chan struct{}),
	}
	
	// Initialize with genesis block if needed
	if len(bc.chain) == 0 {
		genesis := createGenesisBlock()
		bc.chain = append(bc.chain, genesis)
		bc.latestBlock.Store(genesis)
	}
	
	return bc, nil
}

// Start begins the blockchain block production process
func (bc *Blockchain) Start(ctx context.Context) error {
	bc.tickerLock.Lock()
	if bc.currentTicker != nil {
		bc.tickerLock.Unlock()
		return errors.New("blockchain already started")
	}
	
	blockTime := bc.blockTime.GetCurrentBlockTime()
	ticker := time.NewTicker(blockTime)
	bc.currentTicker = ticker
	bc.tickerLock.Unlock()
	
	go func() {
		for {
			select {
			case <-ctx.Done():
				bc.stopBlockchain()
				return
			case <-bc.stopChan:
				return
			case <-ticker.C:
				if err := bc.MineBlock(ctx); err != nil && err != context.Canceled {
					utils.Logger().Error("block mining failed", "err", err)
				}
				
				// Check if block time needs updating
				bc.updateTickerIfNeeded()
			}
		}
	}()
	
	return nil
}

// Stop halts the blockchain process
func (bc *Blockchain) Stop() {
	bc.stopBlockchain()
}

func (bc *Blockchain) stopBlockchain() {
	bc.tickerLock.Lock()
	defer bc.tickerLock.Unlock()
	
	if bc.currentTicker != nil {
		bc.currentTicker.Stop()
		bc.currentTicker = nil
		close(bc.stopChan)
		bc.stopChan = make(chan struct{})
	}
}

// updateTickerIfNeeded checks and updates the ticker if block time has changed
func (bc *Blockchain) updateTickerIfNeeded() {
	newBlockTime := bc.blockTime.GetCurrentBlockTime()
	
	bc.tickerLock.Lock()
	defer bc.tickerLock.Unlock()
	
	// Only update if we have a current ticker and the time has changed
	if bc.currentTicker != nil {
		currentBlockTime := bc.blockTime.GetCurrentBlockTime()
		if currentBlockTime != newBlockTime {
			bc.currentTicker.Stop()
			bc.currentTicker = time.NewTicker(newBlockTime)
			utils.Logger().Info("Block time updated", "new_time", newBlockTime)
		}
	}
}

// MineBlock creates a new block with pending transactions
func (bc *Blockchain) MineBlock(ctx context.Context) error {
	bc.lock.Lock()
	defer bc.lock.Unlock()
	
	// Get pending transactions from pool
	pendingTxs := bc.txPool.GetPending()
	
	// Flatten the pending map into a slice
	var allTxs []*types.Transaction
	for _, addrTxs := range pendingTxs {
		allTxs = append(allTxs, addrTxs...)
	}
	
	// Apply anti-rugpull protection
	var safeTxs []*types.Transaction
	var rejectedTxs []*types.Transaction
	
	for _, tx := range allTxs {
		if bc.rugpull != nil && bc.rugpull.IsSafe(tx) {
			safeTxs = append(safeTxs, tx)
		} else {
			rejectedTxs = append(rejectedTxs, tx)
			utils.Logger().Warn("Transaction rejected by rugpull protection", "txHash", tx.Hash)
		}
	}
	
	// Execute transactions in parallel
	receipts, err := bc.parallelExec.Execute(ctx, safeTxs)
	if err != nil {
		return err
	}
	
	latestBlock := bc.GetLatestBlock()
	if latestBlock == nil {
		return ErrNoBlocks
	}
	
	// Create the new block
	newBlock := &types.Block{
		Header: &types.Header{
			Number:     latestBlock.Header.Number + 1,
			Timestamp:  uint64(time.Now().Unix()),
			ParentHash: latestBlock.Hash(),
		},
		Transactions: safeTxs,
		Receipts:     receipts,
	}
	
	// Calculate the block hash
	newBlock.Header.Hash = calculateBlockHash(newBlock)
	
	// Validate block with consensus engine
	if err := bc.engine.VerifyBlock(newBlock); err != nil {
		return ErrInvalidBlock
	}
	
	// Insert into chain
	bc.chain = append(bc.chain, newBlock)
	bc.latestBlock.Store(newBlock)
	
	// Remove processed transactions from pool
	bc.txPool.RemoveTransactions(safeTxs)
	
	utils.Logger().Info("Block mined successfully", 
		"number", newBlock.Header.Number,
		"txs", len(safeTxs),
		"rejected", len(rejectedTxs),
		"hash", newBlock.Header.Hash)
	
	return nil
}

// GetLatestBlock returns the most recent block
func (bc *Blockchain) GetLatestBlock() *types.Block {
	if block := bc.latestBlock.Load(); block != nil {
		return block.(*types.Block)
	}
	
	// Fall back to chain lookup if atomic value isn't set
	bc.lock.RLock()
	defer bc.lock.RUnlock()
	
	if len(bc.chain) == 0 {
		return nil
	}
	
	return bc.chain[len(bc.chain)-1]
}

// GetBlockByNumber returns a block at the specified height
func (bc *Blockchain) GetBlockByNumber(number uint64) *types.Block {
	bc.lock.RLock()
	defer bc.lock.RUnlock()
	
	if number >= uint64(len(bc.chain)) {
		return nil
	}
	
	return bc.chain[number]
}

// GetBlockByHash returns a block with the specified hash
func (bc *Blockchain) GetBlockByHash(hash types.Hash) *types.Block {
	bc.lock.RLock()
	defer bc.lock.RUnlock()
	
	for _, block := range bc.chain {
		if block.Header.Hash == hash {
			return block
		}
	}
	
	return nil
}

// GetBlockCount returns the current blockchain length
func (bc *Blockchain) GetBlockCount() uint64 {
	bc.lock.RLock()
	defer bc.lock.RUnlock()
	return uint64(len(bc.chain))
}

// calculateBlockHash computes the hash of a block
func calculateBlockHash(block *types.Block) types.Hash {
	// In a real implementation, this would use a proper hashing algorithm
	// considering block header fields and transaction merkle root
	// For now, we'll create a placeholder hash
	return types.ComputeBlockHash(block.Header)
}

// createGenesisBlock generates the first block in the chain
func createGenesisBlock() *types.Block {
	genesisHeader := &types.Header{
		Number:    0,
		Timestamp: uint64(time.Now().Unix()),
	}
	
	genesis := &types.Block{
		Header:       genesisHeader,
		Transactions: []*types.Transaction{},
		Receipts:     []*types.Receipt{},
	}
	
	// Set the genesis hash
	genesis.Header.Hash = calculateBlockHash(genesis)
	return genesis
}
