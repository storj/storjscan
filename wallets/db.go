// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import "time"

// Wallet represents an entry in the wallets table.
type Wallet struct {
	Address string
	Claimed time.Time
}

// DB is a wallets database that stores deposit address information.
//
// architecture: Database
type DB interface{}
