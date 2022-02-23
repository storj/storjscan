// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"math/big"
	"time"

	"github.com/spacemonkeygo/monkit/v3"

	"storj.io/storjscan/blockchain"
)

var mon = monkit.Package()

// Payment is on chain payment made for particular contract and deposit wallet.
type Payment struct {
	From        blockchain.Address
	TokenValue  *big.Int
	Transaction blockchain.Hash
	Timestamp   time.Time
}
