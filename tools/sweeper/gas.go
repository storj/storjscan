package sweeper

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// EstimateSweepGas estimates the total gas cost in wei for sweeping all tokens
// and ETH from a wallet. It includes a 30% buffer on top of the estimated cost.
func EstimateSweepGas(ctx context.Context, client BlockchainClient, from common.Address, destination common.Address, tokens []common.Address) (*big.Int, error) {
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas price: %w", err)
	}

	var totalGas uint64

	// Gas for each ERC20 transfer
	for _, token := range tokens {
		transferData := ERC20TransferData(destination, big.NewInt(1))
		gas, err := client.EstimateGas(ctx, ethereum.CallMsg{
			From: from,
			To:   &token,
			Data: transferData,
		})
		if err != nil {
			return nil, fmt.Errorf("estimate gas for token %s: %w", token.Hex(), err)
		}
		totalGas += gas
	}

	// Total cost = totalGas * gasPrice
	cost := new(big.Int).Mul(new(big.Int).SetUint64(totalGas), gasPrice)

	// Add 30% buffer: cost = cost * 130 / 100
	cost.Mul(cost, big.NewInt(130))
	cost.Div(cost, big.NewInt(100))

	return cost, nil
}

// FundWallet sends ETH from the gas source key to the target wallet and waits
// for the transaction to be mined.
func FundWallet(ctx context.Context, client BlockchainClient, gasSource *KeyPair, target common.Address, amount *big.Int) error {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, gasSource.Address)
	if err != nil {
		return fmt.Errorf("pending nonce: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("suggest gas price: %w", err)
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:  gasSource.Address,
		To:    &target,
		Value: amount,
	})
	if err != nil {
		return fmt.Errorf("estimate gas for funding: %w", err)
	}

	tx := types.NewTransaction(nonce, target, amount, gasLimit, gasPrice, nil)
	signer := types.NewEIP155Signer(chainID)
	signedTx, err := types.SignTx(tx, signer, gasSource.PrivateKey)
	if err != nil {
		return fmt.Errorf("sign transaction: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("send transaction: %w", err)
	}

	receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
	if err != nil {
		return fmt.Errorf("wait for mining: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("funding transaction failed with status %d", receipt.Status)
	}

	return nil
}
