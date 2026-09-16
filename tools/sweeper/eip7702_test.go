// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestSweepCallData(t *testing.T) {
	token1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	data := SweepCallData([]common.Address{token1, token2})

	// Selector of sweep(address[]), computed independently of the code above.
	if got := hex.EncodeToString(data[:4]); got != "780469bb" {
		t.Fatalf("selector: got %s, want 780469bb", got)
	}
	if len(data) != 4+32+32+2*32 {
		t.Fatalf("unexpected calldata length %d", len(data))
	}
	if off := new(big.Int).SetBytes(data[4:36]).Uint64(); off != 32 {
		t.Fatalf("array offset: got %d, want 32", off)
	}
	if n := new(big.Int).SetBytes(data[36:68]).Uint64(); n != 2 {
		t.Fatalf("array length: got %d, want 2", n)
	}
	if got := common.BytesToAddress(data[68:100]); got != token1 {
		t.Fatalf("token 0: got %s, want %s", got.Hex(), token1.Hex())
	}
	if got := common.BytesToAddress(data[100:132]); got != token2 {
		t.Fatalf("token 1: got %s, want %s", got.Hex(), token2.Hex())
	}
}

func TestSweepCallData_NoTokens(t *testing.T) {
	data := SweepCallData(nil)
	if len(data) != 4+32+32 {
		t.Fatalf("unexpected calldata length %d", len(data))
	}
	if n := new(big.Int).SetBytes(data[36:68]).Uint64(); n != 0 {
		t.Fatalf("array length: got %d, want 0", n)
	}
}

func TestContractDestination(t *testing.T) {
	contract := common.HexToAddress("0xc0ffee")
	dest := common.HexToAddress("0xdeadbeef")

	var calledWith []byte
	mock := &mockClient{
		codeAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
			return []byte{0x60, 0x80}, nil
		},
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			calledWith = msg.Data
			result := make([]byte, 32)
			copy(result[12:], dest.Bytes())
			return result, nil
		},
	}

	got, err := ContractDestination(context.Background(), mock, contract)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != dest {
		t.Fatalf("destination: got %s, want %s", got.Hex(), dest.Hex())
	}
	if h := hex.EncodeToString(calledWith); h != "33808381" {
		t.Fatalf("DEST selector: got %s, want 33808381", h)
	}
}

func TestContractDestination_NoContract(t *testing.T) {
	mock := &mockClient{
		codeAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
			return nil, nil
		},
	}

	_, err := ContractDestination(context.Background(), mock, common.HexToAddress("0xc0ffee"))
	if err == nil || !strings.Contains(err.Error(), "no contract deployed") {
		t.Fatalf("expected a no-contract error, got %v", err)
	}
}

func TestDelegationTarget(t *testing.T) {
	contract := common.HexToAddress("0xc0ffee")

	tests := []struct {
		name          string
		code          []byte
		wantDelegated bool
		wantErr       string
	}{
		{name: "plain EOA", code: nil},
		{name: "delegated", code: types.AddressToDelegation(contract), wantDelegated: true},
		{name: "contract code", code: []byte{0x60, 0x80, 0x60, 0x40}, wantErr: "not an EIP-7702 delegation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockClient{
				codeAtFn: func(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
					return tt.code, nil
				},
			}
			target, delegated, err := DelegationTarget(context.Background(), mock, common.HexToAddress("0x1234"))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if delegated != tt.wantDelegated {
				t.Fatalf("delegated: got %v, want %v", delegated, tt.wantDelegated)
			}
			if delegated && target != contract {
				t.Fatalf("target: got %s, want %s", target.Hex(), contract.Hex())
			}
		})
	}
}

func TestSignDelegation(t *testing.T) {
	kp := newTestKey(t)
	contract := common.HexToAddress("0xc0ffee")

	mock := &mockClient{
		pendingNonceAtFn: func(ctx context.Context, account common.Address) (uint64, error) {
			if account != kp.Address {
				t.Fatalf("nonce requested for %s, want the wallet %s", account.Hex(), kp.Address.Hex())
			}
			return 7, nil
		},
	}

	auth, err := SignDelegation(context.Background(), mock, kp, big.NewInt(1), contract)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Nonce != 7 {
		t.Fatalf("nonce: got %d, want 7", auth.Nonce)
	}
	if auth.Address != contract {
		t.Fatalf("address: got %s, want %s", auth.Address.Hex(), contract.Hex())
	}
	authority, err := auth.Authority()
	if err != nil {
		t.Fatalf("recovering authority: %v", err)
	}
	if authority != kp.Address {
		t.Fatalf("authority: got %s, want the wallet %s", authority.Hex(), kp.Address.Hex())
	}
}

func TestEstimateDelegatedSweepGas(t *testing.T) {
	wallet := common.HexToAddress("0x1234")
	sponsor := common.HexToAddress("0x5678")

	tests := []struct {
		name     string
		estimate uint64
		auths    int
		tokens   int
		want     uint64
	}{
		// Floor wins: a node that ignored the authorization list would answer
		// with roughly the intrinsic cost only.
		{name: "estimate below floor", estimate: 21000, auths: 1, tokens: 1, want: 150_000},
		// 300000*1.3 + 25000 = 415000, above the 150000 floor.
		{name: "buffered estimate wins", estimate: 300_000, auths: 1, tokens: 1, want: 415_000},
		{name: "no auth, no token", estimate: 50_000, auths: 0, tokens: 0, want: 100_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockClient{
				estimateGasFn: func(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
					if len(msg.AuthorizationList) != tt.auths {
						t.Fatalf("authorization list: got %d, want %d", len(msg.AuthorizationList), tt.auths)
					}
					return tt.estimate, nil
				},
			}
			auths := make([]types.SetCodeAuthorization, tt.auths)
			got, err := EstimateDelegatedSweepGas(context.Background(), mock, sponsor, wallet, nil, auths, tt.tokens)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("gas limit: got %d, want %d", got, tt.want)
			}
		})
	}
}
