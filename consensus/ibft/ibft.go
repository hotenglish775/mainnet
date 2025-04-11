package ibft

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"

	"github.com/hotenglish775/mainnet/chain"
	"github.com/hotenglish775/mainnet/validators"
)

// Error variables for messaging.
var (
	ErrNoActiveValidators = errors.New("no active validators")
	ErrNilHeader          = errors.New("header is nil")
	ErrNegativeNumber     = errors.New("header number cannot be negative")
	ErrNoHeaders          = errors.New("no headers provided")
	ErrInvalidProposal    = errors.New("invalid proposal message")
)

// Message Types for IBFT consensus.
type ProposalMsg struct {
	Round    uint64
	Block    *types.Block
	Proposer types.Address
	Signature []byte // Cryptographic signature of the proposer.
}

type PrepareMsg struct {
	Round     uint64
	BlockHash types.Hash
	Validator types.Address
	Signature []byte
}

type CommitMsg struct {
	Round      uint64
	BlockHash  types.Hash
	Validator  types.Address
	CommitSeal []byte
}

type RoundChangeMsg struct {
	NewRound  uint64
	Validator types.Address
	Signature []byte
}

// IBFT represents the enhanced IBFT-based PoS consensus engine with messaging.
type IBFT struct {
	logger       hclog.Logger            // Logger for detailed logs.
	config       *chain.Params           // Chain configuration parameters.
	validatorSet *validators.ValidatorSet // Active validator set.
	currentRound uint64                  // Current consensus round.

	// Messaging data structures.
	mu              sync.RWMutex
	ctx             context.Context // Global context.
	cancel          context.CancelFunc
	roundTimeout    time.Duration   // Timeout duration for each round.

	// Consensus state tracking.
	height     uint64       // Current block height.
	lastBlock  *types.Header // Most recently finalized block.
	isProposer bool          // Flag indicating if this node is the proposer in current round.

	// In-memory message pools.
	proposalMsgs    map[uint64]*ProposalMsg                         // Map by round.
	prepareMsgs     map[uint64]map[types.Address]*PrepareMsg          // Per round, keyed by validator.
	commitMsgs      map[uint64]map[types.Address]*CommitMsg           // Per round, keyed by validator.
	roundChangeMsgs map[uint64]map[types.Address]*RoundChangeMsg      // Per round, keyed by validator.
}

// NewIBFT creates a new IBFT consensus instance.
func NewIBFT(ctx context.Context, config *chain.Params, logger hclog.Logger, initialValidators []*validators.Validator) *IBFT {
	cctx, cancel := context.WithCancel(ctx)
	if config == nil {
		config = &chain.Params{
			BlockTime: 2 * time.Second,
		}
	}
	return &IBFT{
		logger:          logger.Named("ibft"),
		config:          config,
		validatorSet:    validators.NewValidatorSet(initialValidators),
		currentRound:    0,
		ctx:             cctx,
		cancel:          cancel,
		roundTimeout:    3 * config.BlockTime,
		height:          0,
		lastBlock:       nil,
		isProposer:      false,
		proposalMsgs:    make(map[uint64]*ProposalMsg),
		prepareMsgs:     make(map[uint64]map[types.Address]*PrepareMsg),
		commitMsgs:      make(map[uint64]map[types.Address]*CommitMsg),
		roundChangeMsgs: make(map[uint64]map[types.Address]*RoundChangeMsg),
	}
}

// VerifyHeader verifies that the header is valid.
func (i *IBFT) VerifyHeader(header *types.Header) error {
	if header == nil {
		return ErrNilHeader
	}
	if header.Number < 0 {
		return ErrNegativeNumber
	}
	_, err := i.GetBlockCreator(header)
	if err != nil {
		return fmt.Errorf("invalid block creator: %w", err)
	}
	return nil
}

