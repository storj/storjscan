// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// mockClient implements BlockchainClient for testing.
type mockClient struct {
	balanceAtFn          func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	codeAtFn             func(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error)
	pendingNonceAtFn     func(ctx context.Context, account common.Address) (uint64, error)
	suggestGasPriceFn    func(ctx context.Context) (*big.Int, error)
	suggestGasTipCapFn   func(ctx context.Context) (*big.Int, error)
	estimateGasFn        func(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
	sendTransactionFn    func(ctx context.Context, tx *types.Transaction) error
	chainIDFn            func(ctx context.Context) (*big.Int, error)
	callContractFn       func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	transactionReceiptFn func(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

func (m *mockClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return m.balanceAtFn(ctx, account, blockNumber)
}

func (m *mockClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	if m.codeAtFn != nil {
		return m.codeAtFn(ctx, account, blockNumber)
	}
	return nil, nil // default: a plain EOA with no code
}

func (m *mockClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return m.pendingNonceAtFn(ctx, account)
}

func (m *mockClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return m.suggestGasPriceFn(ctx)
}

func (m *mockClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	if m.suggestGasTipCapFn != nil {
		return m.suggestGasTipCapFn(ctx)
	}
	return big.NewInt(1e9), nil // default: 1 gwei tip
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
				return errors.New("connection reset by peer")
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

// signedTestTx returns a transaction with the given nonce, signed by a fresh
// key, and that key's address.
func signedTestTx(t *testing.T, nonce uint64) (*types.Transaction, common.Address) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	chainID := big.NewInt(1)
	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        &common.Address{},
		Gas:       21000,
		GasFeeCap: big.NewInt(2),
		GasTipCap: big.NewInt(1),
	}), types.LatestSignerForChainID(chainID), key)
	if err != nil {
		t.Fatal(err)
	}
	return tx, crypto.PubkeyToAddress(key.PublicKey)
}

