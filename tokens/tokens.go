// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
)

var mon = monkit.Package()

// Address represents address in ethereum network.
type Address = common.Address

// Hash represent cryptographic hash.
type Hash = common.Hash

// AddressFromHex creates new address from hex string.
func AddressFromHex(hex string) (Address, error) {
	if !common.IsHexAddress(hex) {
		return Address{}, errs.New("invalid address hex string")
	}
	return common.HexToAddress(hex), nil
}

// Payment is on chain payment made for particular contract and deposit wallet.
type Payment struct {
	From        Address
	TokenValue  *big.Int
	BlockHash   Hash
	BlockNumber int64
	Transaction Hash
	LogIndex    int
}
