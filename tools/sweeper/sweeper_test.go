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
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func newTestKey(t *testing.T) KeyPair {
	t.Helper()
	pk, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}
}

func successReceipt() *types.Receipt {
	return &types.Receipt{Status: types.ReceiptStatusSuccessful}
}

// multicallZeroResponse returns a valid aggregate3 response with n zero-value results.
func multicallZeroResponse(n int) []byte {
	vals := make([]*big.Int, n)
	for i := range vals {
		vals[i] = big.NewInt(0)
	}
	return encodeAggregate3Response(vals)
}

// multicallValueResponse returns a valid aggregate3 response where every result
// is set to the given value.
func multicallValueResponse(n int, val *big.Int) []byte {
	vals := make([]*big.Int, n)
	for i := range vals {
		vals[i] = new(big.Int).Set(val)
	}
	return encodeAggregate3Response(vals)
}

// mockCallContract returns a callContractFn that routes Multicall3 aggregate3
// calls to the provided multicallFn and all other calls to erc20Fn.
func mockCallContract(
	multicallFn func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error),
	erc20Fn func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error),
) func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			return multicallFn(ctx, msg, blockNumber)
		}
		return erc20Fn(ctx, msg, blockNumber)
	}
}

// fullMock returns a mockClient with reasonable defaults for sweep tests.
// The callContractFn automatically handles Multicall3 aggregate3 calls by
// forwarding ETH-balance sub-calls to the mock's own balanceAtFn, so that
// tests only need to set balanceAtFn to control what the multicall pre-check
// sees.
func fullMock() *mockClient {
	m := &mockClient{
		balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
			return big.NewInt(0), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1000000000), nil // 1 gwei
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return 60000, nil
		},
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return nil
		},
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return successReceipt(), nil
		},
	}
	// callContractFn is set after m is created so it can close over m to
	// delegate ETH-balance queries within a multicall to balanceAtFn.
	m.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			// Use the first wallet's ETH balance as representative for all slots.
			// Most tests use a single wallet; for multi-wallet tests the specific
			// balance per address doesn't matter as long as it's consistent.
			bal, err := m.balanceAtFn(ctx, common.Address{}, nil)
			if err != nil {
				// If balanceAtFn errors, return zero so the wallet is skipped by
				// the multicall pre-filter (HasFunds=false).
				return multicallZeroResponse(n), nil
			}
			return multicallValueResponse(n, bal), nil
		}
		return make([]byte, 32), nil // zero ERC20 balance
	}
	return m
}

// multicallCallCount extracts the number of sub-calls from aggregate3 calldata.
// Layout: 4-byte selector + 32-byte outer offset + 32-byte array length + ...
func multicallCallCount(data []byte) int {
	if len(data) < 4+32+32 {
		return 0
	}
	return int(new(big.Int).SetBytes(data[4+32 : 4+32+32]).Uint64())
}

func TestSweepAll_AllZeroBalances(t *testing.T) {
	mock := fullMock()
	kp := newTestKey(t)

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSweepAll_ETHOnly(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)            // 1 gwei
	ethBalance := big.NewInt(1000000000000000000) // 1 ETH

	var txSent bool
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if txSent {
			return ethBalance, nil // return same balance for simplicity in final check
		}
		return ethBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		txSent = true
		// Verify it's going to the destination
		if tx.To() == nil || *tx.To() != destination {
			t.Fatalf("tx sent to wrong address: %v", tx.To())
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !txSent {
		t.Fatal("expected ETH transfer to be sent")
	}
}

func TestSweepAll_ERC20Only(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)
	token := common.HexToAddress("0xtoken")
	tokenBalance := big.NewInt(5000000)

	// The wallet has enough ETH for gas but not enough for a separate ETH sweep
	ethTransferCostWei := new(big.Int).Mul(gasPrice, big.NewInt(21000))
	walletETH := new(big.Int).Set(ethTransferCostWei) // exactly gas cost, not enough to sweep

	var erc20TxSent bool
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return walletETH, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, tokenBalance), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			tokenBalance.FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if *tx.To() == token {
			erc20TxSent = true
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !erc20TxSent {
		t.Fatal("expected ERC20 transfer to be sent")
	}
}