// ProcessHeaders processes an array of headers.
func (i *IBFT) ProcessHeaders(headers []*types.Header) error {
	if len(headers) == 0 {
		return ErrNoHeaders
	}
	for idx, header := range headers {
		if err := i.VerifyHeader(header); err != nil {
			return fmt.Errorf("header verification failed at index %d: %w", idx, err)
		}
		// Update state for the last header processed.
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

// GetBlockCreator returns the creator of a given block header by selecting one from the active validator set.
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

// PreCommitState is a hook executed before state transition finalization.
func (i *IBFT) PreCommitState(header *types.Header, txn interface{}) error {
	// Future: Implement state adjustments before commit.
	return nil
}

// GetSyncProgression returns sync progression details.
func (i *IBFT) GetSyncProgression() interface{} {
	// Future: Return actual sync progression info.
	return nil
}

// Initialize sets up the IBFT consensus engine.
func (i *IBFT) Initialize() error {
	i.logger.Info("IBFT consensus engine initialized",
		"validators", len(i.validatorSet.GetActiveValidators()),
		"roundTimeout", i.roundTimeout)
	return nil
}

// Start begins the consensus process.
func (i *IBFT) Start() error {
	i.logger.Info("IBFT consensus started", "height", i.height)
	go i.consensusLoop()
	return nil
}

// consensusLoop runs the main consensus round loop with timeout-based round management.
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
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// runRoundWithTimeout executes a consensus round with timeout and messaging.
func (i *IBFT) runRoundWithTimeout() error {
	i.mu.Lock()
	current := i.currentRound
	i.mu.Unlock()

	i.logger.Info("Starting round", "round", current, "height", i.height)

	roundCtx, cancel := context.WithTimeout(i.ctx, i.roundTimeout)
	defer cancel()

	// Broadcast Proposal if this node is proposer.
	// For simplicity, we assume that if our node is the proposer,
	// i.logger.Info logs the proposal sending.
	proposer, err := i.CalculateProposer(current, types.ZeroAddress)
	if err != nil {
		i.logger.Error("Failed to calculate proposer", "error", err)
		i.advanceRound()
		return err
	}

	// Mark proposer flag based on configuration (assuming config has SelfAddress).
	i.mu.Lock()
	if types.HexToAddress(proposer.Address) == types.HexToAddress(i.config.SelfAddress) {
		i.isProposer = true
	} else {
		i.isProposer = false
	}
	i.mu.Unlock()

	i.logger.Info("Selected proposer", "address", proposer.Address, "round", current, "isProposer", i.isProposer)

	// Broadcast the proposal message.
	if i.isProposer {
		if err := i.broadcastProposal(current); err != nil {
			i.logger.Error("Failed to broadcast proposal", "error", err)
			i.advanceRound()
			return err
		}
	}

	// Wait for commit messages until round timeout.
	select {
	case <-roundCtx.Done():
		// Check if sufficient commits have been collected.
		if !i.hasQuorum(current) {
			i.logger.Warn("Round timed out without quorum", "round", current)
			i.broadcastRoundChange(current)
			i.advanceRound()
		} else {
			i.logger.Info("Round completed successfully", "round", current)
		}
	}
	return nil
}

// advanceRound safely advances to the next consensus round.
func (i *IBFT) advanceRound() {
	i.mu.Lock()
	i.currentRound++
	i.mu.Unlock()
	i.logger.Info("Advancing to next round", "newRound", i.currentRound)
}

// Close shuts down the consensus engine.
func (i *IBFT) Close() error {
	i.logger.Info("Shutting down IBFT consensus")
	i.cancel()
	i.logger.Info("IBFT stopped")
	return nil
}

// RequiredVotes returns the number of votes required for block finality.
func (i *IBFT) RequiredVotes() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.validatorSet.RequiredVotes()
}

// UpdateValidatorSet replaces the current validator set with a new set.
func (i *IBFT) UpdateValidatorSet(newValidators []*validators.Validator) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	oldCount := len(i.validatorSet.GetActiveValidators())
	i.validatorSet = validators.NewValidatorSet(newValidators)
	newCount := len(newValidators)
	i.logger.Info("Validator set updated", "previous", oldCount, "current", newCount)
	return nil
}

