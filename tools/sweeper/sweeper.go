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
	logger      *slog.Logger
}

// NewSweeper creates a new Sweeper.
func NewSweeper(
	ethClient BlockchainClient,
	zkClient BlockchainClient,
	destination common.Address,
	ethTokens []common.Address,
	zkTokens []common.Address,
	gasSource *KeyPair,
	rateDelay time.Duration,
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
		logger:      logger,
	}
}

// SweepAll sweeps all keys on both Ethereum and zkSync networks.
func (s *Sweeper) SweepAll(ctx context.Context, keys []KeyPair) error {
	for _, kp := range keys {
		if err := s.sweepKey(ctx, kp, s.ethClient, s.ethTokens, "ethereum"); err != nil {
			return fmt.Errorf("sweep ethereum %s: %w", kp.Address.Hex(), err)
		}
		if err := s.sweepKey(ctx, kp, s.zkClient, s.zkTokens, "zksync"); err != nil {
			return fmt.Errorf("sweep zksync %s: %w", kp.Address.Hex(), err)
		}
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

	// Sweep remaining ETH
	// Re-check balance since gas was spent on token transfers
	ethBalance, err = client.BalanceAt(ctx, kp.Address, nil)
	if err != nil {
		return fmt.Errorf("final balance check: %w", err)
	}

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("gas price for ETH sweep: %w", err)
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
	ethTransferCost := new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(ethGasLimit))

	if ethBalance.Cmp(ethTransferCost) > 0 {
		sweepAmount := new(big.Int).Sub(ethBalance, ethTransferCost)
		s.logger.Info("sweeping ETH", "network", network, "address", kp.Address.Hex(), "amount", sweepAmount.String(), "destination", s.destination.Hex())

		if err := s.sendETHTransfer(ctx, client, kp, sweepAmount, ethGasLimit, gasPrice); err != nil {
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

	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("gas price: %w", err)
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

	tx := types.NewTransaction(nonce, token, big.NewInt(0), gasLimit, gasPrice, data)
	signer := types.NewEIP155Signer(chainID)
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

// sendETHTransfer sends an ETH transfer using the gasLimit and gasPrice already
// computed by the caller, ensuring the sweep amount and transaction fee are
// consistent (no re-estimation that could cause "insufficient funds" if the
// gas price ticks up between the two calls).
func (s *Sweeper) sendETHTransfer(ctx context.Context, client BlockchainClient, kp KeyPair, amount *big.Int, gasLimit uint64, gasPrice *big.Int) error {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	nonce, err := client.PendingNonceAt(ctx, kp.Address)
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}

	tx := types.NewTransaction(nonce, s.destination, amount, gasLimit, gasPrice, nil)
	signer := types.NewEIP155Signer(chainID)
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