func TestSweepAll_MixedSweep(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)
	token := common.HexToAddress("0xtoken")
	tokenBalance := big.NewInt(5000000)
	ethBalance := big.NewInt(1000000000000000000)

	var erc20Sent, ethSent bool
	var sendOrder []string
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, tokenBalance), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			tokenBalance.FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if *tx.To() == token {
			erc20Sent = true
			sendOrder = append(sendOrder, "erc20")
		} else if *tx.To() == destination {
			ethSent = true
			sendOrder = append(sendOrder, "eth")
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !erc20Sent {
		t.Fatal("expected ERC20 transfer")
	}
	if !ethSent {
		t.Fatal("expected ETH transfer")
	}
	// ERC20 must be swept before ETH
	if len(sendOrder) < 2 || sendOrder[0] != "erc20" || sendOrder[1] != "eth" {
		t.Fatalf("expected ERC20 before ETH, got %v", sendOrder)
	}
}

func TestSweepAll_GasFunding(t *testing.T) {
	kp := newTestKey(t)
	gasSourceKP := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)
	token := common.HexToAddress("0xtoken")

	// Wallet has zero ETH, needs gas funding
	callCount := 0
	var funded bool
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == kp.Address {
			callCount++
			if callCount <= 1 {
				return big.NewInt(0), nil // no ETH initially
			}
			return big.NewInt(1000000000000000000), nil // funded
		}
		return big.NewInt(0), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if tx.To() != nil && *tx.To() == kp.Address {
			funded = true
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, &gasSourceKP, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !funded {
		t.Fatal("expected wallet to be funded")
	}
}

func TestSweepAll_NoGasSource(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)
	token := common.HexToAddress("0xtoken")

	// Wallet has zero ETH and no gas source, but has token balance
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(0), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)

	// Without gas source, it will still try to send the ERC20 transfer
	// (it doesn't check if there's enough gas — the tx will likely fail on-chain)
	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSweepAll_ETHBelowGasCost(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)

	// Wallet has some ETH but less than gas cost
	ethTransferCostWei := new(big.Int).Mul(gasPrice, big.NewInt(21000))
	tinyBalance := new(big.Int).Div(ethTransferCostWei, big.NewInt(2))

	var txSent bool
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return tinyBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		txSent = true
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txSent {
		t.Fatal("should not send tx when balance < gas cost")
	}
}

// TestSweepAll_OnlyConfiguredPaymentType verifies that an execution transacts
// solely on its own payment type, leaving the other chain untouched.
func TestSweepAll_OnlyConfiguredPaymentType(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	ethBalance := big.NewInt(1000000000000000000)

	var ethNetworkTx, zkNetworkTx bool
	ethMock := fullMock()
	ethMock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	ethMock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		ethNetworkTx = true
		return nil
	}

	zkMock := fullMock()
	zkMock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	zkMock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		zkNetworkTx = true
		return nil
	}

	sw := NewSweeper(zkMock, PaymentTypeL2, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !zkNetworkTx {
		t.Fatal("expected zkSync network transaction")
	}
	if ethNetworkTx {
		t.Fatal("an L2 sweep must not transact on L1")
	}
}