// TestRetryClient_SendTransaction_NonceTooLowIsNotRetried covers a signed
// transaction whose nonce is already used: resending it can never succeed, so
// it must fail at once instead of retrying until the backoff gives up.
func TestRetryClient_SendTransaction_NonceTooLowIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			calls.Add(1)
			return errors.New("nonce=185826 minNonce=185827: nonce too low")
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return nil, ethereum.NotFound
		},
	}
	tx, _ := signedTestTx(t, 185826)
	err := NewRetryClient(mock, testLogger()).SendTransaction(context.Background(), tx)
	if err == nil || !strings.Contains(err.Error(), "nonce too low") {
		t.Fatalf("expected nonce too low error, got %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

// TestRetryClient_SendTransaction_OwnNonceTooLow covers an attempt that reached
// the node although its answer was lost: the retry then finds the nonce used by
// this very transaction, which is a successful send.
func TestRetryClient_SendTransaction_OwnNonceTooLow(t *testing.T) {
	tx, _ := signedTestTx(t, 7)
	var calls atomic.Int32
	mock := &mockClient{
		sendTransactionFn: func(ctx context.Context, sent *types.Transaction) error {
			if calls.Add(1) == 1 {
				return errors.New("connection reset by peer")
			}
			return errors.New("nonce too low")
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			if txHash != tx.Hash() {
				return nil, ethereum.NotFound
			}
			return &types.Receipt{Status: types.ReceiptStatusSuccessful}, nil
		},
	}
	if err := NewRetryClient(mock, testLogger()).SendTransaction(context.Background(), tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRetryClient_PendingNonceAt_NeverBelowMined covers a load-balanced
// endpoint answering from a backend that has not seen the latest block: after a
// transaction is mined, the next nonce must not reuse its nonce.
func TestRetryClient_PendingNonceAt_NeverBelowMined(t *testing.T) {
	tx, from := signedTestTx(t, 185826)
	reported := uint64(185826) // stale: the transaction below used it
	mock := &mockClient{
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return nil
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{Status: types.ReceiptStatusSuccessful}, nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return reported, nil
		},
	}
	rc := NewRetryClient(mock, testLogger())
	ctx := context.Background()

	if err := rc.SendTransaction(ctx, tx); err != nil {
		t.Fatal(err)
	}
	// Not mined yet: the node's answer is all there is to go by.
	if nonce, err := rc.PendingNonceAt(ctx, from); err != nil || nonce != 185826 {
		t.Fatalf("before mining: expected 185826, got %d (err %v)", nonce, err)
	}
	if _, err := rc.TransactionReceipt(ctx, tx.Hash()); err != nil {
		t.Fatal(err)
	}
	if nonce, err := rc.PendingNonceAt(ctx, from); err != nil || nonce != 185827 {
		t.Fatalf("after mining: expected 185827, got %d (err %v)", nonce, err)
	}

	// A node that is ahead, e.g. with transactions sent elsewhere, still wins.
	reported = 185830
	if nonce, err := rc.PendingNonceAt(ctx, from); err != nil || nonce != 185830 {
		t.Fatalf("node ahead: expected 185830, got %d (err %v)", nonce, err)
	}

	// Other senders are unaffected.
	reported = 3
	if nonce, err := rc.PendingNonceAt(ctx, common.Address{1}); err != nil || nonce != 3 {
		t.Fatalf("other sender: expected 3, got %d (err %v)", nonce, err)
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

// TestRetryClient_TransactionReceipt_TimeoutError checks that giving up on a
// receipt reports what actually happened — which transaction, and that it was
// still unmined when the wait expired — instead of a bare "not found" that
// gives an operator nothing to look up on a block explorer.
func TestRetryClient_TransactionReceipt_TimeoutError(t *testing.T) {
	txHash := common.HexToHash("0xabc123")
	mock := &mockClient{
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return nil, ethereum.NotFound
		},
	}
	rc := NewRetryClient(mock, testLogger())
	rc.SetReceiptTimeout(10 * time.Millisecond)

	_, err := rc.TransactionReceipt(context.Background(), txHash)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), txHash.Hex()) {
		t.Errorf("error does not name the transaction: %v", err)
	}
	if !strings.Contains(err.Error(), "not mined within") {
		t.Errorf("error does not explain the timeout: %v", err)
	}
	if !errors.Is(err, ethereum.NotFound) {
		t.Errorf("error no longer wraps ethereum.NotFound: %v", err)
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

// revertError is what a node returns when a call reverts: JSON-RPC error code 3.
type revertError struct{ message string }

func (e revertError) Error() string  { return e.message }
func (e revertError) ErrorCode() int { return 3 }

func TestRetryClient_EstimateGas_RevertIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			calls.Add(1)
			return 0, revertError{message: "execution reverted"}
		},
	}

	rc := NewRetryClient(mock, testLogger())
	_, err := rc.EstimateGas(context.Background(), ethereum.CallMsg{})
	if err == nil {
		t.Fatal("expected the revert to be returned")
	}
	// Retrying a revert would stall the sweep on this wallet instead of
	// recording it as a failure and moving to the next one.
	if got := calls.Load(); got != 1 {
		t.Fatalf("estimate was attempted %d times, want 1", got)
	}
}

func TestRetryClient_EstimateGas_RevertWithoutErrorCode(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			calls.Add(1)
			// Some providers report the revert in the message only.
			return 0, errors.New("execution reverted: insufficient balance")
		},
	}

	rc := NewRetryClient(mock, testLogger())
	if _, err := rc.EstimateGas(context.Background(), ethereum.CallMsg{}); err == nil {
		t.Fatal("expected the revert to be returned")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("estimate was attempted %d times, want 1", got)
	}
}

func TestRetryClient_CallContract_RevertIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			calls.Add(1)
			return nil, revertError{message: "execution reverted"}
		},
	}

	rc := NewRetryClient(mock, testLogger())
	if _, err := rc.CallContract(context.Background(), ethereum.CallMsg{}, nil); err == nil {
		t.Fatal("expected the revert to be returned")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("call was attempted %d times, want 1", got)
	}
}

func TestRetryClient_EstimateGas_TransientErrorIsRetried(t *testing.T) {
	var calls atomic.Int32
	mock := &mockClient{
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			if calls.Add(1) < 3 {
				return 0, errors.New("connection reset")
			}
			return 21000, nil
		},
	}

	rc := NewRetryClient(mock, testLogger())
	gas, err := rc.EstimateGas(context.Background(), ethereum.CallMsg{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gas != 21000 || calls.Load() != 3 {
		t.Fatalf("got %d gas after %d attempts, want 21000 after 3", gas, calls.Load())
	}
}