// CalculateProposer selects the validator for the given round.
func (i *IBFT) CalculateProposer(round uint64, lastProposer types.Address) (validators.Validator, error) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	active := i.validatorSet.GetActiveValidators()
	if len(active) == 0 {
		return validators.Validator{}, ErrNoActiveValidators
	}
	index := int(round % uint64(len(active)))
	return *active[index], nil
}

// --- Message Broadcasting Functions ---

// broadcastProposal broadcasts a proposal message for the current round.
func (i *IBFT) broadcastProposal(round uint64) error {
	// Assemble the proposal message.
	// For production, include cryptographic signing of block and round data.
	proposal := &ProposalMsg{
		Round:    round,
		Block:    i.assembleBlockProposal(round),
		Proposer: types.HexToAddress(i.config.SelfAddress),
		Signature: []byte("signature"), // Replace with actual signature.
	}
	i.mu.Lock()
	i.proposalMsgs[round] = proposal
	i.mu.Unlock()
	i.logger.Info("Proposal broadcast", "round", round, "proposer", proposal.Proposer.String())
	// In production, broadcast over the network.
	return nil
}

// broadcastPrepare broadcasts a prepare message for a given round and block hash.
func (i *IBFT) broadcastPrepare(round uint64, blockHash types.Hash) error {
	prepare := &PrepareMsg{
		Round:     round,
		BlockHash: blockHash,
		Validator: types.HexToAddress(i.config.SelfAddress),
		Signature: []byte("prepare-signature"),
	}
	i.mu.Lock()
	if i.prepareMsgs[round] == nil {
		i.prepareMsgs[round] = make(map[types.Address]*PrepareMsg)
	}
	i.prepareMsgs[round][prepare.Validator] = prepare
	i.mu.Unlock()
	i.logger.Info("Prepare message broadcast", "round", round, "validator", prepare.Validator.String())
	return nil
}

// broadcastCommit broadcasts a commit message for a given round and block hash.
func (i *IBFT) broadcastCommit(round uint64, blockHash types.Hash) error {
	commit := &CommitMsg{
		Round:      round,
		BlockHash:  blockHash,
		Validator:  types.HexToAddress(i.config.SelfAddress),
		CommitSeal: []byte("commit-seal"),
	}
	i.mu.Lock()
	if i.commitMsgs[round] == nil {
		i.commitMsgs[round] = make(map[types.Address]*CommitMsg)
	}
	i.commitMsgs[round][commit.Validator] = commit
	i.mu.Unlock()
	i.logger.Info("Commit message broadcast", "round", round, "validator", commit.Validator.String())
	return nil
}

// broadcastRoundChange broadcasts a round-change message for the current round.
func (i *IBFT) broadcastRoundChange(round uint64) error {
	roundChange := &RoundChangeMsg{
		NewRound:  round + 1,
		Validator: types.HexToAddress(i.config.SelfAddress),
		Signature: []byte("round-change-signature"),
	}
	i.mu.Lock()
	if i.roundChangeMsgs[round] == nil {
		i.roundChangeMsgs[round] = make(map[types.Address]*RoundChangeMsg)
	}
	i.roundChangeMsgs[round][roundChange.Validator] = roundChange
	i.mu.Unlock()
	i.logger.Info("Round-Change message broadcast", "oldRound", round, "newRound", roundChange.NewRound)
	return nil
}

// hasQuorum checks if the number of commit messages meets the required votes.
func (i *IBFT) hasQuorum(round uint64) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()
	commits, exists := i.commitMsgs[round]
	if !exists {
		return false
	}
	return len(commits) >= i.validatorSet.RequiredVotes()
}

// assembleBlockProposal constructs a block proposal for the current round.
// In production, this would gather transactions and form a complete block.
func (i *IBFT) assembleBlockProposal(round uint64) *types.Block {
	// Placeholder: construct a dummy block proposal based on current height and round.
	header := &types.Header{
		Number: int64(i.height + 1),
		Timestamp: uint64(time.Now().Unix()),
		GasLimit: i.config.GasLimit,
	}
	// In production, populate the block with transactions.
	return &types.Block{
		Header: header,
	}
}
