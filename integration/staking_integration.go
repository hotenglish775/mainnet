package integration

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/0xPolygon/polygon-edge/txrelayer"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hotenglish775/mainnet/contracts" // Package containing generated bindings for ACCToken and SecureDeployment
)

// StakingIntegration wires together the ACCToken and SecureDeployment contracts.
type StakingIntegration struct {
	ACCToken         *contracts.ACCToken         // Instance of the ACCToken contract binding.
	SecureDeployment *contracts.SecureDeployment // Instance of the SecureDeployment contract binding.
	txRelayer        txrelayer.TxRelayer         // Transaction relayer to interact with the network.
	sender           types.Address               // The address sending transactions.
}

// NewStakingIntegration creates a new StakingIntegration instance.
// acctokenAddr and secureDeploymentAddr are the deployed contract addresses.
func NewStakingIntegration(txRelayer txrelayer.TxRelayer, sender types.Address, acctokenAddr types.Address, secureDeploymentAddr types.Address) (*StakingIntegration, error) {
	token, err := contracts.NewACCToken(acctokenAddr, txRelayer)
	if err != nil {
		return nil, fmt.Errorf("failed to load ACCToken contract: %w", err)
	}
	
	secure, err := contracts.NewSecureDeployment(secureDeploymentAddr, txRelayer)
	if err != nil {
		return nil, fmt.Errorf("failed to load SecureDeployment contract: %w", err)
	}
	
	return &StakingIntegration{
		ACCToken:         token,
		SecureDeployment: secure,
		txRelayer:        txRelayer,
		sender:           sender,
	}, nil
}

// TransferTokens transfers a specified amount of ACC-20 tokens to the recipient address.
func (si *StakingIntegration) TransferTokens(to types.Address, amount *big.Int) error {
	txn, err := si.ACCToken.Transfer(si.sender, to, amount)
	if err != nil {
		return fmt.Errorf("token transfer failed: %w", err)
	}
	
	receipt, err := si.txRelayer.SendTransaction(txn, nil)
	if err != nil {
		return fmt.Errorf("sending transfer transaction failed: %w", err)
	}
	
	log.Printf("Token transfer transaction sent: %s", receipt.TxHash.String())
	return nil
}

// DeployNewContract deploys a new contract using the SecureDeployment contract.
// The provided bytecode must not match any banned code hash.
func (si *StakingIntegration) DeployNewContract(ctx context.Context, bytecode []byte) (types.Address, error) {
	txn, err := si.SecureDeployment.DeployContract(si.sender, bytecode)
	if err != nil {
		return types.ZeroAddress, fmt.Errorf("contract deployment failed: %w", err)
	}
	
	receipt, err := si.txRelayer.SendTransaction(txn, nil)
	if err != nil {
		return types.ZeroAddress, fmt.Errorf("sending deployment transaction failed: %w", err)
	}
	
	if receipt.ContractAddress == types.ZeroAddress {
		return types.ZeroAddress, fmt.Errorf("deployment receipt missing contract address")
	}
	
	log.Printf("Contract deployed at address: %s", receipt.ContractAddress.String())
	return receipt.ContractAddress, nil
}
