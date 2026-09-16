// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// keyAt builds a KeyPair with just the address set, which is all the ordering
// logic looks at.
func keyAt(hex string) KeyPair {
	return KeyPair{Address: common.HexToAddress(hex)}
}

func addresses(keys []KeyPair) []string {
	out := make([]string, len(keys))
	for i, kp := range keys {
		out[i] = kp.Address.Hex()
	}
	return out
}

func TestSortByAmountDesc_ETH(t *testing.T) {
	keys := []KeyPair{keyAt("0x01"), keyAt("0x02"), keyAt("0x03")}
	balances := []WalletBalances{
		{ETH: big.NewInt(5)},
		{ETH: big.NewInt(100)},
		{ETH: big.NewInt(50)},
	}

	sortedKeys, sortedBalances := sortByAmountDesc(keys, balances, nil)

	want := []string{keyAt("0x02").Address.Hex(), keyAt("0x03").Address.Hex(), keyAt("0x01").Address.Hex()}
	got := addresses(sortedKeys)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order mismatch: got %v, want %v", got, want)
		}
	}
	// Balances must stay parallel to the keys they belong to.
	for i, kp := range sortedKeys {
		var original WalletBalances
		for j, orig := range keys {
			if orig.Address == kp.Address {
				original = balances[j]
			}
		}
		if sortedBalances[i].ETH.Cmp(original.ETH) != 0 {
			t.Fatalf("balance %d not parallel to key: got %s, want %s", i, sortedBalances[i].ETH, original.ETH)
		}
	}
}

func TestSortByAmountDesc_TokensScaledByDecimals(t *testing.T) {
	// One USDC (6 decimals) outranks one thousandth of a token with 18
	// decimals, even though the raw integer is smaller.
	keys := []KeyPair{keyAt("0x01"), keyAt("0x02")}
	balances := []WalletBalances{
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(0), big.NewInt(1e15)}},
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(1e6), big.NewInt(0)}},
	}

	sortedKeys, _ := sortByAmountDesc(keys, balances, []int{6, 18})

	if sortedKeys[0].Address != keys[1].Address {
		t.Fatalf("expected the 1 USDC wallet first, got %v", addresses(sortedKeys))
	}
}

func TestSortByAmountDesc_SumsAllAssets(t *testing.T) {
	keys := []KeyPair{keyAt("0x01"), keyAt("0x02")}
	balances := []WalletBalances{
		// 10 of each token beats a single wallet holding 15 of one.
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(10), big.NewInt(10)}},
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(15), big.NewInt(0)}},
	}

	sortedKeys, _ := sortByAmountDesc(keys, balances, []int{18, 18})

	if sortedKeys[0].Address != keys[0].Address {
		t.Fatalf("expected the wallet with the larger total first, got %v", addresses(sortedKeys))
	}
}

func TestSortByAmountDesc_EqualAmountsOrderedByAddress(t *testing.T) {
	keys := []KeyPair{keyAt("0x22"), keyAt("0x11")}
	balances := []WalletBalances{
		{ETH: big.NewInt(7)},
		{ETH: big.NewInt(7)},
	}

	sortedKeys, _ := sortByAmountDesc(keys, balances, nil)

	if sortedKeys[0].Address != keys[1].Address {
		t.Fatalf("expected the lower address first on a tie, got %v", addresses(sortedKeys))
	}
}

func TestSortByAmountDesc_UnknownDecimalsUsesRawUnits(t *testing.T) {
	keys := []KeyPair{keyAt("0x01"), keyAt("0x02")}
	balances := []WalletBalances{
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(100)}},
		{ETH: new(big.Int), Tokens: []*big.Int{big.NewInt(200)}},
	}

	sortedKeys, _ := sortByAmountDesc(keys, balances, []int{UnknownDecimals})

	if sortedKeys[0].Address != keys[1].Address {
		t.Fatalf("expected the larger raw balance first, got %v", addresses(sortedKeys))
	}
}

func TestSortByAmountDesc_Empty(t *testing.T) {
	keys, balances := sortByAmountDesc(nil, nil, nil)
	if len(keys) != 0 || len(balances) != 0 {
		t.Fatalf("expected empty result, got %d keys and %d balances", len(keys), len(balances))
	}
}

func TestSortByAmountDesc_MissingBalancesAreDropped(t *testing.T) {
	keys := []KeyPair{keyAt("0x01"), keyAt("0x02")}
	balances := []WalletBalances{{ETH: big.NewInt(1)}}

	sortedKeys, sortedBalances := sortByAmountDesc(keys, balances, nil)

	if len(sortedKeys) != 1 || len(sortedBalances) != 1 {
		t.Fatalf("expected one key and one balance, got %d and %d", len(sortedKeys), len(sortedBalances))
	}
}

func TestScaleToOrderDecimals(t *testing.T) {
	tests := []struct {
		name    string
		balance int64
		dec     int
		want    string
	}{
		{"same scale", 5, 18, "5"},
		{"scaled up", 5, 6, "5000000000000"},
		{"scaled down", 5000, 21, "5"},
		{"unknown decimals", 5, UnknownDecimals, "5"},
		{"zero decimals", 2, 0, "2000000000000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scaleToOrderDecimals(big.NewInt(tt.balance), tt.dec)
			if got.String() != tt.want {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestScaleToOrderDecimals_DoesNotMutateInput(t *testing.T) {
	balance := big.NewInt(5)
	scaleToOrderDecimals(balance, 6)
	if balance.String() != "5" {
		t.Fatalf("input mutated: %s", balance)
	}
}