func TestSweepAll_ContextCanceled(t *testing.T) {
	kp := newTestKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := fullMock()
	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, time.Second, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(ctx, []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

func TestSweepAll_BalanceError(t *testing.T) {
	kp := newTestKey(t)
	ethBalance := big.NewInt(1000000000000000000)
	mock := fullMock()
	// Multicall reports ETH balance so sweepKey is entered, then balanceAtFn fails.
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, ethBalance), nil
		}
		return make([]byte, 32), nil
	}
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return nil, errors.New("rpc error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20BalanceError(t *testing.T) {
	kp := newTestKey(t)
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	// Multicall itself fails — this should surface as an error from SweepAll.
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		return nil, errors.New("call error")
	}

	token := common.HexToAddress("0xtoken")
	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_SendTransactionError(t *testing.T) {
	kp := newTestKey(t)
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		return errors.New("send error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_FailedReceipt(t *testing.T) {
	kp := newTestKey(t)
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.transactionReceiptFn = func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
		return &types.Receipt{Status: types.ReceiptStatusFailed}, nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error for failed receipt")
	}
}

func TestSweepAll_MultipleKeys(t *testing.T) {
	kp1 := newTestKey(t)
	kp2 := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	ethBalance := big.NewInt(1000000000000000000)

	addressesSeen := make(map[common.Address]bool)
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		addressesSeen[account] = true
		return ethBalance, nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !addressesSeen[kp1.Address] || !addressesSeen[kp2.Address] {
		t.Fatal("expected both keys to be processed")
	}
}

func TestSweepAll_LargestBalanceFirst(t *testing.T) {
	small := newTestKey(t)
	large := newTestKey(t)
	medium := newTestKey(t)
	keys := []KeyPair{small, large, medium}

	balances := map[common.Address]*big.Int{
		small.Address:  big.NewInt(1e18),
		large.Address:  big.NewInt(3e18),
		medium.Address: big.NewInt(2e18),
	}

	var order []common.Address
	seen := make(map[common.Address]bool)
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		balance, isWallet := balances[account]
		if !isWallet {
			return big.NewInt(0), nil // the destination, read by the final report
		}
		if !seen[account] {
			seen[account] = true
			order = append(order, account)
		}
		return balance, nil
	}
	// No tokens are configured, so the multicall holds exactly one ETH balance
	// sub-call per wallet, in the order the keys were passed in.
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		vals := make([]*big.Int, len(keys))
		for i, kp := range keys {
			vals[i] = balances[kp.Address]
		}
		return encodeAggregate3Response(vals), nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.HexToAddress("0xdead"), nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	if err := sw.SweepAll(context.Background(), keys); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []common.Address{large.Address, medium.Address, small.Address}
	if len(order) != len(want) {
		t.Fatalf("expected %d wallets swept, got %d", len(want), len(order))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("sweep order %d: got %s, want %s", i, order[i].Hex(), want[i].Hex())
		}
	}
}

