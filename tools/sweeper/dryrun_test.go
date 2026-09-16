// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// recordingLogger collects the messages and attributes logged to it, so tests
// can assert on what a dry run reported.
type recordingLogger struct {
	records []slog.Record
}

func (r *recordingLogger) Enabled(context.Context, slog.Level) bool { return true }

func (r *recordingLogger) Handle(_ context.Context, rec slog.Record) error {
	r.records = append(r.records, rec.Clone())
	return nil
}

func (r *recordingLogger) WithAttrs([]slog.Attr) slog.Handler { return r }

func (r *recordingLogger) WithGroup(string) slog.Handler { return r }

func (r *recordingLogger) logger() *slog.Logger { return slog.New(r) }

// attrsOf returns the attributes of the first record with the given message.
func (r *recordingLogger) attrsOf(msg string) map[string]string {
	for _, rec := range r.records {
		if rec.Message != msg {
			continue
		}
		attrs := make(map[string]string)
		rec.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		return attrs
	}
	return nil
}

func (r *recordingLogger) has(msg string) bool {
	return r.attrsOf(msg) != nil
}

func TestDryRun_ReportsBalancesAndSendsNothing(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	ethBalance := big.NewInt(1e18)

	log := &recordingLogger{}
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		t.Fatal("a dry run must not send transactions")
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, true, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wallet := log.attrsOf("dry-run: wallet")
	if wallet == nil {
		t.Fatal("expected a per-wallet dry-run line")
	}
	if wallet["address"] != kp.Address.Hex() {
		t.Fatalf("address: got %s, want %s", wallet["address"], kp.Address.Hex())
	}
	if wallet["ethBalance"] != ethBalance.String() {
		t.Fatalf("ethBalance: got %s, want %s", wallet["ethBalance"], ethBalance)
	}
	// The direct strategy standing behind the dry run adds what it would cost.
	if _, ok := wallet["recoverableETH"]; !ok {
		t.Fatalf("expected the direct strategy's plan in the report, got %v", wallet)
	}

	summary := log.attrsOf("dry-run: summary")
	if summary == nil {
		t.Fatal("expected a dry-run summary")
	}
	if summary["walletsWithFunds"] != "1" {
		t.Fatalf("walletsWithFunds: got %s, want 1", summary["walletsWithFunds"])
	}
	if summary["totalETH"] != ethBalance.String() {
		t.Fatalf("totalETH: got %s, want %s", summary["totalETH"], ethBalance)
	}
	if !log.has("destination balance") {
		t.Fatal("expected the destination balance to be reported")
	}
}

func TestDryRun_TotalsAcrossWallets(t *testing.T) {
	kp1, kp2 := newTestKey(t), newTestKey(t)
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")

	log := &recordingLogger{}
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1e18), nil
	}
	// Every multicall slot — ETH and token alike — reports the same value.
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return multicallValueResponse(multicallCallCount(msg.Data), big.NewInt(1e18)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return make([]byte, 32), nil
		},
	)

	sw := NewSweeper(mock, PaymentTypeL1, common.HexToAddress("0xdead"), []common.Address{token}, nil, 0, 0, false, true, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary := log.attrsOf("dry-run: summary")
	if summary["walletsWithFunds"] != "2" {
		t.Fatalf("walletsWithFunds: got %s, want 2", summary["walletsWithFunds"])
	}
	want := big.NewInt(2e18).String()
	if summary["totalETH"] != want {
		t.Fatalf("totalETH: got %s, want %s", summary["totalETH"], want)
	}
	if got := summary["totalToken_"+token.Hex()]; got != want {
		t.Fatalf("token total: got %s, want %s", got, want)
	}
}

