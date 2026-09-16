// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// directStrategy sweeps a wallet with transactions signed and paid for by the
// wallet itself: one ERC20 transfer per token, then whatever ETH is left after
// the fees. A wallet holding tokens but not enough ETH to move them is topped
// up from the gas source first, since tokens cannot pay for their own gas.
type directStrategy struct {
	sweepConfig

	gasSource *KeyPair
	skipETH   bool
}

func newDirectStrategy(cfg sweepConfig, gasSource *KeyPair, skipETH bool) *directStrategy {
	return &directStrategy{sweepConfig: cfg, gasSource: gasSource, skipETH: skipETH}
}

// prepare has nothing to check: every wallet pays for itself, and the gas
// source is only consulted when a wallet turns out to need it.
func (d *directStrategy) prepare(ctx context.Context) error {
	return nil
}

// finalize reports what the destination ended up with.
func (d *directStrategy) finalize(ctx context.Context) error {
	return d.reportDestinationBalance(ctx)
}

// planWallet works out what this wallet would cost to sweep: the fees come out
// of the wallet itself, so the operator's questions are how much ETH survives
// them and how much the gas source would have to put in first.
func (d *directStrategy) planWallet(ctx context.Context, kp KeyPair, balance WalletBalances) ([]any, error) {
	// Only tokens the wallet actually holds are transferred, so only those cost
	// gas.
	var nonZeroTokens []common.Address
	for i, tb := range balance.Tokens {
		if i < len(d.tokens) && tb != nil && tb.Sign() > 0 {
			nonZeroTokens = append(nonZeroTokens, d.tokens[i])
		}
	}

	tokenGasCost := new(big.Int)
	if len(nonZeroTokens) > 0 {
		cost, err := EstimateSweepGas(ctx, d.client, kp.Address, d.destination, nonZeroTokens)
		if err != nil {
			return nil, fmt.Errorf("estimate gas: %w", err)
		}
		tokenGasCost = cost
	}

	// What is left of the ETH balance once the transfers are paid for.
	recoverableETH := new(big.Int)
	ethTransferGas := new(big.Int)
	if !d.skipETH && balance.ETH != nil && balance.ETH.Sign() > 0 {
		gasFeeCap, _, err := suggestGasFees(ctx, d.client)
		if err != nil {
			return nil, err
		}
		// ETH transfer costs 21000 gas as a conservative estimate.
		ethTransferGas.Mul(gasFeeCap, big.NewInt(21000))

		remaining := new(big.Int).Set(balance.ETH)
		if len(nonZeroTokens) > 0 && d.gasSource != nil && remaining.Cmp(tokenGasCost) < 0 {
			// Gas source would fund the deficit, so full ETH balance remains.
		} else if len(nonZeroTokens) > 0 {
			remaining.Sub(remaining, tokenGasCost)
		}
		if remaining.Cmp(ethTransferGas) > 0 {
			recoverableETH.Sub(remaining, ethTransferGas)
		}
	}

	// Gas the gas source would have to send before the tokens can move.
	fundingNeeded := new(big.Int)
	if d.gasSource != nil && len(nonZeroTokens) > 0 && balance.ETH.Cmp(tokenGasCost) < 0 {
		fundingNeeded.Sub(tokenGasCost, balance.ETH)
	}

	plan := []any{
		"recoverableETH", recoverableETH.String(),
		"gasCost", new(big.Int).Add(tokenGasCost, ethTransferGas).String(),
	}
	if fundingNeeded.Sign() > 0 {
		plan = append(plan, "gasFundingNeeded", fundingNeeded.String())
	}
	return plan, nil
}

