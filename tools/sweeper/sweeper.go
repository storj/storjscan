// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
)

// SweepFailure records a failed sweep attempt.
type SweepFailure struct {
	Network string
	Address common.Address
	LineNum int
	Err     error
}

// sweepConfig is what every sweep needs to reach the chain, shared by the
// Sweeper and by the strategy that moves the funds.
type sweepConfig struct {
	client      BlockchainClient
	paymentType PaymentType
	destination common.Address
	tokens      []common.Address
	rateDelay   time.Duration
	logger      *slog.Logger
}

// network is the chain name used in logs.
func (c sweepConfig) network() string {
	return c.paymentType.Network()
}

// Sweeper orchestrates sweeping ETH and ERC20 tokens from deposit wallets
// into a single destination address, for one payment type.
type Sweeper struct {
	sweepConfig

	strategy    sweepStrategy
	maxFailures int
	skipETH     bool
}

// reportDestinationBalance logs what the destination holds, so a run ends with
// the number the operator is actually after.
func (c sweepConfig) reportDestinationBalance(ctx context.Context) error {
	eth, err := c.client.BalanceAt(ctx, c.destination, nil)
	if err != nil {
		return fmt.Errorf("destination balance: %w", err)
	}

	attrs := []any{"network", c.network(), "destination", c.destination.Hex(), "ETH", eth.String()}
	for _, token := range c.tokens {
		balance, err := ERC20BalanceOf(ctx, c.client, token, c.destination)
		if err != nil {
			c.logger.Warn("failed to read destination token balance", "network", c.network(), "token", token.Hex(), "error", err)
			continue
		}
		attrs = append(attrs, fmt.Sprintf("token_%s", token.Hex()), balance.String())
	}
	c.logger.Info("destination balance", attrs...)
	return nil
}

// NewSweeper creates a Sweeper that moves funds with transactions sent by the
// deposit wallets themselves. Set maxFailures to 0 for unlimited.
func NewSweeper(
	client BlockchainClient,
	paymentType PaymentType,
	destination common.Address,
	tokens []common.Address,
	gasSource *KeyPair,
	rateDelay time.Duration,
	maxFailures int,
	skipETH bool,
	dryRun bool,
	logger *slog.Logger,
) *Sweeper {
	cfg := sweepConfig{
		client:      client,
		paymentType: paymentType,
		destination: destination,
		tokens:      tokens,
		rateDelay:   rateDelay,
		logger:      logger,
	}
	return &Sweeper{
		sweepConfig: cfg,
		strategy:    strategyFor(cfg, newDirectStrategy(cfg, gasSource, skipETH), dryRun),
		maxFailures: maxFailures,
		skipETH:     skipETH,
	}
}

// strategyFor returns the strategy a sweep should run with: the one that does
// the work, or the dry run standing in front of it.
func strategyFor(cfg sweepConfig, real sweepStrategy, dryRun bool) sweepStrategy {
	if dryRun {
		return newDryRunStrategy(cfg, real)
	}
	return real
}

// NewDelegatedSweeper creates a Sweeper that moves funds with an EIP-7702
// delegation instead of transactions sent from the deposit wallets. The
// delegation's sponsor pays for everything, so there is no gas source and no
// option to skip ETH: the contract always takes the whole balance.
func NewDelegatedSweeper(
	client BlockchainClient,
	paymentType PaymentType,
	destination common.Address,
	tokens []common.Address,
	delegation *Delegation,
	rateDelay time.Duration,
	maxFailures int,
	dryRun bool,
	logger *slog.Logger,
) *Sweeper {
	cfg := sweepConfig{
		client:      client,
		paymentType: paymentType,
		destination: destination,
		tokens:      tokens,
		rateDelay:   rateDelay,
		logger:      logger,
	}
	return &Sweeper{
		sweepConfig: cfg,
		strategy:    strategyFor(cfg, newDelegatedStrategy(cfg, delegation), dryRun),
		maxFailures: maxFailures,
	}
}

// SweepAll sweeps all keys for the configured payment type.
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

	if err := s.strategy.prepare(ctx); err != nil {
		return err
	}

	addrs := make([]common.Address, len(keys))
	for i, kp := range keys {
		addrs[i] = kp.Address
	}

	balances, err := s.queryBalances(ctx, addrs)
	if err != nil {
		return err
	}

	// Sweep the fattest wallets first, so a run that is cut short still moved
	// most of the funds.
	keys, balances = sortByAmountDesc(keys, balances, s.tokenDecimals(ctx))

	network := s.network()

	var failures []SweepFailure
	for i, kp := range keys {
		if !balances[i].HasFunds() {
			continue
		}
		if err := s.strategy.sweepWallet(ctx, kp, balances[i]); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.logger.Error("sweep failed, continuing", "network", network, "address", kp.Address.Hex(), "keyLine", kp.LineNum, "error", err)
			failures = append(failures, SweepFailure{Network: network, Address: kp.Address, LineNum: kp.LineNum, Err: err})
			if s.maxFailures > 0 && len(failures) >= s.maxFailures {
				s.logger.Error("circuit breaker tripped, aborting sweep", "failures", len(failures), "max", s.maxFailures)
				break
			}
		}
	}
	// Reporting where the sweep ended up is worth doing even after failures,
	// and never worth turning a finished sweep into an error.
	if err := s.strategy.finalize(ctx); err != nil {
		s.logger.Warn("could not report the final state", "network", network, "error", err)
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

func (s *Sweeper) queryBalances(ctx context.Context, addrs []common.Address) ([]WalletBalances, error) {
	network := s.network()

	var minETH *big.Int
	if !s.skipETH {
		gasFeeCap, _, err := suggestGasFees(ctx, s.client)
		if err != nil {
			return nil, fmt.Errorf("%s gas fees: %w", network, err)
		}
		minETH = new(big.Int).Mul(gasFeeCap, big.NewInt(21000))
	}

	s.logger.Info("querying balances via multicall", "network", network, "wallets", len(addrs))
	balances, err := MulticallBalances(ctx, s.logger, s.client, addrs, s.tokens, minETH, s.skipETH)
	if err != nil {
		return nil, fmt.Errorf("multicall %s balances: %w", network, err)
	}
	logBalanceSummary(s.logger, network, balances, s.tokens)
	return balances, nil
}

// tokenDecimals reads the decimals of every configured token, so balances of
// different scale can be compared when ordering the sweep. decimals() is
// optional in ERC20: a token that does not answer is reported as
// UnknownDecimals and ranked in raw units rather than failing the sweep.
func (s *Sweeper) tokenDecimals(ctx context.Context) []int {
	decimals := make([]int, len(s.tokens))
	for i, token := range s.tokens {
		dec, err := ERC20Decimals(ctx, s.client, token)
		if err != nil {
			s.logger.Warn("failed to read token decimals, ordering by raw units", "network", s.network(), "token", token.Hex(), "error", err)
			dec = UnknownDecimals
		}
		decimals[i] = dec
	}
	return decimals
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
