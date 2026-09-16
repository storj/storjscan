// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// Delegation configures a sweep that runs inside the deposit wallet's own
// account through an EIP-7702 delegation, rather than through transactions sent
// by the deposit wallet itself.
//
// Each wallet authorises the deployed Sweeper7702 contract (see
// contract/sweeper.sol) to run as its code, and the sponsor then calls sweep()
// on the wallet address. The contract moves every configured token plus the
// whole ETH balance to the destination it was constructed with, in one
// transaction. Because the sponsor pays the gas, no ETH has to be sent to the
// deposit wallet first and nothing has to be left behind to cover fees.
type Delegation struct {
	// Contract is the deployed Sweeper7702 the wallets delegate their code to.
	Contract common.Address
	// Sponsor signs and pays for every transaction: the delegation and the
	// sweep call. It is the destination wallet's own key.
	Sponsor *KeyPair
}

// delegatedStrategy sweeps wallets through an EIP-7702 delegation. The
// protocol-level pieces it builds on live in eip7702.go.
type delegatedStrategy struct {
	sweepConfig

	delegation *Delegation
}

func newDelegatedStrategy(cfg sweepConfig, delegation *Delegation) *delegatedStrategy {
	return &delegatedStrategy{sweepConfig: cfg, delegation: delegation}
}

// prepare checks the sweep is pointed at the right contract before a single
// wallet is touched. The destination is immutable in the deployed contract, so
// a mismatch with the configured destination means the sweep would send every
// wallet's funds somewhere the operator did not ask for.
func (d *delegatedStrategy) prepare(ctx context.Context) error {
	contractDest, err := ContractDestination(ctx, d.client, d.delegation.Contract)
	if err != nil {
		return fmt.Errorf("sweeper contract %s: %w", d.delegation.Contract.Hex(), err)
	}
	if contractDest != d.destination {
		return fmt.Errorf("sweeper contract %s sweeps into %s, not the configured destination %s",
			d.delegation.Contract.Hex(), contractDest.Hex(), d.destination.Hex())
	}

	sponsorBalance, err := d.client.BalanceAt(ctx, d.delegation.Sponsor.Address, nil)
	if err != nil {
		return fmt.Errorf("sponsor balance: %w", err)
	}
	if sponsorBalance.Sign() == 0 {
		return fmt.Errorf("sponsor %s has no ETH to pay for gas", d.delegation.Sponsor.Address.Hex())
	}

	d.logger.Info("delegated sweep configured",
		"network", d.network(),
		"contract", d.delegation.Contract.Hex(),
		"sponsor", d.delegation.Sponsor.Address.Hex(),
		"sponsorBalance", sponsorBalance.String(),
		"destination", contractDest.Hex())
	return nil
}

