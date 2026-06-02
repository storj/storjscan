// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// SweepFailure records a failed sweep attempt.
type SweepFailure struct {
	Network string
	Address common.Address
	LineNum int
	Err     error
}

// Sweeper orchestrates sweeping ETH and ERC20 tokens from deposit wallets
// into a single destination address.
type Sweeper struct {
	ethClient   BlockchainClient
	zkClient    BlockchainClient
	destination common.Address
	ethTokens   []common.Address
	zkTokens    []common.Address
	gasSource   *KeyPair
	rateDelay   time.Duration
	maxFailures int
	skipETH     bool
	dryRun      bool
	logger      *slog.Logger
}

// NewSweeper creates a new Sweeper. Set maxFailures to 0 for unlimited.
func NewSweeper(
	ethClient BlockchainClient,
	zkClient BlockchainClient,
	destination common.Address,
	ethTokens []common.Address,
	zkTokens []common.Address,
	gasSource *KeyPair,
	rateDelay time.Duration,
	maxFailures int,
	skipETH bool,
	dryRun bool,
	logger *slog.Logger,
) *Sweeper {
	return &Sweeper{
		ethClient:   ethClient,
		zkClient:    zkClient,
		destination: destination,
		ethTokens:   ethTokens,
		zkTokens:    zkTokens,
		gasSource:   gasSource,
		rateDelay:   rateDelay,
		maxFailures: maxFailures,
		skipETH:     skipETH,
		dryRun:      dryRun,
		logger:      logger,
	}
}

// SweepAll sweeps all keys on both Ethereum and zkSync networks.
// Individual wallet failures are logged and collected rather than stopping the
// sweep. If maxFailures > 0 and that many wallets fail, the sweep is aborted
// early (circuit breaker). All failures are logged as a summary at the end.
func (s *Sweeper) SweepAll(ctx context.Context, keys []KeyPair) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}

	addrs := make([]common.Address, len(keys))
	for i, kp := range keys {
		addrs[i] = kp.Address
	}

	ethBalances, err := s.queryBalances(ctx, s.ethClient, addrs, s.ethTokens, "ethereum")
	if err != nil {
		return err
	}

	zkBalances, err := s.queryBalances(ctx, s.zkClient, addrs, s.zkTokens, "zksync")
	if err != nil {
		return err
	}

	if s.dryRun {
		return s.dryRunReport(ctx, keys, ethBalances, zkBalances)
	}

	var failures []SweepFailure
	for i, kp := range keys {
		if ethBalances[i].HasFunds() {
			if err := s.sweepKey(ctx, kp, s.ethClient, s.ethTokens, "ethereum"); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.logger.Error("sweep failed, continuing", "network", "ethereum", "address", kp.Address.Hex(), "keyLine", kp.LineNum, "error", err)
				failures = append(failures, SweepFailure{Network: "ethereum", Address: kp.Address, LineNum: kp.LineNum, Err: err})
				if s.maxFailures > 0 && len(failures) >= s.maxFailures {
					s.logger.Error("circuit breaker tripped, aborting sweep", "failures", len(failures), "max", s.maxFailures)
					break
				}
			}
		}
		if zkBalances[i].HasFunds() {
			if err := s.sweepKey(ctx, kp, s.zkClient, s.zkTokens, "zksync"); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.logger.Error("sweep failed, continuing", "network", "zksync", "address", kp.Address.Hex(), "keyLine", kp.LineNum, "error", err)
				failures = append(failures, SweepFailure{Network: "zksync", Address: kp.Address, LineNum: kp.LineNum, Err: err})
				if s.maxFailures > 0 && len(failures) >= s.maxFailures {
					s.logger.Error("circuit breaker tripped, aborting sweep", "failures", len(failures), "max", s.maxFailures)
					break
				}
			}
		}
	}
	if len(failures) > 0 {
		s.logger.Error("sweep completed with failures", "total", len(failures))
		for _, f := range failures {
			s.logger.Error("failed address", "network", f.Network, "address", f.Address.Hex(), "keyLine", f.LineNum, "error", f.Err)
		}
		return fmt.Errorf("%d address(es) failed to sweep", len(failures))
	}
	return nil
}

