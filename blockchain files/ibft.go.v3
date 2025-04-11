package ibft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"

	"github.com/hotenglish775/mainnet/chain"
	"github.com/hotenglish775/mainnet/validators"
)

var (
	ErrNoActiveValidators = errors.New("no active validators")
	ErrNilHeader          = errors.New("header is nil")
	ErrNegativeNumber     = errors.New("header number cannot be negative")
	ErrNoHeaders          = errors.New("no headers provided")
)

// IBFT represents the Istanbul BFT consensus engine
type IBFT struct {
	logger       hclog.Logger
	config       *chain.Params
	validatorSet *validators.ValidatorSet
	currentRound uint64

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc

	roundTimeout time.Duration
	
	// Track consensus state
	height      uint64
	lastBlock   *types.Header
	isProposer  bool
}

// NewIBFT creates a new IBFT consensus instance
func NewIBFT(ctx context.Context, config *chain.Params, logger hclog.Logger, initialValidators []*validators.Validator) *IBFT {
	cctx, cancel := context.WithCancel(ctx)
	
	if config == nil {
		// Safe default if nil config is passed
		config = &chain.Params{
			BlockTime: 2 * time.Second,
		}
	}
	
	return &IBFT{
		logger:        logger.Named("ibft"),
		config:        config,
		validatorSet:  validators.NewValidatorSet(initialValidators),
		currentRound:  0,
		ctx:           cctx,
		cancel:        cancel,
		roundTimeout:  3 * config.BlockTime,
		height:        0,
		lastBlock:     nil,
		isProposer:    false,
	}
}

// VerifyHeader verifies the header is valid
func (i *IBFT) VerifyHeader(header *types.Header) error {
	if header == nil {
		return ErrNilHeader
	}
	if header.Number < 0 {
		return ErrNegativeNumber
	}
	
	// Future: Add signature and timestamp checks
	// Verify the proposer is valid
	_, err := i.GetBlockCreator(header)
	if err != nil {
		return fmt.Errorf("invalid block creator: %w", err)
	}
	
	return nil
}

// ProcessHeaders processes a batch of headers
func (i *IBFT) ProcessHeaders(headers []*types.Header) error {
	if len(headers) == 0 {
		return ErrNoHeaders
	}
	
	for idx, header := range headers {
		if err := i.VerifyHeader(header); err != nil {
			return fmt.Errorf("header verification failed at index %d: %w", idx, err)
		}
		
		// Update IBFT state after processing valid header
		if idx == len(headers)-1 {
			i.mu.Lock()
			i.height = uint64(header.Number)
			i.lastBlock = header
			i.mu.Unlock()
		}
		
		i.logger.Info("Processed header", 
			"number", header.Number, 
			"hash", header.Hash().String(),
			"index", idx,
			"total", len(headers))
	}
	
	return nil
}

// GetBlockCreator returns the creator of the given block header
func (i *IBFT) GetBlockCreator(header *types.Header) (types.Address, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	active := i.validatorSet.GetActiveValidators()
	if len(active) == 0 {
		return types.ZeroAddress, ErrNoActiveValidators
	}
	
	index := int(header.Number % uint64(len(active)))
	creatorAddr := active[index].Address
	
	return types.HexToAddress(creatorAddr), nil
}

// PreCommitState prepares the state before committing
func (i *IBFT) PreCommitState(header *types.Header, txn interface{}) error {
	// This is a placeholder in the original code
	// Future: Add the actual state commitment logic here
	return nil
}

// GetSyncProgression returns synchronization progress information
func (i *IBFT) GetSyncProgression() interface{} {
	// This is a placeholder in the original code
	// Future: Implement actual sync progression tracking
	return nil
}

// Initialize prepares the consensus engine
func (i *IBFT) Initialize() error {
	i.logger.Info("IBFT consensus engine initialized", 
		"validators", len(i.validatorSet.GetActiveValidators()),
		"roundTimeout", i.roundTimeout)
	return nil
}

