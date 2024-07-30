// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"time"

	"github.com/spacemonkeygo/monkit/v3"

	"storj.io/common/currency"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/common"
)

var mon = monkit.Package()

// Payment is on chain payment made for particular contract and deposit wallet.
type Payment struct {
	ChainID     int64
	From        common.Address
	To          common.Address
	TokenValue  currency.Amount
	USDValue    currency.Amount
	BlockHash   common.Hash
	BlockNumber int64
	Transaction common.Hash
	LogIndex    int
	Timestamp   time.Time
}

// LatestPayments contains latest payments and latest chain block header.
type LatestPayments struct {
	LatestBlocks []blockchain.Header
	Payments     []Payment
}