// sweepWallet moves the wallet's funds with transactions it signs itself. The
// balance from the pre-run query is only what the sweep is expected to find:
// every amount that goes into a transfer is read again here, because it has to
// match what the wallet holds at that moment, and a deposit may have landed
// since.
func (d *directStrategy) sweepWallet(ctx context.Context, kp KeyPair, balance WalletBalances) error {
	client := d.client
	network := d.network()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.rateDelay):
	}

	d.logger.Info("checking wallet", "network", network, "address", kp.Address.Hex(), "expectedETH", balance.ETH.String())

	// Check ETH balance
	ethBalance, err := client.BalanceAt(ctx, kp.Address, nil)
	if err != nil {
		return fmt.Errorf("balance check: %w", err)
	}

	// Check ERC20 balances
	type tokenBalance struct {
		token   common.Address
		balance *big.Int
	}
	var nonZeroTokens []tokenBalance
	for _, token := range d.tokens {
		balance, err := ERC20BalanceOf(ctx, client, token, kp.Address)
		if err != nil {
			return fmt.Errorf("erc20 balance %s: %w", token.Hex(), err)
		}
		if balance.Sign() > 0 {
			nonZeroTokens = append(nonZeroTokens, tokenBalance{token: token, balance: balance})
		}
	}

	// Skip if all balances are zero
	if ethBalance.Sign() == 0 && len(nonZeroTokens) == 0 {
		d.logger.Info("skipping wallet, all balances zero", "network", network, "address", kp.Address.Hex())
		return nil
	}

	// Calculate gas needed for all non-zero transfers
	var tokensToEstimate []common.Address
	for _, tb := range nonZeroTokens {
		tokensToEstimate = append(tokensToEstimate, tb.token)
	}
	gasCost, err := EstimateSweepGas(ctx, client, kp.Address, d.destination, tokensToEstimate)
	if err != nil {
		return fmt.Errorf("estimate gas: %w", err)
	}

	// Fund wallet from gas source if needed.
	// Only fund when there are ERC20 tokens to sweep — those can't pay for
	// their own gas. For ETH-only sweeps, the gas cost is simply deducted
	// from the swept amount; funding externally would spend more than is
	// recovered.
	if d.gasSource != nil && len(nonZeroTokens) > 0 && ethBalance.Cmp(gasCost) < 0 {
		deficit := new(big.Int).Sub(gasCost, ethBalance)
		d.logger.Info("funding wallet for gas", "network", network, "address", kp.Address.Hex(), "amount", deficit.String())
		if err := FundWallet(ctx, client, d.gasSource, kp.Address, deficit); err != nil {
			return fmt.Errorf("fund wallet: %w", err)
		}
		// Re-check ETH balance after funding
		ethBalance, err = client.BalanceAt(ctx, kp.Address, nil)
		if err != nil {
			return fmt.Errorf("re-check balance: %w", err)
		}
	}

	// Sweep each ERC20 token
	for _, tb := range nonZeroTokens {
		d.logger.Info("sweeping ERC20", "network", network, "address", kp.Address.Hex(), "token", tb.token.Hex(), "amount", tb.balance.String(), "destination", d.destination.Hex())

		if err := d.sendERC20Transfer(ctx, kp, tb.token, tb.balance); err != nil {
			return fmt.Errorf("sweep token %s: %w", tb.token.Hex(), err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.rateDelay):
		}
	}

	if d.skipETH {
		d.logger.Info("skipping ETH sweep", "network", network, "address", kp.Address.Hex())
		return nil
	}

	// Sweep remaining ETH
	// Re-check balance since gas was spent on token transfers
	ethBalance, err = client.BalanceAt(ctx, kp.Address, nil)
	if err != nil {
		return fmt.Errorf("final balance check: %w", err)
	}

	gasFeeCap, gasTipCap, err := suggestGasFees(ctx, client)
	if err != nil {
		return fmt.Errorf("gas fees for ETH sweep: %w", err)
	}
	dest := d.destination
	ethGasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:  kp.Address,
		To:    &dest,
		Value: ethBalance,
	})
	if err != nil {
		return fmt.Errorf("estimate gas for ETH sweep: %w", err)
	}
	ethTransferCost := new(big.Int).Mul(gasFeeCap, new(big.Int).SetUint64(ethGasLimit))

	if ethBalance.Cmp(ethTransferCost) > 0 {
		sweepAmount := new(big.Int).Sub(ethBalance, ethTransferCost)
		d.logger.Info("sweeping ETH", "network", network, "address", kp.Address.Hex(), "amount", sweepAmount.String(), "destination", d.destination.Hex())

		if err := d.sendETHTransfer(ctx, kp, sweepAmount, ethGasLimit, gasFeeCap, gasTipCap); err != nil {
			return fmt.Errorf("sweep ETH: %w", err)
		}
	} else {
		d.logger.Info("skipping ETH sweep, balance below gas cost", "network", network, "address", kp.Address.Hex(), "balance", ethBalance.String(), "gasCost", ethTransferCost.String())
	}

	return nil
}

func (d *directStrategy) sendERC20Transfer(ctx context.Context, kp KeyPair, token common.Address, amount *big.Int) error {
	client := d.client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, kp.Address)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	gasFeeCap, gasTipCap, err := suggestGasFees(ctx, client)
	if err != nil {
		return err
	}

	data := ERC20TransferData(d.destination, amount)
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: kp.Address,
		To:   &token,
		Data: data,
	})
	if err != nil {
		return fmt.Errorf("estimate gas: %w", err)
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &token,
		Gas:       gasLimit,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
		Data:      data,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, kp.PrivateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
	if err != nil {
		return fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("transaction failed with status %d", receipt.Status)
	}

	return nil
}

// sendETHTransfer sends an ETH transfer using the gasLimit, gasFeeCap, and
// gasTipCap already computed by the caller, ensuring the sweep amount and
// transaction fee are consistent (no re-estimation that could cause
// "insufficient funds" if the base fee ticks up between the two calls).
func (d *directStrategy) sendETHTransfer(ctx context.Context, kp KeyPair, amount *big.Int, gasLimit uint64, gasFeeCap, gasTipCap *big.Int) error {
	client := d.client

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, kp.Address)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	dest := d.destination
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &dest,
		Value:     amount,
		Gas:       gasLimit,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, kp.PrivateKey)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
	if err != nil {
		return fmt.Errorf("wait mined: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("transaction failed with status %d", receipt.Status)
	}

	return nil
}
