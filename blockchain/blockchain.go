package blockchain

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/0xPolygon/polygon-edge/blockchain/types"
	"github.com/0xPolygon/polygon-edge/consensus"
	"github.com/0xPolygon/polygon-edge/types/txpool"
	"github.com/0xPolygon/polygon-edge/utils"
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
type ParallelTxExecutor struct{}

func (p *ParallelTxExecutor) Execute(ctx context.Context, txs []*types.Transaction) ([]*types.Receipt, error) {
	// In production, this would check for conflicting states, mutex keys, etc.
	var receipts []*types.Receipt
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, tx := range txs {
		wg.Add(1)
		go func(tx *types.Transaction) {
			defer wg.Done()

			// Simulate receipt (replace with real execution engine)
			receipt := &types.Receipt{
				TxHash: tx.Hash,
				Success: true,
			}

			mu.Lock()
			receipts = append(receipts, receipt)
			mu.Unlock()
		}(tx)
	}

	wg.Wait()
	return receipts, nil
}

type Blockchain struct {
	chain       []*types.Block
	txPool      txpool.TxPool
	engine      consensus.Engine
	blockTime   BlockTimeManager
	rugpull     AntiRugpullGuard
	parallelExec *ParallelTxExecutor
	lock        sync.Mutex
}

func NewBlockchain(engine consensus.Engine, txPool txpool.TxPool, blockTime BlockTimeManager, rugpull AntiRugpullGuard) *Blockchain {
	return &Blockchain{
		chain:       make([]*types.Block, 0),
		engine:      engine,
		txPool:      txPool,
		blockTime:   blockTime,
		rugpull:     rugpull,
		parallelExec: &ParallelTxExecutor{},
	}
}

func (bc *Blockchain) Start(ctx context.Context) {
	ticker := time.NewTicker(bc.blockTime.GetCurrentBlockTime())

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				bc.lock.Lock()
				if err := bc.mineBlock(ctx); err != nil {
					utils.Logger().Error("block mining failed", "err", err)
				}
				ticker.Reset(bc.blockTime.GetCurrentBlockTime())
				bc.lock.Unlock()
			}
		}
	}()
}

func (bc *Blockchain) mineBlock(ctx context.Context) error {
	pendingTxs := bc.txPool.Pending()

	// Anti-rugpull validation
	safeTxs := []*types.Transaction{}
	for _, tx := range pendingTxs {
		if bc.rugpull.IsSafe(tx) {
			safeTxs = append(safeTxs, tx)
		} else {
			utils.Logger().Warn("rugpull tx blocked", "tx", tx.Hash)
		}
	}

	// Parallel execution
	receipts, err := bc.parallelExec.Execute(ctx, safeTxs)
	if err != nil {
		return err
	}

	newBlock := &types.Block{
		Header: &types.Header{
			Number:     uint64(len(bc.chain)),
			Timestamp:  uint64(time.Now().Unix()),
			ParentHash: bc.getLatestBlockHash(),
		},
		Transactions: safeTxs,
		Receipts:     receipts,
	}

	// Insert into chain
	bc.chain = append(bc.chain, newBlock)
	utils.Logger().Info("Block mined", "number", newBlock.Number)

	return nil
}

func (bc *Blockchain) getLatestBlockHash() types.Hash {
	if len(bc.chain) == 0 {
		return types.ZeroHash
	}
	return bc.chain[len(bc.chain)-1].Hash()
}

func (bc *Blockchain) GetLatestBlock() *types.Block {
	if len(bc.chain) == 0 {
		return nil
	}
	return bc.chain[len(bc.chain)-1]
}
