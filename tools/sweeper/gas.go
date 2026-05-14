// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// suggestGasFees fetches the EIP-1559 gas fee cap and tip cap from the network.
// gasFeeCap is the value returned by SuggestGasPrice (which on EIP-1559 networks
// returns baseFee*2 + tip, a safe upper bound). gasTipCap is the priority fee.
func suggestGasFees(ctx context.Context, client BlockchainClient) (gasFeeCap, gasTipCap *big.Int, err error) {
	gasFeeCap, err = client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("suggest gas price: %w", err)
	}
	gasTipCap, err = client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("suggest gas tip cap: %w", err)
	}
	return gasFeeCap, gasTipCap, nil
}

// EstimateSweepGas estimates the total gas cost in wei for sweeping all tokens
// and ETH from a wallet. It includes a 30% buffer on top of the estimated cost.
func EstimateSweepGas(ctx context.Context, client BlockchainClient, from common.Address, destination common.Address, tokens []common.Address) (*big.Int, error) {
	gasFeeCap, _, err := suggestGasFees(ctx, client)
	if err != nil {
		return nil, err
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

	// Total cost = totalGas * gasFeeCap
	cost := new(big.Int).Mul(new(big.Int).SetUint64(totalGas), gasFeeCap)

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

	gasFeeCap, gasTipCap, err := suggestGasFees(ctx, client)
	if err != nil {
		return err
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:  gasSource.Address,
		To:    &target,
		Value: amount,
	})
	if err != nil {
		return fmt.Errorf("estimate gas for funding: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &target,
		Value:     amount,
		Gas:       gasLimit,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
	})
	signer := types.LatestSignerForChainID(chainID)
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