func TestDryRun_NoWalletsWithFunds(t *testing.T) {
	log := &recordingLogger{}
	mock := fullMock() // every balance is zero

	sw := NewSweeper(mock, PaymentTypeL1, common.HexToAddress("0xdead"), nil, nil, 0, 0, false, true, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{newTestKey(t)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !log.has("dry-run: no wallets with funds") {
		t.Fatal("expected the empty-run message")
	}
	if log.has("dry-run: summary") {
		t.Fatal("did not expect a summary when nothing would be swept")
	}
}

func TestDryRun_RunsTheRealStrategysPrepare(t *testing.T) {
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	contractDest := common.HexToAddress("0xdeadbeef")
	configured := common.HexToAddress("0xfeedface")

	mock := delegatedMock(contract, contractDest, big.NewInt(1e18), func(common.Address) []byte { return nil })

	// The delegated strategy refuses a contract that sweeps somewhere else; the
	// dry run has to surface that too, or it would report a sweep that could
	// never run.
	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, configured, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, true, testLogger())

	err := sw.SweepAll(context.Background(), []KeyPair{newTestKey(t)})
	if err == nil || !strings.Contains(err.Error(), "not the configured destination") {
		t.Fatalf("expected the destination mismatch to surface in a dry run, got %v", err)
	}
}

func TestDryRun_ReportsWalletEvenWhenPlanFails(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")

	log := &recordingLogger{}
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1e18), nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return multicallValueResponse(multicallCallCount(msg.Data), big.NewInt(1e18)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return make([]byte, 32), nil
		},
	)
	// Gas estimation fails, so the direct strategy cannot say what the sweep
	// would cost.
	mock.estimateGasFn = func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
		return 0, errors.New("estimation unavailable")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.HexToAddress("0xdead"), []common.Address{token}, nil, 0, 0, false, true, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wallet := log.attrsOf("dry-run: wallet")
	if wallet == nil {
		t.Fatal("expected the balances to be reported even without a plan")
	}
	if _, ok := wallet["recoverableETH"]; ok {
		t.Fatal("did not expect a plan when estimation failed")
	}
	if !log.has("dry-run: cannot describe what the sweep would take") {
		t.Fatal("expected a warning about the missing plan")
	}
}

func TestDryRun_DelegatedPlanReportsDelegation(t *testing.T) {
	kp := newTestKey(t)
	sponsorKey := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	log := &recordingLogger{}
	mock := delegatedMock(contract, dest, big.NewInt(1e18), func(common.Address) []byte { return nil })

	sw := NewDelegatedSweeper(mock, PaymentTypeL17702, dest, nil, &Delegation{
		Contract: contract,
		Sponsor:  &sponsorKey,
	}, 0, 0, true, log.logger())

	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wallet := log.attrsOf("dry-run: wallet")
	if wallet["delegating"] != "true" {
		t.Fatalf("expected the wallet to be reported as needing a delegation, got %v", wallet)
	}
	if wallet["sponsorGasCost"] == "" || wallet["sponsorGasCost"] == "0" {
		t.Fatalf("expected a sponsor gas cost, got %q", wallet["sponsorGasCost"])
	}
}

func TestFinalize_ReportsDestinationBalance(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	destETH := big.NewInt(7e18)

	log := &recordingLogger{}
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == destination {
			return destETH, nil
		}
		return big.NewInt(0), nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, false, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	final := log.attrsOf("destination balance")
	if final == nil {
		t.Fatal("expected the destination balance to be reported")
	}
	if final["destination"] != destination.Hex() {
		t.Fatalf("destination: got %s, want %s", final["destination"], destination.Hex())
	}
	if final["ETH"] != destETH.String() {
		t.Fatalf("ETH: got %s, want %s", final["ETH"], destETH)
	}
	if _, ok := final["token_"+token.Hex()]; !ok {
		t.Fatalf("expected the token balance in the report, got %v", final)
	}
}

func TestFinalize_FailureDoesNotFailTheSweep(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")

	log := &recordingLogger{}
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == destination {
			return nil, errors.New("rpc error")
		}
		return big.NewInt(0), nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, log.logger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("a failed final report must not fail the sweep, got %v", err)
	}
	if !log.has("could not report the final state") {
		t.Fatal("expected a warning about the failed final report")
	}
}
