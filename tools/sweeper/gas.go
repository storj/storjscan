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

// baseFeeWiggleMultiplier is how many times the current base fee the gas fee cap
// covers, matching go-ethereum's bind package. The base fee can grow by 12.5%
// per block, so a 2x cap keeps a transaction minable across roughly six
// consecutive full blocks instead of going stale after one.
const baseFeeWiggleMultiplier = 2

// suggestGasFees fetches the EIP-1559 gas fee cap and tip cap from the network.
//
// gasTipCap is the priority fee suggested by the node. gasFeeCap is
// gasTipCap + baseFee*baseFeeWiggleMultiplier, the same formula go-ethereum's
// bind package uses. The base fee is derived from the two suggestions
// (eth_gasPrice returns baseFee + tip on EIP-1559 networks), so no extra RPC
// call is needed.
//
// Note that SuggestGasPrice alone is NOT a safe fee cap: it embeds only a
// single base fee, so the transaction becomes unminable as soon as the base fee
// ticks up and then sits in the mempool until the receipt wait times out.
func suggestGasFees(ctx context.Context, client BlockchainClient) (gasFeeCap, gasTipCap *big.Int, err error) {
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("suggest gas price: %w", err)
	}
	gasTipCap, err = client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("suggest gas tip cap: %w", err)
	}

	baseFee := new(big.Int).Sub(gasPrice, gasTipCap)
	if baseFee.Sign() < 0 {
		// Not an EIP-1559 gas price (or the two suggestions raced across a
		// block boundary); there is no base fee to add headroom for.
		baseFee.SetInt64(0)
	}
	baseFee.Mul(baseFee, big.NewInt(baseFeeWiggleMultiplier))
	gasFeeCap = new(big.Int).Add(gasTipCap, baseFee)

	// Never bid below what the node itself suggested.
	if gasFeeCap.Cmp(gasPrice) < 0 {
		gasFeeCap.Set(gasPrice)
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
