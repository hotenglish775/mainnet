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
	Logger *log.Logger            // Logger for consensus-level logging
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
	BlockTime      uint64
}

// Factory is a function to instantiate a new consensus mechanism
type Factory func(*Params) (Consensus, error)

// Constants to enforce your 0.2 second blocks and advanced security features
const (
	TargetBlockTime = 200 * time.Millisecond // 0.2s block production interval
)
