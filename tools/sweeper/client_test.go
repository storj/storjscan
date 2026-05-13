// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"sync/atomic"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// mockClient implements BlockchainClient for testing.
type mockClient struct {
	balanceAtFn          func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	pendingNonceAtFn     func(ctx context.Context, account common.Address) (uint64, error)
	suggestGasPriceFn    func(ctx context.Context) (*big.Int, error)
	estimateGasFn        func(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
	sendTransactionFn    func(ctx context.Context, tx *types.Transaction) error
	chainIDFn            func(ctx context.Context) (*big.Int, error)
	callContractFn       func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	transactionReceiptFn func(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

func (m *mockClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return m.balanceAtFn(ctx, account, blockNumber)
}

func (m *mockClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return m.pendingNonceAtFn(ctx, account)
}

func (m *mockClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return m.suggestGasPriceFn(ctx)
}

func (m *mockClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return m.estimateGasFn(ctx, msg)
}

func (m *mockClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return m.sendTransactionFn(ctx, tx)
}

func (m *mockClient) ChainID(ctx context.Context) (*big.Int, error) {
	return m.chainIDFn(ctx)
}

func (m *mockClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return m.callContractFn(ctx, msg, blockNumber)
}

func (m *mockClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return m.transactionReceiptFn(ctx, txHash)
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRetryClient_BalanceAt_Success(t *testing.T) {
	expected := big.NewInt(1000)
	mock := &mockClient{
		balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.BalanceAt(context.Background(), common.Address{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(expected) != 0 {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestRetryClient_BalanceAt_RetriesThenSucceeds(t *testing.T) {
	expected := big.NewInt(5000)
	var calls atomic.Int32
	mock := &mockClient{
		balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
			if calls.Add(1) <= 3 {
				return nil, errors.New("connection refused")
			}
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.BalanceAt(context.Background(), common.Address{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(expected) != 0 {
		t.Fatalf("expected %v, got %v", expected, result)
	}
	if calls.Load() != 4 {
		t.Fatalf("expected 4 calls, got %d", calls.Load())
	}
}

func TestRetryClient_BalanceAt_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mock := &mockClient{
		balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
			return nil, errors.New("connection refused")
		},
	}
	rc := NewRetryClient(mock, testLogger())
	_, err := rc.BalanceAt(ctx, common.Address{}, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRetryClient_PendingNonceAt_RetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			if calls.Add(1) <= 2 {
				return 0, errors.New("timeout")
			}
			return 42, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.PendingNonceAt(context.Background(), common.Address{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 42 {
		t.Fatalf("expected 42, got %d", result)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestRetryClient_SuggestGasPrice_Success(t *testing.T) {
	expected := big.NewInt(20000000000)
	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.SuggestGasPrice(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(expected) != 0 {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestRetryClient_EstimateGas_RetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			if calls.Add(1) <= 1 {
				return 0, errors.New("server error")
			}
			return 65000, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.EstimateGas(context.Background(), ethereum.CallMsg{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != 65000 {
		t.Fatalf("expected 65000, got %d", result)
	}
}

func TestRetryClient_SendTransaction_Success(t *testing.T) {
	mock := &mockClient{
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	err := rc.SendTransaction(context.Background(), types.NewTransaction(0, common.Address{}, big.NewInt(0), 0, big.NewInt(0), nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRetryClient_SendTransaction_RetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			if calls.Add(1) <= 2 {
				return errors.New("nonce too low")
			}
			return nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	err := rc.SendTransaction(context.Background(), types.NewTransaction(0, common.Address{}, big.NewInt(0), 0, big.NewInt(0), nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestRetryClient_ChainID_Success(t *testing.T) {
	expected := big.NewInt(1)
	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.ChainID(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Cmp(expected) != 0 {
		t.Fatalf("expected %v, got %v", expected, result)
	}
}

func TestRetryClient_CallContract_Success(t *testing.T) {
	expected := []byte{0x01, 0x02, 0x03}
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.CallContract(context.Background(), ethereum.CallMsg{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 || result[0] != 0x01 {
		t.Fatalf("unexpected result: %v", result)
	}
}

func TestRetryClient_TransactionReceipt_RetriesThenSucceeds(t *testing.T) {
	expected := &types.Receipt{Status: types.ReceiptStatusSuccessful}
	var calls atomic.Int32
	mock := &mockClient{
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			if calls.Add(1) <= 2 {
				return nil, errors.New("not found")
			}
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.TransactionReceipt(context.Background(), common.Hash{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("expected successful receipt, got status %d", result.Status)
	}
	if calls.Load() != 3 {
		t.Fatalf("expected 3 calls, got %d", calls.Load())
	}
}

func TestRetryClient_WaitMined(t *testing.T) {
	expected := &types.Receipt{Status: types.ReceiptStatusSuccessful}
	var calls atomic.Int32
	mock := &mockClient{
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			if calls.Add(1) <= 3 {
				return nil, errors.New("not found")
			}
			return expected, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	result, err := rc.WaitMined(context.Background(), common.Hash{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("expected successful receipt, got status %d", result.Status)
	}
	if calls.Load() != 4 {
		t.Fatalf("expected 4 calls, got %d", calls.Load())
	}
}

func TestNewRetryClient(t *testing.T) {
	mock := &mockClient{}
	logger := slog.Default()
	rc := NewRetryClient(mock, logger)
	if rc.client != mock {
		t.Fatal("client not set correctly")
	}
	if rc.logger != logger {
		t.Fatal("logger not set correctly")
	}
}

// Verify RetryClient implements BlockchainClient.
var _ BlockchainClient = (*RetryClient)(nil)
