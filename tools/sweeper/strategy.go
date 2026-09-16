// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import "context"

// sweepStrategy is how one wallet's funds are actually moved. Everything around
// it — reading balances, ordering the wallets, collecting failures — belongs to
// the Sweeper, so the mechanisms only have to differ in the transactions they
// send: see directStrategy (transactions sent by the deposit wallet itself),
// delegatedStrategy (EIP-7702, in delegate.go), and dryRunStrategy, which sends
// nothing and reports what the others would have done.
type sweepStrategy interface {
	// prepare runs once before the first wallet, for whatever a strategy has to
	// check before it starts spending gas.
	prepare(ctx context.Context) error
	// sweepWallet moves what one wallet holds to the destination. balance is
	// what the pre-run balance query saw for this wallet; a strategy that sends
	// transactions re-reads what it needs, since the amounts have to match what
	// the wallet holds now, but it is all a reporting strategy has to work
	// with.
	sweepWallet(ctx context.Context, kp KeyPair, balance WalletBalances) error
	// finalize runs once after the last wallet, to report where the sweep left
	// things. It is informational: an error here does not fail the sweep.
	finalize(ctx context.Context) error
}

// walletPlanner is implemented by a strategy that can say what sweeping one
// wallet would take beyond its balances — gas to fund, a delegation to install.
// The dry run reports these next to the balances when the strategy standing
// behind it offers them.
type walletPlanner interface {
	planWallet(ctx context.Context, kp KeyPair, balance WalletBalances) ([]any, error)
}
