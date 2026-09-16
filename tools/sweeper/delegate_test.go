// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"bytes"
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// delegatedMock returns a mock wired for a delegated sweep: the Sweeper7702
// contract lives at contract and reports dest from DEST(), every wallet holds
// walletETH, and wallet code is whatever walletCode returns.
func delegatedMock(contract, dest common.Address, walletETH *big.Int, walletCode func(common.Address) []byte) *mockClient {
	m := fullMock()
	m.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return walletETH, nil
	}
	m.codeAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
		if account == contract {
			return []byte{0x60, 0x80}, nil // the deployed sweeper contract
		}
		return walletCode(account), nil
	}
	m.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		switch {
		case msg.To != nil && *msg.To == multicall3Address:
			return multicallValueResponse(multicallCallCount(msg.Data), walletETH), nil
		case msg.To != nil && *msg.To == contract:
			result := make([]byte, 32)
			copy(result[12:], dest.Bytes())
			return result, nil
		default:
			return make([]byte, 32), nil
		}
	}
	return m
}

func TestSweepAll_Delegated_InstallsDelegationAndSweeps(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")

	var sent *types.Transaction
	// The wallet has no code until the set-code transaction is mined.
	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(common.Address) []byte {
		if sent == nil {
			return nil
		}
		return types.AddressToDelegation(contract)
	})
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		sent = tx
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, []common.Address{token}, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sent == nil {
		t.Fatal("expected a sweep transaction")
	}
	if sent.Type() != types.SetCodeTxType {
		t.Fatalf("transaction type: got %d, want %d (set code)", sent.Type(), types.SetCodeTxType)
	}
	if sent.To() == nil || *sent.To() != kp.Address {
		t.Fatalf("transaction sent to %v, want the deposit wallet %s", sent.To(), kp.Address.Hex())
	}
	if !bytes.Equal(sent.Data(), SweepCallData([]common.Address{token})) {
		t.Fatalf("unexpected calldata %x", sent.Data())
	}

	// The sponsor signs and pays for the transaction...
	from, err := types.Sender(types.LatestSignerForChainID(big.NewInt(1)), sent)
	if err != nil {
		t.Fatalf("recovering sender: %v", err)
	}
	if from != sponsorKey.Address {
		t.Fatalf("sender: got %s, want the sponsor %s", from.Hex(), sponsorKey.Address.Hex())
	}

	// ...while the deposit wallet signs the authorization that lets the
	// contract's code run in its own account.
	auths := sent.SetCodeAuthorizations()
	if len(auths) != 1 {
		t.Fatalf("authorizations: got %d, want 1", len(auths))
	}
	if auths[0].Address != contract {
		t.Fatalf("authorization target: got %s, want %s", auths[0].Address.Hex(), contract.Hex())
	}
	authority, err := auths[0].Authority()
	if err != nil {
		t.Fatalf("recovering authority: %v", err)
	}
	if authority != kp.Address {
		t.Fatalf("authority: got %s, want the deposit wallet %s", authority.Hex(), kp.Address.Hex())
	}
}

func TestSweepAll_Delegated_AuthorizationNotApplied(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	// The wallet never picks up the delegation: the transaction is mined, but
	// it called a plain EOA and moved nothing.
	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(common.Address) []byte { return nil })

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil || !strings.Contains(err.Error(), "1 address(es) failed to sweep") {
		t.Fatalf("expected the wallet to be recorded as a failure, got %v", err)
	}
}

func TestSweepAll_Delegated_AlreadyDelegated(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	var sent *types.Transaction
	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(account common.Address) []byte {
		return types.AddressToDelegation(contract)
	})
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		sent = tx
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sent == nil {
		t.Fatal("expected a sweep transaction")
	}
	// No second authorization once the wallet already runs the contract's code.
	if sent.Type() != types.DynamicFeeTxType {
		t.Fatalf("transaction type: got %d, want %d (dynamic fee)", sent.Type(), types.DynamicFeeTxType)
	}
	if sent.To() == nil || *sent.To() != kp.Address {
		t.Fatalf("transaction sent to %v, want the deposit wallet %s", sent.To(), kp.Address.Hex())
	}
}

func TestSweepAll_Delegated_ReplacesForeignDelegation(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	other := common.HexToAddress("0xbadc0de")
	dest := common.HexToAddress("0xdeadbeef")

	var sent *types.Transaction
	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(account common.Address) []byte {
		if sent == nil {
			return types.AddressToDelegation(other)
		}
		return types.AddressToDelegation(contract)
	})
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		sent = tx
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent == nil || sent.Type() != types.SetCodeTxType {
		t.Fatal("expected a set-code transaction re-pointing the delegation")
	}
	if auths := sent.SetCodeAuthorizations(); len(auths) != 1 || auths[0].Address != contract {
		t.Fatalf("expected an authorization for %s, got %v", contract.Hex(), auths)
	}
}

func TestSweepAll_Delegated_WalletHoldsContractCode(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(account common.Address) []byte {
		return []byte{0x60, 0x80, 0x60, 0x40}
	})
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		t.Fatal("no transaction should be sent for an account holding contract code")
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil || !strings.Contains(err.Error(), "1 address(es) failed to sweep") {
		t.Fatalf("expected the wallet to be recorded as a failure, got %v", err)
	}
}

func TestSweepAll_Delegated_DestinationMismatch(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	contractDest := common.HexToAddress("0xdeadbeef")
	configured := common.HexToAddress("0xfeedface")

	mock := delegatedMock(contract, contractDest, big.NewInt(1e18), func(common.Address) []byte { return nil })
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		t.Fatal("no transaction should be sent when the destinations disagree")
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, configured, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil || !strings.Contains(err.Error(), "not the configured destination") {
		t.Fatalf("expected a destination mismatch error, got %v", err)
	}
}

func TestSweepAll_Delegated_SponsorWithoutETH(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	mock := delegatedMock(contract, dest, big.NewInt(0), func(common.Address) []byte { return nil })

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil || !strings.Contains(err.Error(), "no ETH to pay for gas") {
		t.Fatalf("expected a sponsor funding error, got %v", err)
	}
}

func TestSweepAll_Delegated_SkipsSponsorItself(t *testing.T) {
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(common.Address) []byte { return nil })
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		t.Fatal("the sponsor must not sweep itself")
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, false, testLogger())

	if err := sw.SweepAll(context.Background(), []KeyPair{sponsorKey}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSweepAll_Delegated_DryRunSendsNothing(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(common.Address) []byte { return nil })
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		t.Fatal("dry run must not send transactions")
		return nil
	}

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, true, testLogger())

	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPaymentTypeL17702_Network(t *testing.T) {
	if got := PaymentTypeL17702.Network(); got != "ethereum" {
		t.Fatalf("network: got %s, want ethereum", got)
	}
}
