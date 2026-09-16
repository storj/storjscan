// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"math/big"
	"time"
)

// dryRunStrategy reports what a sweep would move without sending anything. It
// stands in for the strategy that would have done the work, and still asks that
// one to prepare(), so a dry run surfaces the same setup problems a real run
// would hit. Whatever the real strategy can add about a wallet — gas it would
// have to fund, a delegation it would install — comes from walletPlanner.
type dryRunStrategy struct {
	sweepConfig

	// real is the strategy the sweep would have used. It is only asked to
	// prepare and to describe wallets, never to move anything.
	real sweepStrategy

	// skipETH means the balance query left ETH out, so the ETH figures in the
	// balances are not real and are not reported; the plan reads its own.
	skipETH bool

	wallets     int
	unplanned   int
	totalETH    *big.Int
	tokenTotals []*big.Int
}

func newDryRunStrategy(cfg sweepConfig, real sweepStrategy, skipETH bool) *dryRunStrategy {
	tokenTotals := make([]*big.Int, len(cfg.tokens))
	for i := range tokenTotals {
		tokenTotals[i] = new(big.Int)
	}
	return &dryRunStrategy{
		sweepConfig: cfg,
		real:        real,
		skipETH:     skipETH,
		totalETH:    new(big.Int),
		tokenTotals: tokenTotals,
	}
}

func (d *dryRunStrategy) prepare(ctx context.Context) error {
	return d.real.prepare(ctx)
}

// sweepWallet prints what the wallet holds instead of moving it. Describing a
// wallet still costs RPC calls, so the dry run paces itself like the sweep
// would.
func (d *dryRunStrategy) sweepWallet(ctx context.Context, kp KeyPair, balance WalletBalances) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.rateDelay):
	}

	d.wallets++

	attrs := []any{
		"network", d.network(),
		"address", kp.Address.Hex(),
		"keyLine", kp.LineNum,
	}
	if balance.ETH != nil && !d.skipETH {
		d.totalETH.Add(d.totalETH, balance.ETH)
		attrs = append(attrs, "ethBalance", balance.ETH.String())
	}
	for i, tb := range balance.Tokens {
		if i >= len(d.tokens) || tb == nil || tb.Sign() <= 0 {
			continue
		}
		d.tokenTotals[i].Add(d.tokenTotals[i], tb)
		attrs = append(attrs, fmt.Sprintf("token_%s", d.tokens[i].Hex()), tb.String())
	}

	var planErr error
	if planner, ok := d.real.(walletPlanner); ok {
		plan, err := planner.planWallet(ctx, kp, balance)
		if err != nil {
			// The balances are still worth printing, but a wallet the sweep
			// cannot be priced for is a failed wallet: it is counted in the
			// summary and fails the run, the same as a wallet the sweep could
			// not move, rather than leaving a report that looks complete.
			d.unplanned++
			planErr = fmt.Errorf("dry-run: cannot describe what the sweep would take: %w", err)
		} else {
			attrs = append(attrs, plan...)
		}
	}

	d.logger.Info("dry-run: wallet", attrs...)
	return planErr
}

// finalize prints the totals the run would have moved, and what the destination
// holds today to compare them against.
func (d *dryRunStrategy) finalize(ctx context.Context) error {
	if d.wallets == 0 {
		d.logger.Info("dry-run: no wallets with funds", "network", d.network())
		return nil
	}

	attrs := []any{
		"network", d.network(),
		"walletsWithFunds", d.wallets,
	}
	if d.unplanned > 0 {
		attrs = append(attrs, "walletsWithoutPlan", d.unplanned)
	}
	if !d.skipETH {
		attrs = append(attrs, "totalETH", d.totalETH.String())
	}
	for i, token := range d.tokens {
		attrs = append(attrs, fmt.Sprintf("totalToken_%s", token.Hex()), d.tokenTotals[i].String())
	}
	d.logger.Info("dry-run: summary", attrs...)

	return d.reportDestinationBalance(ctx)
}
