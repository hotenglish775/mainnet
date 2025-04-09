package ibft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hotenglish775/mainnet/chain"
	"github.com/hotenglish775/mainnet/consensus/state"
	"github.com/hotenglish775/mainnet/helper/progress"
	"github.com/hotenglish775/mainnet/validators"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
)

// Common errors
var (
	ErrNilHeader          = errors.New("header is nil")
	ErrNoHeadersProvided  = errors.New("no headers provided")
	ErrNoActiveValidators = errors.New("no active validators available")
)

// IBFT represents our enhanced IBFT-based PoS consensus engine.
type IBFT struct {
	logger       hclog.Logger             // Logger for debug/info messages.
	config       *chain.Params            // Chain configuration parameters.
	validatorSet *validators.ValidatorSet // Dynamic set of active validators.
	currentRound uint64                   // Current round of consensus.
	mu           sync.RWMutex             // Protects dynamic updates to the validator set.
	ctx          context.Context          // Global context.
	cancel       context.CancelFunc       // Function to cancel the global context.
	isRunning    bool                     // Flag to track if consensus loop is running.
}

// NewIBFT creates a new IBFT consensus instance using the given chain params, logger, and an initial list of validators.
func NewIBFT(ctx context.Context, config *chain.Params, logger hclog.Logger, initialValidators []*validators.Validator) *IBFT {
	if logger == nil {
		logger = hclog.NewNullLogger()
	}

	if config == nil {
		config = chain.DefaultChainParams()
	}

	cctx, cancel := context.WithCancel(ctx)
	return &IBFT{
		logger:       logger.Named("ibft"),
		config:       config,
		validatorSet: validators.NewValidatorSet(initialValidators),
		currentRound: 0,
		ctx:          cctx,
		cancel:       cancel,
		isRunning:    false,
	}
}

// VerifyHeader verifies the block header to ensure it meets consensus rules.
func (i *IBFT) VerifyHeader(header *types.Header) error {
	if header == nil {
		return ErrNilHeader
	}

	// Check block timestamp
	if header.Timestamp == 0 {
		return errors.New("invalid block timestamp")
	}

	// Check if the extra data is valid IBFT extra data
	if err := i.verifyExtraData(header); err != nil {
		return fmt.Errorf("invalid extra data: %w", err)
	}

	// Verify the validator signature in the header
	if err := i.verifySignature(header); err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	return nil
}

// verifyExtraData validates the IBFT-specific extra data in the header
func (i *IBFT) verifyExtraData(header *types.Header) error {
	// In a real implementation, this would parse and verify the IBFT extra data
	// which typically contains round information, committed seals, etc.
	return nil
}

// verifySignature verifies that the block was created by a valid validator
func (i *IBFT) verifySignature(header *types.Header) error {
	// In a real implementation, this would verify the cryptographic signature
	// of the validator who created this block
	return nil
}

// ProcessHeaders updates internal consensus state based on the verified headers.
func (i *IBFT) ProcessHeaders(headers []*types.Header) error {
	if len(headers) == 0 {
		return ErrNoHeadersProvided
	}

	for _, header := range headers {
		if err := i.VerifyHeader(header); err != nil {
			return fmt.Errorf("header verification failed for header number %d: %w", header.Number, err)
		}
		
		i.logger.Debug("Processing header", "number", header.Number, "hash", header.Hash().String())
		
		// Update the validator set if the header contains validator updates
		if err := i.processValidatorUpdates(header); err != nil {
			return fmt.Errorf("failed to process validator updates: %w", err)
		}
	}
	
	return nil
}

// processValidatorUpdates checks if the header contains validator updates and applies them
func (i *IBFT) processValidatorUpdates(header *types.Header) error {
	// In a real implementation, this would extract validator changes from the header
	// and update the validator set accordingly
	return nil
}

// GetBlockCreator retrieves the block creator for the given header by selecting one from the active validator set.
func (i *IBFT) GetBlockCreator(header *types.Header) (types.Address, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	active := i.validatorSet.GetActiveValidators()
	if len(active) == 0 {
		return types.ZeroAddress, ErrNoActiveValidators
	}
	
	// Simple round-robin selection: use (header.Number mod active count) as the index.
	index := int(header.Number % uint64(len(active)))
	creatorAddr := active[index].Address
	
	return types.HexToAddress(creatorAddr), nil
}

