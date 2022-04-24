// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/zeebo/errs"
)

// Address is wallet address on eth chain.
type Address = common.Address

// AddressFromHex creates new address from hex string.
func AddressFromHex(hex string) (Address, error) {
	if !common.IsHexAddress(hex) {
		return Address{}, errs.New("invalid address hex string")
	}
	return common.HexToAddress(hex), nil
}

// AddressFromBytes creates a new address from hex bytes.
func AddressFromBytes(byteAddr []byte) Address {
	return common.BytesToAddress(byteAddr)
}

// Hash represent cryptographic hash.
type Hash = common.Hash

// HashFromBytes creates hash from byte slice.
func HashFromBytes(b []byte) Hash {
	return common.BytesToHash(b)
}
