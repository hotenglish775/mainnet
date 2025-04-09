package rewards

import (
	"errors"
	"math/big"

	"github.com/0xPolygon/polygon-edge/chain"
	"github.com/0xPolygon/polygon-edge/state"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hashicorp/go-hclog"
)

var (
	// ErrInsufficientFunds is returned when there are not enough funds to distribute rewards
	ErrInsufficientFunds = errors.New("insufficient funds for reward distribution")
	// ErrInvalidSlashPercentage is returned when an invalid slashing percentage is provided
	ErrInvalidSlashPercentage = errors.New("invalid slash percentage, must be 0-100")
	// ErrSlashingTooHigh is returned when attempting to slash more than the available balance
	ErrSlashingTooHigh = errors.New("cannot slash more than available stake")
	// ErrNilState is returned when a nil state is provided
	ErrNilState = errors.New("state cannot be nil")
	// ErrNilReward is returned when a nil reward amount is provided
	ErrNilReward = errors.New("reward amount cannot be nil")
	// ErrZeroAddress is returned when the zero address is provided as a block producer
	ErrZeroAddress = errors.New("block producer address cannot be zero")
)

// RewardCalculator handles the logic of computing and distributing rewards.
type RewardCalculator struct {
	logger         hclog.Logger
	rewardPerBlock *big.Int // Block reward amount
}

// NewRewardCalculator creates a new RewardCalculator with the specified reward.
func NewRewardCalculator(logger hclog.Logger, reward *big.Int) (*RewardCalculator, error) {
	if logger == nil {
		return nil, errors.New("logger cannot be nil")
	}

	if reward == nil {
		return nil, ErrNilReward
	}

	if reward.Sign() < 0 {
		return nil, errors.New("reward amount cannot be negative")
	}

	return &RewardCalculator{
		logger:         logger,
		rewardPerBlock: new(big.Int).Set(reward), // Make a copy to avoid mutation
	}, nil
}

// DistributeBlockReward distributes the entire block reward to the block producer.
// Optionally, if a list of active validator addresses is provided, it splits a fraction
// among them for additional incentive.
func (rc *RewardCalculator) DistributeBlockReward(
	s state.State,
	blockProducer types.Address,
	validators []types.Address,
) error {
	if s == nil {
		return ErrNilState
	}

	if blockProducer == types.ZeroAddress {
		return ErrZeroAddress
	}

	// Credit block producer full reward if no validators are provided.
	if len(validators) == 0 {
		currentBalance := s.GetBalance(blockProducer)
		if currentBalance == nil {
			currentBalance = big.NewInt(0)
		}
		
		newBalance := new(big.Int).Add(currentBalance, rc.rewardPerBlock)
		if err := s.SetBalance(blockProducer, newBalance); err != nil {
			return err
		}
		
		rc.logger.Info("reward distributed to block producer", 
			"address", blockProducer.String(), 
			"reward", rc.rewardPerBlock.String(),
			"newBalance", newBalance.String())
		return nil
	}

	// Distribute reward: 80% to block producer, 20% equally among validators.
	producerShare := new(big.Int).Mul(rc.rewardPerBlock, big.NewInt(80))
	producerShare = producerShare.Div(producerShare, big.NewInt(100))
	
	validatorShareTotal := new(big.Int).Sub(rc.rewardPerBlock, producerShare)
	
	// Update block producer balance.
	currentProdBal := s.GetBalance(blockProducer)
	if currentProdBal == nil {
		currentProdBal = big.NewInt(0)
	}
	
	newProdBal := new(big.Int).Add(currentProdBal, producerShare)
	if err := s.SetBalance(blockProducer, newProdBal); err != nil {
		return err
	}

	// If there are validators to distribute to
	if len(validators) > 0 {
		// Calculate per-validator share
		validatorShare := new(big.Int).Div(validatorShareTotal, big.NewInt(int64(len(validators))))
		
		if validatorShare.Sign() > 0 {  // Only distribute if share is positive
			// Update balances for each validator.
			for _, addr := range validators {
				// Skip zero addresses
				if addr == types.ZeroAddress {
					rc.logger.Warn("skipping reward for zero address validator")
					continue
				}
				
				// Skip the block producer if they're also in the validator set
				// to avoid double payment
				if addr == blockProducer {
					rc.logger.Debug("skipping duplicate reward for block producer as validator", 
						"address", addr.String())
					continue
				}
				
				currentValBal := s.GetBalance(addr)
				if currentValBal == nil {
					currentValBal = big.NewInt(0)
				}
				
				newValBal := new(big.Int).Add(currentValBal, validatorShare)
				if err := s.SetBalance(addr, newValBal); err != nil {
					rc.logger.Error("failed to update validator reward", 
						"address", addr.String(),
						"error", err)
					return err
				}
				
				rc.logger.Debug("validator reward distributed", 
					"address", addr.String(), 
					"reward", validatorShare.String(),
					"newBalance", newValBal.String())
			}
		}
	}

	rc.logger.Info("block reward distributed", 
		"blockProducer", blockProducer.String(), 
		"producerReward", producerShare.String(), 
		"validatorShareTotal", validatorShareTotal.String(),
		"validatorCount", len(validators))
	
	return nil
}

// ApplySlashing deducts a percentage of the validator's stake as a penalty.
// slashPercentage must be between 0 and 100.
func (rc *RewardCalculator) ApplySlashing(
	s state.State,
	validator types.Address,
	slashPercentage uint64,
) error {
	if s == nil {
		return ErrNilState
	}

	if validator == types.ZeroAddress {
		return errors.New("validator address cannot be zero")
	}

	if slashPercentage > 100 {
		return ErrInvalidSlashPercentage
	}

	// If slash percentage is 0, nothing to do
	if slashPercentage == 0 {
		rc.logger.Debug("slash percentage is 0, no action taken",
			"validator", validator.String())
		return nil
	}

	currentStake := s.GetBalance(validator)
	if currentStake == nil || currentStake.Sign() <= 0 {
		return errors.New("validator has no stake to slash")
	}

	slashAmount := new(big.Int).Mul(currentStake, big.NewInt(int64(slashPercentage)))
	slashAmount = slashAmount.Div(slashAmount, big.NewInt(100))

	// Ensure we're not slashing more than available
	if slashAmount.Cmp(currentStake) > 0 {
		return ErrSlashingTooHigh
	}

	newStake := new(big.Int).Sub(currentStake, slashAmount)
	if err := s.SetBalance(validator, newStake); err != nil {
		rc.logger.Error("failed to update validator stake after slashing",
			"validator", validator.String(),
			"error", err)
		return err
	}

	rc.logger.Info("validator slashed", 
		"validator", validator.String(), 
		"slashAmount", slashAmount.String(),
		"slashPercentage", slashPercentage,
		"previousStake", currentStake.String(),
		"newStake", newStake.String())
	
	return nil
}

// GetRewardPerBlock returns the current reward amount per block
func (rc *RewardCalculator) GetRewardPerBlock() *big.Int {
	return new(big.Int).Set(rc.rewardPerBlock)
}

// UpdateRewardPerBlock updates the reward amount per block
func (rc *RewardCalculator) UpdateRewardPerBlock(newReward *big.Int) error {
	if newReward == nil {
		return ErrNilReward
	}
	
	if newReward.Sign() < 0 {
		return errors.New("reward amount cannot be negative")
	}
	
	rc.rewardPerBlock = new(big.Int).Set(newReward)
	rc.logger.Info("block reward updated", "newReward", newReward.String())
	
	return nil
}
