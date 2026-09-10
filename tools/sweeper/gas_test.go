// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestSuggestGasFees_BaseFeeHeadroom pins down the property that keeps sweep
// transactions minable: the fee cap must sit well above the current base fee.
// eth_gasPrice returns baseFee+tip, which leaves a transaction unminable as
// soon as the base fee ticks up (it rises up to 12.5% per block), stranding it
// in the mempool until the receipt wait times out.
func TestSuggestGasFees_BaseFeeHeadroom(t *testing.T) {
	baseFee := big.NewInt(18_500_000_000) // 18.5 gwei
	tip := big.NewInt(1_500_000_000)      // 1.5 gwei
	gasPrice := new(big.Int).Add(baseFee, tip)

	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return gasPrice, nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return tip, nil
		},
	}

	gasFeeCap, gasTipCap, err := suggestGasFees(context.Background(), mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gasTipCap.Cmp(tip) != 0 {
		t.Fatalf("expected gasTipCap %s, got %s", tip, gasTipCap)
	}

	// Same formula as go-ethereum's bind package: tip + 2*baseFee.
	want := new(big.Int).Add(tip, new(big.Int).Mul(baseFee, big.NewInt(2)))
	if gasFeeCap.Cmp(want) != 0 {
		t.Fatalf("expected gasFeeCap %s, got %s", want, gasFeeCap)
	}

	// The cap must survive several consecutive full blocks of base fee growth.
	future := new(big.Int).Set(baseFee)
	for range 6 {
		future.Mul(future, big.NewInt(1125))
		future.Div(future, big.NewInt(1000))
	}
	if gasFeeCap.Cmp(future) <= 0 {
		t.Fatalf("gasFeeCap %s does not cover base fee %s after 6 full blocks", gasFeeCap, future)
	}
}

// TestSuggestGasFees_NeverBelowSuggestion covers nodes whose eth_gasPrice is
// not baseFee+tip (pre-EIP-1559 or non-standard chains): the fee cap must never
// end up below what the node itself suggested.
func TestSuggestGasFees_NeverBelowSuggestion(t *testing.T) {
	gasPrice := big.NewInt(1_000_000_000)
	tip := big.NewInt(5_000_000_000) // tip above gasPrice: no base fee to derive

	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return gasPrice, nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return tip, nil
		},
	}

	gasFeeCap, gasTipCap, err := suggestGasFees(context.Background(), mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gasFeeCap.Cmp(gasPrice) < 0 {
		t.Fatalf("gasFeeCap %s below suggested gas price %s", gasFeeCap, gasPrice)
	}
	if gasFeeCap.Cmp(gasTipCap) < 0 {
		t.Fatalf("gasFeeCap %s below gasTipCap %s", gasFeeCap, gasTipCap)
	}
}

func TestEstimateSweepGas_ETHOnly(t *testing.T) {
	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(20000000000), nil
		},
	}

	// No tokens means no ERC20 gas to estimate; ETH sweeps pay their own gas.
	cost, err := EstimateSweepGas(context.Background(), mock, common.Address{}, common.Address{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cost.Sign() != 0 {
		t.Fatalf("expected zero cost with no tokens, got %s", cost.String())
	}
}

func TestEstimateSweepGas_WithTokens(t *testing.T) {
	baseFee := big.NewInt(9000000000) // 9 gwei
	tip := big.NewInt(1000000000)     // 1 gwei
	gasPrice := new(big.Int).Add(baseFee, tip)
	// Funding has to cover the fee cap the sweep transactions will actually
	// carry, which includes the base fee headroom.
	gasFeeCap := new(big.Int).Add(tip, new(big.Int).Mul(baseFee, big.NewInt(baseFeeWiggleMultiplier)))
	token1Gas := uint64(60000)
	token2Gas := uint64(80000)

	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return gasPrice, nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return tip, nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			if *msg.To == common.HexToAddress("0x01") {
				return token1Gas, nil
			}
			return token2Gas, nil
		},
	}

	tokens := []common.Address{
		common.HexToAddress("0x01"),
		common.HexToAddress("0x02"),
	}

	cost, err := EstimateSweepGas(context.Background(), mock, common.Address{}, common.Address{}, tokens)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	totalGas := token1Gas + token2Gas
	expectedCost := new(big.Int).Mul(new(big.Int).SetUint64(totalGas), gasFeeCap)
	expectedCost.Mul(expectedCost, big.NewInt(130))
	expectedCost.Div(expectedCost, big.NewInt(100))

	if cost.Cmp(expectedCost) != 0 {
		t.Fatalf("expected %s, got %s", expectedCost.String(), cost.String())
	}
}