// Start begins the consensus process
func (i *IBFT) Start() error {
	i.logger.Info("IBFT consensus started", "height", i.height)
	go i.consensusLoop()
	return nil
}

// consensusLoop is the main loop for the consensus process
func (i *IBFT) consensusLoop() {
	i.logger.Debug("Consensus loop started")
	
	for {
		select {
		case <-i.ctx.Done():
			i.logger.Info("Consensus loop stopped")
			return
		default:
			if err := i.runRoundWithTimeout(); err != nil {
				i.logger.Error("Error in consensus round", "error", err)
			}
			
			// Small sleep to prevent CPU spinning in case of continuous errors
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runRoundWithTimeout executes a single round with a timeout
func (i *IBFT) runRoundWithTimeout() error {
	i.mu.Lock()
	current := i.currentRound
	i.mu.Unlock()

	i.logger.Info("Starting round", "round", current, "height", i.height)

	roundCtx, cancel := context.WithTimeout(i.ctx, i.roundTimeout)
	defer cancel()

	// Get last proposer if available
	var lastProposer types.Address
	if i.lastBlock != nil {
		var err error
		lastProposer, err = i.GetBlockCreator(i.lastBlock)
		if err != nil {
			i.logger.Warn("Failed to get last proposer", "error", err)
			// Continue with zero address as fallback
		}
	}
	
	// Calculate the proposer for this round
	proposer, err := i.CalculateProposer(current, lastProposer)
	if err != nil {
		i.logger.Error("Failed to calculate proposer", "error", err)
		i.advanceRound()
		return err
	}

	i.mu.Lock()
	i.isProposer = proposer.Address == i.config.SelfAddress // Assuming config has SelfAddress
	i.mu.Unlock()

	i.logger.Info("Selected proposer", 
		"address", proposer.Address,
		"round", current,
		"isProposer", i.isProposer)

	// Main round logic
	roundComplete := false
	
	// Future: Add proposal creation if this node is the proposer
	// Future: Add vote/proposal receipt handling here
	
	select {
	case <-roundCtx.Done():
		if roundComplete {
			i.logger.Info("Round completed successfully", "round", current)
		} else {
			i.logger.Warn("Round timed out", "round", current)
			i.advanceRound()
		}
	}
	
	return nil
}

// advanceRound moves to the next consensus round
func (i *IBFT) advanceRound() {
	i.mu.Lock()
	i.currentRound++
	round := i.currentRound
	i.mu.Unlock()

	i.logger.Info("Advancing to next round", "newRound", round)
}

// Close shuts down the consensus engine
func (i *IBFT) Close() error {
	i.logger.Info("Shutting down IBFT consensus")
	i.cancel()
	i.logger.Info("IBFT stopped")
	return nil
}

// RequiredVotes returns the number of votes required for consensus
func (i *IBFT) RequiredVotes() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.validatorSet.RequiredVotes()
}

// UpdateValidatorSet updates the validator set
func (i *IBFT) UpdateValidatorSet(newValidators []*validators.Validator) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	oldCount := len(i.validatorSet.GetActiveValidators())
	i.validatorSet = validators.NewValidatorSet(newValidators)
	newCount := len(newValidators)
	
	i.logger.Info("Validator set updated", 
		"previous", oldCount,
		"current", newCount)
		
	return nil
}

// CalculateProposer selects the validator to propose the next block
func (i *IBFT) CalculateProposer(round uint64, lastProposer types.Address) (validators.Validator, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	active := i.validatorSet.GetActiveValidators()
	if len(active) == 0 {
		return validators.Validator{}, ErrNoActiveValidators
	}
	
	// Simple round-robin selection
	index := int(round % uint64(len(active)))
	
	// Future: Implement more sophisticated proposer selection based on lastProposer
	
	return *active[index], nil
}
