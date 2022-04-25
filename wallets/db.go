// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"
	"time"

	"github.com/zeebo/errs"

	"storj.io/storjscan/blockchain"
)

// ErrNoAvailableWallets represents the error that occurs when there are no deposit addresses that are unclaimed.
var ErrNoAvailableWallets = errs.New("no unclaimed wallets found")

// ErrUpdateWallet represents the error that occurs when the db cannot update the row that has a certain address.
var ErrUpdateWallet = errs.New("could not update wallet by address")

// Wallet represents an entry in the wallets table.
type Wallet struct {
	Address   blockchain.Address
	Claimed   time.Time
	Satellite string
	Info      string
	CreatedAt time.Time
}

// DB is a wallets database that stores deposit address information.
//
// architecture: Database
type DB interface {
	// Insert adds a new entry in the wallets table. Info can be an empty string.
	Insert(ctx context.Context, satellite string, address blockchain.Address, info string) (*Wallet, error)
	// InsertBatch adds a new db entry for each address. Entries is a string map of address:info.
	InsertBatch(ctx context.Context, satellite string, entries map[blockchain.Address]string) error
	// Claim claims and returns the first unclaimed wallet address.
	Claim(ctx context.Context, satellite string) (*Wallet, error)
	// Get returns the information stored for a given address.
	Get(ctx context.Context, satellite string, address blockchain.Address) (*Wallet, error)
	// GetStats returns information about the wallets table.
	GetStats(ctx context.Context) (*Stats, error)
	// ListBySatellite returns accounts claimed by a certain satellite.
	ListBySatellite(ctx context.Context, satellite string) (map[blockchain.Address]string, error)
}