// dryRunReport prints a summary of what would be swept without sending any transactions.
// For each wallet with funds it estimates gas costs and logs recoverable balances.
func (s *Sweeper) dryRunReport(ctx context.Context, keys []KeyPair, ethBalances, zkBalances []WalletBalances) error {
	type networkConfig struct {
		name     string
		client   BlockchainClient
		tokens   []common.Address
		balances []WalletBalances
	}
	networks := []networkConfig{
		{"ethereum", s.ethClient, s.ethTokens, ethBalances},
		{"zksync", s.zkClient, s.zkTokens, zkBalances},
	}

	for _, net := range networks {
		totalRecoverableETH := new(big.Int)
		totalGasCost := new(big.Int)
		tokenTotals := make([]*big.Int, len(net.tokens))
		for i := range tokenTotals {
			tokenTotals[i] = new(big.Int)
		}
		walletsWithFunds := 0

		for i, kp := range keys {
			bal := net.balances[i]
			if !bal.HasFunds() {
				continue
			}
			walletsWithFunds++

			// Collect non-zero tokens for this wallet.
			var nonZeroTokens []common.Address
			for ti, tb := range bal.Tokens {
				if tb.Sign() > 0 {
					nonZeroTokens = append(nonZeroTokens, net.tokens[ti])
					tokenTotals[ti].Add(tokenTotals[ti], tb)
				}
			}

			// Estimate gas cost for ERC20 transfers.
			var walletGasCost *big.Int
			if len(nonZeroTokens) > 0 {
				cost, err := EstimateSweepGas(ctx, net.client, kp.Address, s.destination, nonZeroTokens)
				if err != nil {
					s.logger.Warn("dry-run: failed to estimate gas", "network", net.name, "address", kp.Address.Hex(), "error", err)
					continue
				}
				walletGasCost = cost
			} else {
				walletGasCost = new(big.Int)
			}

			// Estimate ETH recovery.
			recoverableETH := new(big.Int)
			ethTransferGas := new(big.Int)
			if !s.skipETH && bal.ETH.Sign() > 0 {
				gasFeeCap, _, err := suggestGasFees(ctx, net.client)
				if err != nil {
					s.logger.Warn("dry-run: failed to get gas fees", "network", net.name, "address", kp.Address.Hex(), "error", err)
					continue
				}
				// ETH transfer costs 21000 gas as a conservative estimate.
				ethTransferGas.Mul(gasFeeCap, big.NewInt(21000))

				// The ETH available after covering ERC20 gas and the ETH transfer itself.
				remaining := new(big.Int).Set(bal.ETH)
				if len(nonZeroTokens) > 0 && s.gasSource != nil && remaining.Cmp(walletGasCost) < 0 {
					// Gas source would fund the deficit, so full ETH balance remains.
				} else if len(nonZeroTokens) > 0 {
					remaining.Sub(remaining, walletGasCost)
				}
				if remaining.Cmp(ethTransferGas) > 0 {
					recoverableETH.Sub(remaining, ethTransferGas)
				}
			}

			// Gas needed from gas source for this wallet.
			fundingNeeded := new(big.Int)
			if s.gasSource != nil && len(nonZeroTokens) > 0 && bal.ETH.Cmp(walletGasCost) < 0 {
				fundingNeeded.Sub(walletGasCost, bal.ETH)
			}

			totalGasCostForWallet := new(big.Int).Add(walletGasCost, ethTransferGas)
			totalGasCost.Add(totalGasCost, totalGasCostForWallet)
			totalRecoverableETH.Add(totalRecoverableETH, recoverableETH)

			attrs := []any{
				"network", net.name,
				"address", kp.Address.Hex(),
				"ethBalance", bal.ETH.String(),
				"recoverableETH", recoverableETH.String(),
				"gasCost", totalGasCostForWallet.String(),
			}
			if fundingNeeded.Sign() > 0 {
				attrs = append(attrs, "gasFundingNeeded", fundingNeeded.String())
			}
			for ti, tb := range bal.Tokens {
				if tb.Sign() > 0 {
					attrs = append(attrs, fmt.Sprintf("token_%s", net.tokens[ti].Hex()), tb.String())
				}
			}
			s.logger.Info("dry-run: wallet", attrs...)
		}

		if walletsWithFunds == 0 {
			s.logger.Info("dry-run: no wallets with funds", "network", net.name)
			continue
		}

		summaryAttrs := []any{
			"network", net.name,
			"walletsWithFunds", walletsWithFunds,
			"totalRecoverableETH", totalRecoverableETH.String(),
			"totalEstimatedGasCost", totalGasCost.String(),
		}
		for ti, token := range net.tokens {
			summaryAttrs = append(summaryAttrs, fmt.Sprintf("totalToken_%s", token.Hex()), tokenTotals[ti].String())
		}
		s.logger.Info("dry-run: summary", summaryAttrs...)
	}

	return nil
}

