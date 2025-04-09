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
	ErrInsufficientFunds = errors.New("insufficient funds for reward distribution")
)

// RewardCalculator handles the logic of computing and distributing rewards.
type RewardCalculator struct {
	logger         hclog.Logger
	rewardPerBlock *big.Int // Block reward amount
}

// NewRewardCalculator creates a new RewardCalculator with the specified reward.
func NewRewardCalculator(logger hclog.Logger, reward *big.Int) *RewardCalculator {
	return &RewardCalculator{
		logger:         logger,
		rewardPerBlock: reward,
	}
}

// DistributeBlockReward distributes the entire block reward to the block producer.
// Optionally, if a list of active validator addresses is provided, it splits a fraction
// among them for additional incentive.
func (rc *RewardCalculator) DistributeBlockReward(
	s state.State,
	blockProducer types.Address,
	validators []types.Address,
) error {
	// Credit block producer full reward if no validators are provided.
	if len(validators) == 0 {
		currentBalance := s.GetBalance(blockProducer)
		newBalance := new(big.Int).Add(currentBalance, rc.rewardPerBlock)
		if err := s.SetBalance(blockProducer, newBalance); err != nil {
			return err
		}
		rc.logger.Info("reward distributed to block producer", "address", blockProducer.String(), "reward", rc.rewardPerBlock.String())
		return nil
	}

	// Distribute reward: 80% to block producer, 20% equally among validators.
	producerShare := new(big.Int).Div(new(big.Int).Mul(rc.rewardPerBlock, big.NewInt(80)), big.NewInt(100))
	validatorShareTotal := new(big.Int).Sub(rc.rewardPerBlock, producerShare)
	validatorShare := new(big.Int)
	if len(validators) > 0 {
		validatorShare.Div(validatorShareTotal, big.NewInt(int64(len(validators))))
	}

	// Update block producer balance.
	currentProdBal := s.GetBalance(blockProducer)
	newProdBal := new(big.Int).Add(currentProdBal, producerShare)
	if err := s.SetBalance(blockProducer, newProdBal); err != nil {
		return err
	}

	// Update balances for each validator.
	for _, addr := range validators {
		currentValBal := s.GetBalance(addr)
		newValBal := new(big.Int).Add(currentValBal, validatorShare)
		if err := s.SetBalance(addr, newValBal); err != nil {
			rc.logger.Error("failed to update validator reward", "address", addr.String())
			return err
		}
	}

	rc.logger.Info("block reward distributed", "blockProducer", blockProducer.String(), "producerReward", producerShare.String(), "validatorRewardPerValidator", validatorShare.String())
	return nil
}

// ApplySlashing deducts a percentage of the validator's stake as a penalty.
// slashPercentage must be between 0 and 100.
func (rc *RewardCalculator) ApplySlashing(
	s state.State,
	validator types.Address,
	slashPercentage uint64,
) error {
	if slashPercentage > 100 {
		return errors.New("invalid slash percentage, must be 0-100")
	}
	currentStake := s.GetBalance(validator)
	slashAmount := new(big.Int).Div(new(big.Int).Mul(currentStake, big.NewInt(int64(slashPercentage))), big.NewInt(100))
	if slashAmount.Cmp(currentStake) >= 0 {
		return errors.New("cannot slash all stake")
	}
	newStake := new(big.Int).Sub(currentStake, slashAmount)
	if err := s.SetBalance(validator, newStake); err != nil {
		return err
	}
	rc.logger.Info("validator slashed", "validator", validator.String(), "slashAmount", slashAmount.String())
	return nil
}