func TestSweepAll_GasEstimateError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return nil, errors.New("gas price error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20SendError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if tx.To() != nil && *tx.To() == token {
			return errors.New("send error")
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20ChainIDError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.chainIDFn = func(ctx context.Context) (*big.Int, error) {
		return nil, errors.New("chain ID error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20NonceError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.pendingNonceAtFn = func(ctx context.Context, account common.Address) (uint64, error) {
		return 0, errors.New("nonce error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20GasPriceError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		callCount++
		if callCount <= 1 {
			return big.NewInt(1000000000), nil // first call for EstimateSweepGas
		}
		return nil, errors.New("gas price error") // second call in sendERC20Transfer
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20EstimateGasError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.estimateGasFn = func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
		callCount++
		if callCount <= 1 {
			return 60000, nil // first call: ERC20 estimate in EstimateSweepGas
		}
		return 0, errors.New("estimate error") // second call in sendERC20Transfer
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20ReceiptError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.transactionReceiptFn = func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
		return nil, errors.New("receipt error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ERC20FailedReceipt(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.transactionReceiptFn = func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
		return &types.Receipt{Status: types.ReceiptStatusFailed}, nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error for failed receipt")
	}
}

func TestSweepAll_ETHChainIDError(t *testing.T) {
	kp := newTestKey(t)
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.chainIDFn = func(ctx context.Context) (*big.Int, error) {
		return nil, errors.New("chain ID error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ETHNonceError(t *testing.T) {
	kp := newTestKey(t)
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.pendingNonceAtFn = func(ctx context.Context, account common.Address) (uint64, error) {
		return 0, errors.New("nonce error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ETHReceiptError(t *testing.T) {
	kp := newTestKey(t)
	gasPrice := big.NewInt(1000000000)

	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.transactionReceiptFn = func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
		return nil, errors.New("receipt error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_FinalBalanceCheckError(t *testing.T) {
	kp := newTestKey(t)
	gasPrice := big.NewInt(1000000000)

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		callCount++
		if callCount <= 1 {
			return big.NewInt(1000000000000000000), nil
		}
		return nil, errors.New("balance error")
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_FinalGasPriceError(t *testing.T) {
	kp := newTestKey(t)

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		callCount++
		if callCount <= 1 {
			return big.NewInt(1000000000), nil // for EstimateSweepGas
		}
		return nil, errors.New("gas price error") // for final ETH sweep
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewSweeper(t *testing.T) {
	mock := fullMock()
	dest := common.HexToAddress("0xdead")
	tokens := []common.Address{common.HexToAddress("0x01")}
	pk := newTestKey(t)
	logger := slog.Default()

	sw := NewSweeper(mock, PaymentTypeL2, dest, tokens, &pk, 500*time.Millisecond, 0, false, false, logger)
	if sw.destination != dest {
		t.Fatal("destination not set")
	}
	if sw.paymentType != PaymentTypeL2 {
		t.Fatal("paymentType not set")
	}
	if len(sw.tokens) != 1 {
		t.Fatal("tokens not set")
	}
	direct, ok := sw.strategy.(*directStrategy)
	if !ok {
		t.Fatalf("expected a direct strategy, got %T", sw.strategy)
	}
	if direct.gasSource == nil {
		t.Fatal("gasSource not set")
	}
	if sw.rateDelay != 500*time.Millisecond {
		t.Fatal("rateDelay not set")
	}
	if sw.maxFailures != 0 {
		t.Fatal("maxFailures not set")
	}
}

func TestSweepAll_ContinuesPastFailures(t *testing.T) {
	kp1 := newTestKey(t)
	kp1.LineNum = 1
	kp2 := newTestKey(t)
	kp2.LineNum = 2
	destination := common.HexToAddress("0xdead")
	ethBalance := big.NewInt(1000000000000000000)

	// Both wallets have ETH (multicall returns non-zero so sweepKey is called).
	// kp1 fails the balance re-check inside sweepKey; kp2 succeeds.
	var kp2Swept bool
	mock := fullMock()
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, ethBalance), nil
		}
		return make([]byte, 32), nil
	}
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == kp1.Address {
			return nil, errors.New("rpc error")
		}
		return ethBalance, nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if tx.To() != nil && *tx.To() == destination {
			kp2Swept = true
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2})
	// Should return error (there were failures) but kp2 should still be processed.
	if err == nil {
		t.Fatal("expected error due to kp1 failures")
	}
	if !kp2Swept {
		t.Fatal("expected kp2 to be swept despite kp1 failure")
	}
	if !strings.Contains(err.Error(), "failed to sweep") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSweepAll_CircuitBreaker(t *testing.T) {
	kp1 := newTestKey(t)
	kp1.LineNum = 1
	kp2 := newTestKey(t)
	kp2.LineNum = 2
	kp3 := newTestKey(t)
	kp3.LineNum = 3

	ethBalance := big.NewInt(1000000000000000000)

	// All wallets have ETH (multicall returns non-zero) but fail inside sweepKey.
	sweepCalls := 0
	mock := fullMock()
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, ethBalance), nil
		}
		return make([]byte, 32), nil
	}
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == (common.Address{}) {
			return big.NewInt(0), nil // the destination, read by the final report
		}
		sweepCalls++
		return nil, errors.New("rpc error")
	}

	// With maxFailures=2: kp1 fails (1), kp2 fails (2) → trips before kp3.
	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 2, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2, kp3})
	if err == nil {
		t.Fatal("expected error")
	}
	if sweepCalls != 2 {
		t.Fatalf("expected 2 sweepKey calls (circuit breaker at 2), got %d", sweepCalls)
	}
}

func TestSweepAll_FailureIncludesLineNum(t *testing.T) {
	kp := newTestKey(t)
	kp.LineNum = 42

	ethBalance := big.NewInt(1000000000000000000)
	mock := fullMock()
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, ethBalance), nil
		}
		return make([]byte, 32), nil
	}
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return nil, errors.New("rpc error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_UnlimitedFailures(t *testing.T) {
	// With maxFailures=0, all keys should be attempted regardless of failures.
	keys := make([]KeyPair, 10)
	for i := range keys {
		keys[i] = newTestKey(t)
		keys[i].LineNum = i + 1
	}

	ethBalance := big.NewInt(1000000000000000000)
	sweepCalls := 0
	mock := fullMock()
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == multicall3Address {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, ethBalance), nil
		}
		return make([]byte, 32), nil
	}
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		if account == (common.Address{}) {
			return big.NewInt(0), nil // the destination, read by the final report
		}
		sweepCalls++
		return nil, errors.New("rpc error")
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), keys)
	if err == nil {
		t.Fatal("expected error")
	}
	// Each key has ETH (per multicall), so every key is attempted once.
	if sweepCalls != 10 {
		t.Fatalf("expected 10 sweepKey calls (all keys), got %d", sweepCalls)
	}
}

