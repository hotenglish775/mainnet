package consensus

import (
	"context"
	"log"
	"time"
	"github.com/0xPolygon/polygon-edge/blockchain"
	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/helper/progress"
	"github.com/0xPolygon/polygon-edge/network"
	"github.com/0xPolygon/polygon-edge/secrets"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/txpool"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
	"google.golang.org/grpc"
)

// Consensus is the public interface for consensus mechanism
// Each consensus mechanism must implement this interface in order to be valid
type Consensus interface {
	// VerifyHeader verifies the header is correct (e.g., timestamp, signature, validator)
	VerifyHeader(header *types.Header) error
	// ProcessHeaders updates the validator snapshot based on verified headers
	ProcessHeaders(headers []*types.Header) error
	// GetBlockCreator retrieves the block creator (validator) for the given block
	GetBlockCreator(header *types.Header) (types.Address, error)
	// PreCommitState hook before finalizing the block
	PreCommitState(header *types.Header, txn *state.Transition) error
	// GetSyncProgression returns sync progress (used during node catch-up)
	GetSyncProgression() *progress.Progression
	// Initialize sets up consensus-specific state/data
	Initialize() error
	// Start launches the consensus mechanism loop and its network services
	Start() error
	// Close terminates the consensus services
	Close() error
}

// Config holds the configuration for the consensus
type Config struct {
	Logger hclog.Logger           // Logger for consensus-level logging
	Params *chain.Params          // Chain parameters (e.g., block time, genesis validators)
	Config map[string]interface{} // Consensus-specific key-values
	Path   string                 // File path for consensus storage
}

// Params encapsulates parameters required to start the consensus mechanism
type Params struct {
	Context        context.Context
	Config         *Config
	TxPool         *txpool.TxPool
	Network        *network.Server
	Blockchain     *blockchain.Blockchain
	Executor       *state.Executor
	Grpc           *grpc.Server
	Logger         hclog.Logger
	SecretsManager secrets.SecretsManager
	BlockTime      time.Duration // Changed from uint64 to time.Duration for consistency
}

// Factory is a function to instantiate a new consensus mechanism
type Factory func(*Params) (Consensus, error)

// Constants to enforce your 0.2 second blocks and advanced security features
const (
	TargetBlockTime = 200 * time.Millisecond // 0.2s block production interval
)

// ConsensusType represents the type of consensus mechanism being used
type ConsensusType string

// Available consensus mechanisms
const (
	PoA ConsensusType = "poa"
	PoS ConsensusType = "pos"
)

// ConsensusImplementation ensures implementations satisfy the Consensus interface at compile time
var _ Consensus = (*ConsensusImplementation)(nil)

// ConsensusImplementation is a placeholder struct to ensure interface compliance
type ConsensusImplementation struct{}

// CreateConsensus initializes a consensus mechanism based on provided parameters
func CreateConsensus(consensusType ConsensusType, params *Params) (Consensus, error) {
	if params == nil {
		return nil, ErrParamsNil
	}

	// Check for required dependencies
	if err := validateDependencies(params); err != nil {
		return nil, err
	}

	// Create appropriate consensus based on type
	switch consensusType {
	case PoA, PoS:
		// These would be implemented elsewhere and returned by factories
		return nil, ErrConsensusNotImplemented
	default:
		return nil, ErrInvalidConsensusType
	}
}

// ErrParamsNil is returned when consensus parameters are nil
var ErrParamsNil = consensusError("consensus parameters cannot be nil")

// ErrConsensusNotImplemented is returned when a consensus type is not implemented
var ErrConsensusNotImplemented = consensusError("consensus type not implemented")

// ErrInvalidConsensusType is returned when an invalid consensus type is specified
var ErrInvalidConsensusType = consensusError("invalid consensus type")

// consensusError represents a consensus-related error
type consensusError string

// Error returns the error message
func (e consensusError) Error() string {
	return string(e)
}

// validateDependencies ensures all required dependencies are present
func validateDependencies(params *Params) error {
	if params.Logger == nil {
		return consensusError("logger cannot be nil")
	}
	if params.Blockchain == nil {
		return consensusError("blockchain cannot be nil")
	}
	if params.TxPool == nil {
		return consensusError("transaction pool cannot be nil")
	}
	if params.Network == nil {
		return consensusError("network cannot be nil")
	}
	if params.Executor == nil {
		return consensusError("executor cannot be nil")
	}
	return nil
}