func (s *Sweeper) sweepKey(ctx context.Context, kp KeyPair, client BlockchainClient, tokens []common.Address, network string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.rateDelay):
	}

	s.logger.Info("checking wallet", "network", network, "address", kp.Address.Hex())

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
	for _, token := range tokens {
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
		s.logger.Info("skipping wallet, all balances zero", "network", network, "address", kp.Address.Hex())
		return nil
	}

	// Calculate gas needed for all non-zero transfers
	var tokensToEstimate []common.Address
	for _, tb := range nonZeroTokens {
		tokensToEstimate = append(tokensToEstimate, tb.token)
	}
	gasCost, err := EstimateSweepGas(ctx, client, kp.Address, s.destination, tokensToEstimate)
	if err != nil {
		return fmt.Errorf("estimate gas: %w", err)
	}

	// Fund wallet from gas source if needed.
	// Only fund when there are ERC20 tokens to sweep — those can't pay for
	// their own gas. For ETH-only sweeps, the gas cost is simply deducted
	// from the swept amount; funding externally would spend more than is
	// recovered.
	if s.gasSource != nil && len(nonZeroTokens) > 0 && ethBalance.Cmp(gasCost) < 0 {
		deficit := new(big.Int).Sub(gasCost, ethBalance)
		s.logger.Info("funding wallet for gas", "network", network, "address", kp.Address.Hex(), "amount", deficit.String())
		if err := FundWallet(ctx, client, s.gasSource, kp.Address, deficit); err != nil {
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
		s.logger.Info("sweeping ERC20", "network", network, "address", kp.Address.Hex(), "token", tb.token.Hex(), "amount", tb.balance.String(), "destination", s.destination.Hex())

		if err := s.sendERC20Transfer(ctx, client, kp, tb.token, tb.balance); err != nil {
			return fmt.Errorf("sweep token %s: %w", tb.token.Hex(), err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.rateDelay):
		}
	}

	if s.skipETH {
		s.logger.Info("skipping ETH sweep", "network", network, "address", kp.Address.Hex())
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
	dest := s.destination
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
		s.logger.Info("sweeping ETH", "network", network, "address", kp.Address.Hex(), "amount", sweepAmount.String(), "destination", s.destination.Hex())

		if err := s.sendETHTransfer(ctx, client, kp, sweepAmount, ethGasLimit, gasFeeCap, gasTipCap); err != nil {
			return fmt.Errorf("sweep ETH: %w", err)
		}
	} else {
		s.logger.Info("skipping ETH sweep, balance below gas cost", "network", network, "address", kp.Address.Hex(), "balance", ethBalance.String(), "gasCost", ethTransferCost.String())
	}

	return nil
}

func (s *Sweeper) sendERC20Transfer(ctx context.Context, client BlockchainClient, kp KeyPair, token common.Address, amount *big.Int) error {
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

	data := ERC20TransferData(s.destination, amount)
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
func (s *Sweeper) sendETHTransfer(ctx context.Context, client BlockchainClient, kp KeyPair, amount *big.Int, gasLimit uint64, gasFeeCap, gasTipCap *big.Int) error {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, kp.Address)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	dest := s.destination
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

func (s *Sweeper) queryBalances(ctx context.Context, client BlockchainClient, addrs []common.Address, tokens []common.Address, network string) ([]WalletBalances, error) {
	var minETH *big.Int
	if !s.skipETH {
		gasFeeCap, _, err := suggestGasFees(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("%s gas fees: %w", network, err)
		}
		minETH = new(big.Int).Mul(gasFeeCap, big.NewInt(21000))
	}

	s.logger.Info("querying balances via multicall", "network", network, "wallets", len(addrs))
	balances, err := MulticallBalances(ctx, s.logger, client, addrs, tokens, minETH, s.skipETH)
	if err != nil {
		return nil, fmt.Errorf("multicall %s balances: %w", network, err)
	}
	logBalanceSummary(s.logger, network, balances, tokens)
	return balances, nil
}

func logBalanceSummary(logger *slog.Logger, network string, balances []WalletBalances, tokens []common.Address) {
	ethCount := 0
	ethTotal := new(big.Int)
	tokenCounts := make([]int, len(tokens))
	tokenTotals := make([]*big.Int, len(tokens))
	for i := range tokens {
		tokenTotals[i] = new(big.Int)
	}
	for _, wb := range balances {
		if wb.ETH.Sign() > 0 {
			ethCount++
			ethTotal.Add(ethTotal, wb.ETH)
		}
		for i, t := range wb.Tokens {
			if t.Sign() > 0 {
				tokenCounts[i]++
				tokenTotals[i].Add(tokenTotals[i], t)
			}
		}
	}
	logger.Info("balance check complete", "network", network, "ETH_wallets", ethCount, "ETH_total", ethTotal.String())
	for i, token := range tokens {
		logger.Info("token balance summary", "network", network, "token", token.Hex(), "wallets", tokenCounts[i], "total", tokenTotals[i].String())
	}
}