func TestSweepAll_SkipETH(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	gasPrice := big.NewInt(1000000000)
	token := common.HexToAddress("0xtoken")
	tokenBalance := big.NewInt(5000000)
	ethBalance := big.NewInt(1000000000000000000) // 1 ETH

	var ethTxSent bool
	var erc20TxSent bool
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, tokenBalance), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			tokenBalance.FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if tx.To() != nil && *tx.To() == destination && len(tx.Data()) == 0 {
			ethTxSent = true
		}
		if tx.To() != nil && *tx.To() == token {
			erc20TxSent = true
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, true, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ethTxSent {
		t.Fatal("expected no ETH transfer when skipETH is true")
	}
	if !erc20TxSent {
		t.Fatal("expected ERC20 transfer to be sent even when skipETH is true")
	}
}

func TestNewSweeper_SkipETH(t *testing.T) {
	mock := fullMock()
	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, true, false, slog.New(slog.DiscardHandler))
	if !sw.skipETH {
		t.Fatal("skipETH not set")
	}
	// The balance query and the strategy both need it.
	if direct, ok := sw.strategy.(*directStrategy); !ok || !direct.skipETH {
		t.Fatal("skipETH not passed to the strategy")
	}
}

func TestSweepAll_ETHTransferIsDynamicFeeTx(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	baseFee := big.NewInt(18500000000)  // 18.5 gwei
	gasTipCap := big.NewInt(1500000000) // 1.5 gwei
	gasPrice := new(big.Int).Add(baseFee, gasTipCap)
	gasFeeCap := new(big.Int).Add(gasTipCap, new(big.Int).Mul(baseFee, big.NewInt(baseFeeWiggleMultiplier)))
	ethBalance := big.NewInt(1000000000000000000)

	var sentTx *types.Transaction
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.suggestGasTipCapFn = func(ctx context.Context) (*big.Int, error) {
		return gasTipCap, nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		sentTx = tx
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sentTx == nil {
		t.Fatal("no transaction sent")
	}
	if sentTx.Type() != types.DynamicFeeTxType {
		t.Fatalf("expected DynamicFeeTx (type 2), got type %d", sentTx.Type())
	}
	if sentTx.GasFeeCap().Cmp(gasFeeCap) != 0 {
		t.Fatalf("expected GasFeeCap %s, got %s", gasFeeCap.String(), sentTx.GasFeeCap().String())
	}
	if sentTx.GasTipCap().Cmp(gasTipCap) != 0 {
		t.Fatalf("expected GasTipCap %s, got %s", gasTipCap.String(), sentTx.GasTipCap().String())
	}
}

func TestSweepAll_ERC20TransferIsDynamicFeeTx(t *testing.T) {
	kp := newTestKey(t)
	destination := common.HexToAddress("0xdead")
	token := common.HexToAddress("0xtoken")
	baseFee := big.NewInt(18500000000)
	gasTipCap := big.NewInt(1500000000)
	gasPrice := new(big.Int).Add(baseFee, gasTipCap)
	gasFeeCap := new(big.Int).Add(gasTipCap, new(big.Int).Mul(baseFee, big.NewInt(baseFeeWiggleMultiplier)))
	tokenBalance := big.NewInt(5000000)
	ethBalance := big.NewInt(1000000000000000000)

	var erc20Tx *types.Transaction
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return ethBalance, nil
	}
	mock.suggestGasPriceFn = func(ctx context.Context) (*big.Int, error) {
		return gasPrice, nil
	}
	mock.suggestGasTipCapFn = func(ctx context.Context) (*big.Int, error) {
		return gasTipCap, nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, tokenBalance), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			tokenBalance.FillBytes(result)
			return result, nil
		},
	)
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		if tx.To() != nil && *tx.To() == token {
			erc20Tx = tx
		}
		return nil
	}

	sw := NewSweeper(mock, PaymentTypeL1, destination, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if erc20Tx == nil {
		t.Fatal("no ERC20 transaction sent")
	}
	if erc20Tx.Type() != types.DynamicFeeTxType {
		t.Fatalf("expected DynamicFeeTx (type 2), got type %d", erc20Tx.Type())
	}
	if erc20Tx.GasFeeCap().Cmp(gasFeeCap) != 0 {
		t.Fatalf("expected GasFeeCap %s, got %s", gasFeeCap.String(), erc20Tx.GasFeeCap().String())
	}
	if erc20Tx.GasTipCap().Cmp(gasTipCap) != 0 {
		t.Fatalf("expected GasTipCap %s, got %s", gasTipCap.String(), erc20Tx.GasTipCap().String())
	}
}

