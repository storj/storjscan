// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/zeebo/errs"
)

// Address is wallet address on eth chain.
type Address = common.Address

// AddrLength is byte length of eth account address.
const AddrLength = 20

// ErrAddrLength represents the error that the address is the wrong length.
var ErrAddrLength = errs.Class(fmt.Sprintf("Address must be %v bytes in length", AddrLength))

// AddressFromHex creates new address from hex string.
func AddressFromHex(hex string) (Address, error) {
	if !common.IsHexAddress(hex) {
		return Address{}, errs.New("invalid address hex string")
	}
	return common.HexToAddress(hex), nil
}

// AddressFromBytes creates a new address from hex bytes.
func AddressFromBytes(byteAddr []byte) (Address, error) {
	// sanity check that the address is the correct length
	length := len(byteAddr)
	addr := common.BytesToAddress(byteAddr)
	if length != AddrLength {
		return Address{}, ErrAddrLength.New("%v is %d bytes", addr, length)
	}
	return addr, nil
}

// Hash represent cryptographic hash.
type Hash = common.Hash

// HashFromBytes creates hash from byte slice.
func HashFromBytes(b []byte) Hash {
	return common.BytesToHash(b)
}
