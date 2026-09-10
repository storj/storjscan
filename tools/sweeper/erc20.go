// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

var (
	// balanceOf(address) method ID
	balanceOfMethodID = [4]byte{0x70, 0xa0, 0x82, 0x31}
	// transfer(address,uint256) method ID
	transferMethodID = [4]byte{0xa9, 0x05, 0x9c, 0xbb}
	// decimals() method ID
	decimalsMethodID = [4]byte{0x31, 0x3c, 0xe5, 0x67}
)

// ERC20BalanceOf queries the ERC20 token balance for an account.
func ERC20BalanceOf(ctx context.Context, client BlockchainClient, token common.Address, account common.Address) (*big.Int, error) {
	data := make([]byte, 4+32)
	copy(data[0:4], balanceOfMethodID[:])
	copy(data[4+12:], account.Bytes())

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &token,
		Data: data,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("balanceOf call: %w", err)
	}

	if len(result) < 32 {
		return nil, fmt.Errorf("balanceOf: unexpected response length %d", len(result))
	}

	return new(big.Int).SetBytes(result[len(result)-32:]), nil
}

// ERC20Decimals queries the number of decimal places a token uses to display
// balances. decimals() is an optional part of the ERC20 standard, so callers
// must be prepared for an error from tokens that do not implement it.
func ERC20Decimals(ctx context.Context, client BlockchainClient, token common.Address) (int, error) {
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &token,
		Data: decimalsMethodID[:],
	}, nil)
	if err != nil {
		return 0, fmt.Errorf("decimals call: %w", err)
	}

	if len(result) < 32 {
		return 0, fmt.Errorf("decimals: unexpected response length %d", len(result))
	}

	value := new(big.Int).SetBytes(result[len(result)-32:])
	// The standard declares decimals() as uint8; anything larger is a
	// non-conforming contract we should not try to format balances with.
	if !value.IsInt64() || value.Int64() > 255 {
		return 0, fmt.Errorf("decimals: value %s out of uint8 range", value)
	}
	return int(value.Int64()), nil
}

// ERC20TransferData builds the calldata for an ERC20 transfer(address,uint256) call.
func ERC20TransferData(to common.Address, amount *big.Int) []byte {
	data := make([]byte, 4+32+32)
	copy(data[0:4], transferMethodID[:])
	copy(data[4+12:], to.Bytes())
	amount.FillBytes(data[4+32:])
	return data
}
