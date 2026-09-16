// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"bytes"
	"math/big"
	"sort"
)

// orderDecimals is the common scale every asset is converted to before wallets
// are compared. It matches ETH's 18 decimals, so ETH balances need no scaling.
const orderDecimals = 18

// sortByAmountDesc returns keys, and the parallel balances, reordered so the
// wallets holding the most funds come first. Sweeping in that order puts the
// bulk of the funds at the destination early, so an interrupted run — a
// cancelled context, or the circuit breaker tripping on repeated failures —
// leaves behind the least value.
//
// Wallets are ranked by the sum of all their balances, each scaled from its own
// decimals to orderDecimals so that assets of different scale are comparable.
// This is a unit normalisation, not a valuation: the sweeper has no price feed,
// so one unit of a token counts the same as one ETH. decimals is parallel to
// tokens; a token with UnknownDecimals is counted in its raw contract units.
// Equal amounts are ordered by address to keep runs reproducible.
func sortByAmountDesc(keys []KeyPair, balances []WalletBalances, decimals []int) ([]KeyPair, []WalletBalances) {
	n := min(len(keys), len(balances))

	amounts := make([]*big.Int, n)
	order := make([]int, n)
	for i := range n {
		amounts[i] = walletAmount(balances[i], decimals)
		order[i] = i
	}

	sort.Slice(order, func(a, b int) bool {
		i, j := order[a], order[b]
		if c := amounts[i].Cmp(amounts[j]); c != 0 {
			return c > 0
		}
		return bytes.Compare(keys[i].Address.Bytes(), keys[j].Address.Bytes()) < 0
	})

	sortedKeys := make([]KeyPair, n)
	sortedBalances := make([]WalletBalances, n)
	for pos, i := range order {
		sortedKeys[pos] = keys[i]
		sortedBalances[pos] = balances[i]
	}
	return sortedKeys, sortedBalances
}

// walletAmount sums a wallet's balances on the orderDecimals scale. decimals is
// parallel to the token balances in wb.
func walletAmount(wb WalletBalances, decimals []int) *big.Int {
	total := new(big.Int)
	if wb.ETH != nil && wb.ETH.Sign() > 0 {
		total.Add(total, wb.ETH)
	}
	for i, balance := range wb.Tokens {
		if balance == nil || balance.Sign() <= 0 {
			continue
		}
		dec := UnknownDecimals
		if i < len(decimals) {
			dec = decimals[i]
		}
		total.Add(total, scaleToOrderDecimals(balance, dec))
	}
	return total
}

// scaleToOrderDecimals converts a balance held in a token using dec decimals to
// the orderDecimals scale. A token whose decimals are unknown is left in its
// raw units, which is all the sweeper can say about it.
func scaleToOrderDecimals(balance *big.Int, dec int) *big.Int {
	if dec == UnknownDecimals || dec == orderDecimals {
		return balance
	}
	if dec < orderDecimals {
		factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(orderDecimals-dec)), nil)
		return new(big.Int).Mul(balance, factor)
	}
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec-orderDecimals)), nil)
	return new(big.Int).Quo(balance, factor)
}