// sweepWallet sweeps one wallet through the EIP-7702 contract. A wallet that is
// not delegated to the contract yet gets the authorization attached to the same
// transaction that calls sweep(), so a wallet is never left delegated but
// unswept.
// balance is what the pre-run query saw; the contract reads the real balances
// on chain when it runs, so nothing here depends on it.
func (d *delegatedStrategy) sweepWallet(ctx context.Context, kp KeyPair, balance WalletBalances) error {
	client := d.client
	network := d.network()
	sponsor := d.delegation.Sponsor

	if kp.Address == sponsor.Address {
		// Sweeping the sponsor into itself would only burn gas, and EIP-7702
		// counts the nonce differently when the authority sends the
		// transaction, so the authorization would be rejected anyway.
		d.logger.Info("skipping wallet, it is the sponsor", "network", network, "address", kp.Address.Hex())
		return nil
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.rateDelay):
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}

	auths, err := d.authorizations(ctx, kp, chainID)
	if err != nil {
		return err
	}
	d.logger.Info("sweeping wallet via delegation", "network", network, "address", kp.Address.Hex(),
		"delegating", len(auths) > 0, "expectedETH", balance.ETH.String(), "destination", d.destination.Hex())

	// Every configured token is passed to the contract, not just the ones the
	// balance query found: the contract skips empty ones for a couple of
	// thousand gas, and a deposit that lands between the query and the sweep is
	// still picked up.
	data := SweepCallData(d.tokens)

	gasLimit, err := EstimateDelegatedSweepGas(ctx, client, sponsor.Address, kp.Address, data, auths, len(d.tokens))
	if err != nil {
		return err
	}

	gasFeeCap, gasTipCap, err := suggestGasFees(ctx, client)
	if err != nil {
		return err
	}

	nonce, err := client.PendingNonceAt(ctx, sponsor.Address)
	if err != nil {
		return fmt.Errorf("sponsor nonce: %w", err)
	}

	tx, err := NewDelegatedSweepTx(chainID, nonce, kp.Address, data, gasLimit, gasFeeCap, gasTipCap, auths)
	if err != nil {
		return err
	}
	signedTx, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), sponsor.PrivateKey)
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
		return fmt.Errorf("sweep transaction %s failed with status %d", signedTx.Hash().Hex(), receipt.Status)
	}

	// An authorization whose nonce no longer matches the wallet is dropped
	// silently: the transaction still succeeds, but it called a plain EOA and
	// swept nothing. Confirm the delegation actually took before reporting the
	// wallet as done.
	if len(auths) > 0 {
		target, delegated, err := DelegationTarget(ctx, client, kp.Address)
		if err != nil {
			return fmt.Errorf("confirming delegation: %w", err)
		}
		if !delegated || target != d.delegation.Contract {
			return fmt.Errorf("authorization in transaction %s was not applied, wallet swept nothing (its nonce likely changed)", signedTx.Hash().Hex())
		}
	}

	d.logger.Info("wallet swept", "network", network, "address", kp.Address.Hex(),
		"tx", signedTx.Hash().Hex(), "gasUsed", receipt.GasUsed)
	return nil
}

// authorizations returns the authorizations the sweep transaction has to carry
// for this wallet: none when it already runs the sweeper contract's code, one
// signed by the wallet otherwise.
func (d *delegatedStrategy) authorizations(ctx context.Context, kp KeyPair, chainID *big.Int) ([]types.SetCodeAuthorization, error) {
	target, delegated, err := DelegationTarget(ctx, d.client, kp.Address)
	if err != nil {
		return nil, err
	}
	if delegated && target == d.delegation.Contract {
		return nil, nil
	}
	if delegated {
		d.logger.Warn("wallet delegates to a different contract, replacing it",
			"network", d.network(), "address", kp.Address.Hex(),
			"current", target.Hex(), "want", d.delegation.Contract.Hex())
	}

	auth, err := SignDelegation(ctx, d.client, kp, chainID, d.delegation.Contract)
	if err != nil {
		return nil, err
	}
	return []types.SetCodeAuthorization{auth}, nil
}

// finalize reports what the destination ended up with.
func (d *delegatedStrategy) finalize(ctx context.Context) error {
	return d.reportDestinationBalance(ctx)
}

// planWallet works out what this wallet would take: whether it still has to be
// delegated, and what the sponsor would pay to sweep it. Everything the wallet
// holds is recoverable, since the sponsor covers the fees.
func (d *delegatedStrategy) planWallet(ctx context.Context, kp KeyPair, balance WalletBalances) ([]any, error) {
	if kp.Address == d.delegation.Sponsor.Address {
		return []any{"skipped", "wallet is the sponsor"}, nil
	}

	chainID, err := d.client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain ID: %w", err)
	}
	auths, err := d.authorizations(ctx, kp, chainID)
	if err != nil {
		return nil, err
	}

	gasFeeCap, _, err := suggestGasFees(ctx, d.client)
	if err != nil {
		return nil, err
	}
	gasLimit, err := EstimateDelegatedSweepGas(ctx, d.client, d.delegation.Sponsor.Address, kp.Address,
		SweepCallData(d.tokens), auths, len(d.tokens))
	if err != nil {
		return nil, err
	}

	return []any{
		"delegating", len(auths) > 0,
		"sponsorGasCost", new(big.Int).Mul(gasFeeCap, new(big.Int).SetUint64(gasLimit)).String(),
	}, nil
}
