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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, []common.Address{token}, nil, &gasSourceKP, 0, 0, false, slog.New(slog.DiscardHandler))
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
	sw := NewSweeper(mock, mock, destination, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txSent {
		t.Fatal("should not send tx when balance < gas cost")
	}
}

func TestSweepAll_BothNetworks(t *testing.T) {
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

	sw := NewSweeper(ethMock, zkMock, destination, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ethNetworkTx {
		t.Fatal("expected ETH network transaction")
	}
	if !zkNetworkTx {
		t.Fatal("expected zkSync network transaction")
	}
}

func TestSweepAll_ContextCanceled(t *testing.T) {
	kp := newTestKey(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mock := fullMock()
	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, time.Second, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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
	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, destination, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !addressesSeen[kp1.Address] || !addressesSeen[kp2.Address] {
		t.Fatal("expected both keys to be processed")
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNewSweeper(t *testing.T) {
	mock := fullMock()
	dest := common.HexToAddress("0xdead")
	ethTokens := []common.Address{common.HexToAddress("0x01")}
	zkTokens := []common.Address{common.HexToAddress("0x02")}
	pk := newTestKey(t)
	logger := slog.Default()

	sw := NewSweeper(mock, mock, dest, ethTokens, zkTokens, &pk, 500*time.Millisecond, 0, false, logger)
	if sw.destination != dest {
		t.Fatal("destination not set")
	}
	if len(sw.ethTokens) != 1 {
		t.Fatal("ethTokens not set")
	}
	if len(sw.zkTokens) != 1 {
		t.Fatal("zkTokens not set")
	}
	if sw.gasSource == nil {
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

	sw := NewSweeper(mock, mock, destination, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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
		sweepCalls++
		return nil, errors.New("rpc error")
	}

	// Circuit breaker at 2 failures — should stop before processing kp2/kp3.
	// With maxFailures=2: kp1 eth fails (1), kp1 zksync fails (2) → trips.
	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 2, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1, kp2, kp3})
	if err == nil {
		t.Fatal("expected error")
	}
	if sweepCalls != 2 {
		t.Fatalf("expected 2 sweepKey calls (circuit breaker at 2), got %d", sweepCalls)
	}
}

func TestSweepAll_CircuitBreakerMidKey(t *testing.T) {
	kp1 := newTestKey(t)

	ethBalance := big.NewInt(1000000000000000000)

	// maxFailures=1: ethereum sweepKey fails → trips immediately, zksync not attempted.
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
		sweepCalls++
		return nil, errors.New("rpc error")
	}

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 1, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), []KeyPair{kp1})
	if err == nil {
		t.Fatal("expected error")
	}
	if sweepCalls != 1 {
		t.Fatalf("expected 1 sweepKey call (circuit breaker at 1), got %d", sweepCalls)
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

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
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
		sweepCalls++
		return nil, errors.New("rpc error")
	}

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, false, slog.New(slog.DiscardHandler))
	err := sw.SweepAll(context.Background(), keys)
	if err == nil {
		t.Fatal("expected error")
	}
	// Each key has ETH (per multicall) and is attempted on both networks = 20 sweepKey calls.
	if sweepCalls != 20 {
		t.Fatalf("expected 20 sweepKey calls (all keys, both networks), got %d", sweepCalls)
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

	sw := NewSweeper(mock, mock, destination, []common.Address{token}, nil, nil, 0, 0, true, slog.New(slog.DiscardHandler))
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
	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, true, slog.New(slog.DiscardHandler))
	if !sw.skipETH {
		t.Fatal("skipETH not set")
	}
}
