// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestERC20TransferData_Encoding(t *testing.T) {
	to := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	amount := big.NewInt(1000000)

	data := ERC20TransferData(to, amount)

	if len(data) != 68 {
		t.Fatalf("expected 68 bytes, got %d", len(data))
	}

	// Check method ID
	if hex.EncodeToString(data[0:4]) != "a9059cbb" {
		t.Fatalf("wrong method ID: %s", hex.EncodeToString(data[0:4]))
	}

	// Check address is properly padded (12 zero bytes + 20 address bytes)
	for i := 4; i < 4+12; i++ {
		if data[i] != 0 {
			t.Fatalf("expected zero padding at byte %d, got %d", i, data[i])
		}
	}
	addrFromData := common.BytesToAddress(data[4+12 : 4+32])
	if addrFromData != to {
		t.Fatalf("address mismatch: expected %s, got %s", to.Hex(), addrFromData.Hex())
	}

	// Check amount
	amountFromData := new(big.Int).SetBytes(data[4+32:])
	if amountFromData.Cmp(amount) != 0 {
		t.Fatalf("amount mismatch: expected %s, got %s", amount.String(), amountFromData.String())
	}
}

func TestERC20TransferData_LargeAmount(t *testing.T) {
	to := common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	// Large uint256 value
	amount, _ := new(big.Int).SetString("115792089237316195423570985008687907853269984665640564039457584007913129639935", 10)

	data := ERC20TransferData(to, amount)

	if len(data) != 68 {
		t.Fatalf("expected 68 bytes, got %d", len(data))
	}

	amountFromData := new(big.Int).SetBytes(data[4+32:])
	if amountFromData.Cmp(amount) != 0 {
		t.Fatalf("amount mismatch: expected %s, got %s", amount.String(), amountFromData.String())
	}
}

func TestERC20TransferData_ZeroAmount(t *testing.T) {
	to := common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	amount := big.NewInt(0)

	data := ERC20TransferData(to, amount)
	amountFromData := new(big.Int).SetBytes(data[4+32:])
	if amountFromData.Sign() != 0 {
		t.Fatalf("expected zero amount, got %s", amountFromData.String())
	}
}

func TestERC20BalanceOf_Success(t *testing.T) {
	expectedBalance := big.NewInt(5000000)
	// Encode as 32-byte big-endian
	result := make([]byte, 32)
	expectedBalance.FillBytes(result)

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			// Verify the call data is correct
			if hex.EncodeToString(msg.Data[0:4]) != "70a08231" {
				t.Fatalf("wrong method ID: %s", hex.EncodeToString(msg.Data[0:4]))
			}
			return result, nil
		},
	}

	token := common.HexToAddress("0xdac17f958d2ee523a2206206994597c13d831ec7")
	account := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	balance, err := ERC20BalanceOf(context.Background(), mock, token, account)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance.Cmp(expectedBalance) != 0 {
		t.Fatalf("expected %s, got %s", expectedBalance.String(), balance.String())
	}
}

func TestERC20BalanceOf_ZeroBalance(t *testing.T) {
	result := make([]byte, 32)

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return result, nil
		},
	}

	balance, err := ERC20BalanceOf(context.Background(), mock, common.Address{}, common.Address{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance.Sign() != 0 {
		t.Fatalf("expected zero balance, got %s", balance.String())
	}
}

func TestERC20BalanceOf_CallError(t *testing.T) {
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return nil, errors.New("rpc error")
		},
	}

	_, err := ERC20BalanceOf(context.Background(), mock, common.Address{}, common.Address{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestERC20BalanceOf_ShortResponse(t *testing.T) {
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return []byte{0x01}, nil
		},
	}

	_, err := ERC20BalanceOf(context.Background(), mock, common.Address{}, common.Address{})
	if err == nil {
		t.Fatal("expected error for short response")
	}
}

func TestERC20BalanceOf_VerifiesTokenAddress(t *testing.T) {
	token := common.HexToAddress("0xdac17f958d2ee523a2206206994597c13d831ec7")
	result := make([]byte, 32)

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			if *msg.To != token {
				t.Fatalf("call sent to wrong address: %s", msg.To.Hex())
			}
			return result, nil
		},
	}

	_, err := ERC20BalanceOf(context.Background(), mock, token, common.Address{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestERC20BalanceOf_VerifiesAccountEncoding(t *testing.T) {
	account := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	result := make([]byte, 32)

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			// Extract address from calldata: bytes 4+12 to 4+32
			addrFromData := common.BytesToAddress(msg.Data[4+12 : 4+32])
			if addrFromData != account {
				t.Fatalf("account mismatch in calldata: expected %s, got %s", account.Hex(), addrFromData.Hex())
			}
			return result, nil
		},
	}

	_, err := ERC20BalanceOf(context.Background(), mock, common.Address{}, account)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ensure mockClient satisfies BlockchainClient (referenced from client_test.go).
var _ BlockchainClient = (*mockClient)(nil)

// Provide unused method stubs needed by tests in this file that use mockClient.
func newMockClientForERC20(callFn func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)) *mockClient {
	return &mockClient{
		callContractFn: callFn,
		balanceAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
			return nil, nil
		},
		pendingNonceAtFn:     func(ctx context.Context, account common.Address) (uint64, error) { return 0, nil },
		suggestGasPriceFn:    func(ctx context.Context) (*big.Int, error) { return nil, nil },
		estimateGasFn:        func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) { return 0, nil },
		sendTransactionFn:    func(ctx context.Context, tx *types.Transaction) error { return nil },
		chainIDFn:            func(ctx context.Context) (*big.Int, error) { return nil, nil },
		transactionReceiptFn: func(ctx context.Context, txHash common.Hash) (*types.Receipt, error) { return nil, nil },
	}
}
