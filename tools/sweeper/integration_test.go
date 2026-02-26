package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// TestIntegration_FullSweepFlow simulates a complete end-to-end sweep across
// both Ethereum and zkSync networks with multiple keys, tokens, and gas funding.
func TestIntegration_FullSweepFlow(t *testing.T) {
	// Setup: Generate test keys and write them to a file.
	key1, _ := crypto.GenerateKey()
	key2, _ := crypto.GenerateKey()
	gasKey, _ := crypto.GenerateKey()

	keysFile := filepath.Join(t.TempDir(), "keys.txt")
	content := fmt.Sprintf("%x\n%x\n", crypto.FromECDSA(key1), crypto.FromECDSA(key2))
	if err := os.WriteFile(keysFile, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Load keys from file.
	keys, err := LoadKeys(keysFile)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}

	// Filter to first key only.
	filtered := FilterKeys(keys, []common.Address{keys[0].Address})
	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered key, got %d", len(filtered))
	}

	destination := common.HexToAddress("0xDeaDbeefdEAdbeefdEadbEEFdeadbeEFdEaDbeeF")
	gasSource := &KeyPair{PrivateKey: gasKey, Address: crypto.PubkeyToAddress(gasKey.PublicKey)}

	ethToken := common.HexToAddress("0x1111111111111111111111111111111111111111")
	zkToken := common.HexToAddress("0x2222222222222222222222222222222222222222")

	gasPrice := big.NewInt(1000000000)     // 1 gwei
	ethBalance := big.NewInt(500000000000) // small amount of ETH
	tokenBalanceAmt := big.NewInt(1000000)

	// Track what happens.
	var ethTxsSent, zkTxsSent int
	var fundingSent bool

	buildMock := func(network string, txCount *int) *mockClient {
		return &mockClient{
			balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
				if account == gasSource.Address {
					return big.NewInt(1000000000000000000), nil // gas source has plenty
				}
				if fundingSent {
					return big.NewInt(1000000000000000000), nil // funded
				}
				return ethBalance, nil
			},
			pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
				return 0, nil
			},
			suggestGasPriceFn: func(ctx context.Context) (*big.Int, error) {
				return gasPrice, nil
			},
			estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
				return 60000, nil
			},
			sendTransactionFn: func(ctx context.Context, tx *types.Transaction) error {
				*txCount++
				if tx.To() != nil && *tx.To() == filtered[0].Address {
					fundingSent = true
				}
				return nil
			},
			chainIDFn: func(ctx context.Context) (*big.Int, error) {
				if network == "eth" {
					return big.NewInt(1), nil
				}
				return big.NewInt(324), nil
			},
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				result := make([]byte, 32)
				tokenBalanceAmt.FillBytes(result)
				return result, nil
			},
			transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
				return &types.Receipt{Status: types.ReceiptStatusSuccessful}, nil
			},
		}
	}

	ethMock := buildMock("eth", &ethTxsSent)
	zkMock := buildMock("zk", &zkTxsSent)

	sw := NewSweeper(
		ethMock, zkMock,
		destination,
		[]common.Address{ethToken},
		[]common.Address{zkToken},
		gasSource,
		0,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)

	// Run the full sweep.
	if err := sw.SweepAll(context.Background(), filtered); err != nil {
		t.Fatalf("SweepAll: %v", err)
	}

	// Verify transactions were sent on both networks.
	if ethTxsSent == 0 {
		t.Fatal("expected transactions on Ethereum network")
	}
	if zkTxsSent == 0 {
		t.Fatal("expected transactions on zkSync network")
	}
	t.Logf("ETH txs: %d, zkSync txs: %d, funding sent: %v", ethTxsSent, zkTxsSent, fundingSent)
}

// TestIntegration_EmptyKeysFile verifies sweep with no keys is a no-op.
func TestIntegration_EmptyKeysFile(t *testing.T) {
	keysFile := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(keysFile, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	keys, err := LoadKeys(keysFile)
	if err != nil {
		t.Fatalf("LoadKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}

	mock := fullMock()
	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, slog.New(slog.DiscardHandler))
	if err := sw.SweepAll(context.Background(), keys); err != nil {
		t.Fatalf("SweepAll: %v", err)
	}
}

// TestIntegration_AllZeroBalances verifies sweep skips wallets with zero balances.
func TestIntegration_AllZeroBalances(t *testing.T) {
	key, _ := crypto.GenerateKey()
	kp := KeyPair{PrivateKey: key, Address: crypto.PubkeyToAddress(key.PublicKey)}

	var txSent bool
	mock := fullMock()
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		txSent = true
		return nil
	}

	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	sw := NewSweeper(mock, mock, common.Address{}, []common.Address{token}, []common.Address{token}, nil, 0, slog.New(slog.DiscardHandler))
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("SweepAll: %v", err)
	}
	if txSent {
		t.Fatal("expected no transactions for zero-balance wallets")
	}
}
