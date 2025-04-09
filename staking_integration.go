package integration

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/0xPolygon/polygon-edge/types"
	"github.com/hotenglish775/mainnet/contracts" // Package containing generated bindings for ACCToken and SecureDeployment
)

// StakingIntegration wires together the ACCToken and SecureDeployment contracts.
type StakingIntegration struct {
	ACCToken         *contracts.ACCToken         // Instance of the ACCToken contract binding.
	SecureDeployment *contracts.SecureDeployment // Instance of the SecureDeployment contract binding.
	auth             *bind.TransactOpts          // Transaction options including signer.
	client           *ethclient.Client           // Ethereum client to interact with the network.
}

// NewStakingIntegration creates a new StakingIntegration instance.
// acctokenAddr and secureDeploymentAddr are the deployed contract addresses.
func NewStakingIntegration(client *ethclient.Client, acctokenAddr types.Address, secureDeploymentAddr types.Address, auth *bind.TransactOpts) (*StakingIntegration, error) {
	token, err := contracts.NewACCToken(acctokenAddr.Hex(), client)
	if err != nil {
		return nil, fmt.Errorf("failed to load ACCToken contract: %w", err)
	}
	secure, err := contracts.NewSecureDeployment(secureDeploymentAddr.Hex(), client)
	if err != nil {
		return nil, fmt.Errorf("failed to load SecureDeployment contract: %w", err)
	}
	return &StakingIntegration{
		ACCToken:         token,
		SecureDeployment: secure,
		auth:             auth,
		client:           client,
	}, nil
}

// TransferTokens transfers a specified amount of ACC-20 tokens to the recipient address.
func (si *StakingIntegration) TransferTokens(to types.Address, amount *big.Int) error {
	tx, err := si.ACCToken.Transfer(si.auth, to.Hex(), amount)
	if err != nil {
		return fmt.Errorf("token transfer failed: %w", err)
	}
	log.Printf("Token transfer transaction sent: %s", tx.Hash().Hex())
	return nil
}

// DeployNewContract deploys a new contract using the SecureDeployment contract.
// The provided bytecode must not match any banned code hash.
func (si *StakingIntegration) DeployNewContract(bytecode []byte) (types.Address, error) {
	tx, err := si.SecureDeployment.DeployContract(si.auth, bytecode)
	if err != nil {
		return types.ZeroAddress, fmt.Errorf("contract deployment failed: %w", err)
	}
	receipt, err := bind.WaitMined(context.Background(), si.client, tx)
	if err != nil {
		return types.ZeroAddress, fmt.Errorf("waiting for mining failed: %w", err)
	}
	if receipt.ContractAddress == "" {
		return types.ZeroAddress, fmt.Errorf("deployment receipt missing contract address")
	}
	log.Printf("Contract deployed at address: %s", receipt.ContractAddress)
	return types.HexToAddress(receipt.ContractAddress), nil
}
