// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package common

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/zeebo/errs"

	"storj.io/common/currency"
)

// EthEndpoint contains the URL and contract address to access a chain API.
type EthEndpoint struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Contract string `json:"contract"`
	ChainID  int64  `json:"chainId,string,omitempty"`
	// Currency is the symbol of the token (e.g. STORJ, USDC).
	Currency string `json:"currency,omitempty"`
}

// TokenCurrency returns the currency of the token contract.
func (endpoint EthEndpoint) TokenCurrency() (*currency.Currency, error) {
	if endpoint.Currency == "" {
		return nil, errs.New("token currency is not configured for endpoint %q", endpoint.Name)
	}
	return currency.FromSymbol(endpoint.Currency)
}

// Address is wallet address on eth chain.
type Address = common.Address

// AddrLength is byte length of eth account address.
const AddrLength = 20

// ErrAddrLength represents the error that the address is the wrong length.
var ErrAddrLength = errs.Class("Address must be 20 bytes in length")

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

// HashLength is byte length of cryptographic hash.
const HashLength = 32

// HashFromBytes creates hash from byte slice.
func HashFromBytes(b []byte) Hash {
	return common.BytesToHash(b)
}