func TestEstimateSweepGas_GasPriceError(t *testing.T) {
	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return nil, errors.New("rpc error")
		},
	}

	_, err := EstimateSweepGas(context.Background(), mock, common.Address{}, common.Address{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEstimateSweepGas_GasTipCapError(t *testing.T) {
	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return nil, errors.New("tip cap error")
		},
	}

	_, err := EstimateSweepGas(context.Background(), mock, common.Address{}, common.Address{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEstimateSweepGas_EstimateGasError(t *testing.T) {
	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return 0, errors.New("estimation failed")
		},
	}

	tokens := []common.Address{common.HexToAddress("0x01")}
	_, err := EstimateSweepGas(context.Background(), mock, common.Address{}, common.Address{}, tokens)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_Success(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{
		PrivateKey: pk,
		Address:    crypto.PubkeyToAddress(pk.PublicKey),
	}
	target := common.HexToAddress("0xaaaa")
	amount := big.NewInt(1000000)
	baseFee := big.NewInt(18500000000)  // 18.5 gwei
	gasTipCap := big.NewInt(1500000000) // 1.5 gwei
	gasPrice := new(big.Int).Add(baseFee, gasTipCap)
	gasFeeCap := new(big.Int).Add(gasTipCap, new(big.Int).Mul(baseFee, big.NewInt(baseFeeWiggleMultiplier)))

	fundGas := uint64(21000)
	var sentTx *types.Transaction
	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 5, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return gasPrice, nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return gasTipCap, nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return fundGas, nil
		},
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			sentTx = tx
			return nil
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{Status: types.ReceiptStatusSuccessful}, nil
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, target, amount)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if sentTx == nil {
		t.Fatal("no transaction sent")
	}
	if sentTx.Type() != types.DynamicFeeTxType {
		t.Fatalf("expected DynamicFeeTx (type 2), got type %d", sentTx.Type())
	}
	if sentTx.Nonce() != 5 {
		t.Fatalf("expected nonce 5, got %d", sentTx.Nonce())
	}
	if sentTx.Value().Cmp(amount) != 0 {
		t.Fatalf("expected amount %s, got %s", amount.String(), sentTx.Value().String())
	}
	if sentTx.Gas() != fundGas {
		t.Fatalf("expected gas %d, got %d", fundGas, sentTx.Gas())
	}
	if sentTx.GasFeeCap().Cmp(gasFeeCap) != 0 {
		t.Fatalf("expected GasFeeCap %s, got %s", gasFeeCap.String(), sentTx.GasFeeCap().String())
	}
	if sentTx.GasTipCap().Cmp(gasTipCap) != 0 {
		t.Fatalf("expected GasTipCap %s, got %s", gasTipCap.String(), sentTx.GasTipCap().String())
	}
}

func TestFundWallet_ChainIDError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return nil, errors.New("chain ID error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_NonceError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, errors.New("nonce error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_GasPriceError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return nil, errors.New("gas price error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_GasTipCapError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		suggestGasTipCapFn: func(ctx context.Context) (*big.Int, error) {
			return nil, errors.New("tip cap error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_SendError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return 21000, nil
		},
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return errors.New("send error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_ReceiptError(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return 21000, nil
		},
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return nil
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return nil, errors.New("receipt error")
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFundWallet_FailedReceipt(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	gasSource := &KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}

	mock := &mockClient{
		chainIDFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			return 0, nil
		},
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return big.NewInt(1), nil
		},
		estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
			return 21000, nil
		},
		sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
			return nil
		},
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
			return &types.Receipt{Status: types.ReceiptStatusFailed}, nil
		},
	}

	err := FundWallet(context.Background(), mock, gasSource, common.Address{}, big.NewInt(1))
	if err == nil {
		t.Fatal("expected error for failed receipt")
	}
}