// PreCommitState is a hook executed before state transition finalization.
func (i *IBFT) PreCommitState(header *types.Header, txn *state.Transition) error {
	if header == nil {
		return ErrNilHeader
	}
	
	if txn == nil {
		return errors.New("state transition is nil")
	}
	
	// Apply any IBFT-specific state changes before the block is committed
	// For example, distributing block rewards to validators
	return nil
}

// GetSyncProgression returns current synchronization progress.
func (i *IBFT) GetSyncProgression() *progress.Progression {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	// In a production implementation, this would return actual sync progress
	return &progress.Progression{
		CurrentBlock: i.currentRound,
		HighestBlock: i.currentRound, // In a synced state, these would be equal
		StartingBlock: 0,
	}
}

// Initialize sets up the consensus engine for operation.
func (i *IBFT) Initialize() error {
	i.logger.Info("IBFT consensus initialized")
	
	// Validate configuration
	if i.config.BlockTime < 1*time.Second {
		i.logger.Warn("Block time is very low, may cause performance issues", "blockTime", i.config.BlockTime)
	}
	
	// Initialize any additional components needed
	return nil
}

// Start begins the consensus operations (e.g., periodic rounds, proposer selection).
func (i *IBFT) Start() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	
	if i.isRunning {
		return errors.New("consensus already running")
	}
	
	i.logger.Info("IBFT consensus started")
	i.isRunning = true
	
	// Start the consensus loop in a goroutine
	go i.consensusLoop()
	
	return nil
}

// consensusLoop runs a basic consensus round loop for demonstration.
func (i *IBFT) consensusLoop() {
	ticker := time.NewTicker(i.config.BlockTime)
	defer ticker.Stop()

	for {
		select {
		case <-i.ctx.Done():
			i.logger.Info("Consensus loop stopped")
			return
		case <-ticker.C:
			if err := i.runConsensusRound(); err != nil {
				i.logger.Error("Error in consensus round", "error", err)
			}
		}
	}
}

// runConsensusRound executes a single consensus round
func (i *IBFT) runConsensusRound() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	
	i.currentRound++
	i.logger.Debug("New consensus round", "round", i.currentRound)
	
	// 1. Calculate proposer for this round
	proposer, err := i.CalculateProposer(i.currentRound, types.ZeroAddress)
	if err != nil {
		return fmt.Errorf("failed to calculate proposer: %w", err)
	}
	
	// 2. If we're the proposer, create and propose a block
	// 3. Collect votes from validators
	// 4. Finalize block if sufficient votes received
	
	// Note: In a full implementation, this would contain the complete IBFT consensus algorithm
	
	return nil
}

// Close stops the consensus engine and cleans up resources.
func (i *IBFT) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	
	if !i.isRunning {
		return nil
	}
	
	i.cancel()
	i.isRunning = false
	i.logger.Info("IBFT consensus closed")
	
	return nil
}

// RequiredVotes returns the number of votes needed to achieve consensus.
func (i *IBFT) RequiredVotes() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	return i.validatorSet.RequiredVotes()
}

// UpdateValidatorSet replaces the current validator set with a new set.
func (i *IBFT) UpdateValidatorSet(newValidators []*validators.Validator) error {
	if newValidators == nil {
		return errors.New("validator list cannot be nil")
	}
	
	i.mu.Lock()
	defer i.mu.Unlock()
	
	newSet := validators.NewValidatorSet(newValidators)
	if i.validatorSet.Equal(newSet) {
		return nil // No change in validator set
	}
	
	i.validatorSet = newSet
	i.logger.Info("Validator set updated", "activeValidators", len(i.validatorSet.GetActiveValidators()))
	
	return nil
}

// CalculateProposer selects the next block proposer based on the current round and validator set.
// It returns the selected validator.
func (i *IBFT) CalculateProposer(round uint64, lastProposer types.Address) (validators.Validator, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	
	active := i.validatorSet.GetActiveValidators()
	if len(active) == 0 {
		return validators.Validator{}, ErrNoActiveValidators
	}
	
	// For production systems, a more sophisticated selection algorithm would be used
	// that accounts for previous proposers and validator weights/stakes
	index := int(round % uint64(len(active)))
	
	return *active[index], nil
}
