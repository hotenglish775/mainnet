package validators

import (
	"errors"
	"math"
)

// Validator represents an individual validator in the network.
type Validator struct {
	Address string // Unique identifier (e.g., hex address)
	PubKey  string // Public key (BLS public key as per configuration)
	Stake   uint64 // Amount staked by the validator
	Active  bool   // Whether the validator is active in consensus
}

// ValidatorSet manages the collection of validators.
type ValidatorSet struct {
	validators map[string]*Validator // Map keyed by validator address
}

// NewValidatorSet creates a new ValidatorSet with an optional initial list of validators.
func NewValidatorSet(initialValidators []*Validator) *ValidatorSet {
	vs := &ValidatorSet{
		validators: make(map[string]*Validator),
	}
	for _, v := range initialValidators {
		vs.validators[v.Address] = v
	}
	return vs
}

// RegisterValidator adds a new validator to the set after performing necessary checks.
func (vs *ValidatorSet) RegisterValidator(v *Validator) error {
	if v == nil {
		return errors.New("validator is nil")
	}
	if v.Address == "" {
		return errors.New("validator address is empty")
	}
	if v.PubKey == "" {
		return errors.New("validator public key is empty")
	}
	if _, exists := vs.validators[v.Address]; exists {
		return errors.New("validator already exists")
	}
	v.Active = true
	vs.validators[v.Address] = v
	return nil
}

// RemoveValidator removes a validator by its address.
func (vs *ValidatorSet) RemoveValidator(address string) error {
	if address == "" {
		return errors.New("address is empty")
	}
	if _, exists := vs.validators[address]; !exists {
		return errors.New("validator not found")
	}
	delete(vs.validators, address)
	return nil
}

// GetActiveValidators returns all validators that are currently active.
func (vs *ValidatorSet) GetActiveValidators() []*Validator {
	active := make([]*Validator, 0)
	for _, v := range vs.validators {
		if v.Active {
			active = append(active, v)
		}
	}
	return active
}

// RequiredVotes computes the number of validator votes required for block finality.
// It typically returns 2/3 of the active validators plus one.
func (vs *ValidatorSet) RequiredVotes() int {
	active := vs.GetActiveValidators()
	total := len(active)
	if total == 0 {
		return 0
	}
	required := int(math.Floor(float64(total)*2/3)) + 1
	if required > total {
		required = total
	}
	return required
}

// UpdateValidatorStake updates the stake for the given validator address.
func (vs *ValidatorSet) UpdateValidatorStake(address string, newStake uint64) error {
	if v, exists := vs.validators[address]; exists {
		v.Stake = newStake
		return nil
	}
	return errors.New("validator not found")
}

// DeactivateValidator marks the validator as inactive without removing it.
func (vs *ValidatorSet) DeactivateValidator(address string) error {
	if v, exists := vs.validators[address]; exists {
		v.Active = false
		return nil
	}
	return errors.New("validator not found")
}

// ActivateValidator marks the validator as active.
func (vs *ValidatorSet) ActivateValidator(address string) error {
	if v, exists := vs.validators[address]; exists {
		v.Active = true
		return nil
	}
	return errors.New("validator not found")
}

// ListAllValidators returns all validators, active or not.
func (vs *ValidatorSet) ListAllValidators() []*Validator {
	all := make([]*Validator, 0)
	for _, v := range vs.validators {
		all = append(all, v)
	}
	return all
}
