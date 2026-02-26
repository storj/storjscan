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
	gasPrice := big.NewInt(10000000000) // 10 gwei
	token1Gas := uint64(60000)
	token2Gas := uint64(80000)

	mock := &mockClient{
		suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
			return gasPrice, nil
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
	expectedCost := new(big.Int).Mul(new(big.Int).SetUint64(totalGas), gasPrice)
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
			return big.NewInt(20000000000), nil
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
	if sentTx.Nonce() != 5 {
		t.Fatalf("expected nonce 5, got %d", sentTx.Nonce())
	}
	if sentTx.Value().Cmp(amount) != 0 {
		t.Fatalf("expected amount %s, got %s", amount.String(), sentTx.Value().String())
	}
	if sentTx.Gas() != fundGas {
		t.Fatalf("expected gas %d, got %d", fundGas, sentTx.Gas())
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