func TestSweepAll_ERC20GasTipCapError(t *testing.T) {
	kp := newTestKey(t)
	token := common.HexToAddress("0xtoken")

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.callContractFn = mockCallContract(
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			n := multicallCallCount(msg.Data)
			return multicallValueResponse(n, big.NewInt(100)), nil
		},
		func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			result := make([]byte, 32)
			big.NewInt(100).FillBytes(result)
			return result, nil
		},
	)
	mock.suggestGasTipCapFn = func(ctx context.Context) (*big.Int, error) {
		callCount++
		if callCount <= 3 {
			// queryBalances calls suggestGasFees once per network (eth+zk = 2 calls),
			// then EstimateSweepGas calls it once more — allow those through.
			return big.NewInt(1e9), nil
		}
		return nil, errors.New("tip cap error") // call in sendERC20Transfer
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, []common.Address{token}, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSweepAll_ETHGasTipCapError(t *testing.T) {
	kp := newTestKey(t)

	callCount := 0
	mock := fullMock()
	mock.balanceAtFn = func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
		return big.NewInt(1000000000000000000), nil
	}
	mock.suggestGasTipCapFn = func(ctx context.Context) (*big.Int, error) {
		callCount++
		if callCount <= 2 {
			// queryBalances calls suggestGasFees once per network (eth+zk = 2 calls)
			// before sweepKey is entered.
			return big.NewInt(1e9), nil
		}
		return nil, errors.New("tip cap error") // call in sweepKey ETH path
	}

	sw := NewSweeper(mock, PaymentTypeL1, common.Address{}, nil, nil, 0, 0, false, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}
