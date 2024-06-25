// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events

import (
	"context"

	"storj.io/common/currency"
	"storj.io/storjscan/common"
)

// TransferEvent holds a transfer event raised by an ERC20 contract.
type TransferEvent struct {
	ChainID     int64
	From        common.Address
	To          common.Address
	BlockHash   common.Hash
	BlockNumber int64
	TxHash      common.Hash
	LogIndex    int
	TokenValue  currency.Amount
}

// DB is an ERC20 contract transfer event cache.
//
// architecture: Database
type DB interface {
	// Insert inserts new transfer event to cache db.
	Insert(ctx context.Context, transferEvent []TransferEvent) error
	// GetBySatellite retrieves transfer events for satellite addresses on and after the given block number.
	GetBySatellite(ctx context.Context, chainID int64, satellite string, start uint64) ([]TransferEvent, error)
	// GetByAddress retrieves transfer events for the wallet address on and after the given block number.
	GetByAddress(ctx context.Context, chainID int64, to common.Address, start uint64) ([]TransferEvent, error)
	// GetLatestCachedBlockNumber retrieves the latest block number in the cache for the given chain.
	GetLatestCachedBlockNumber(ctx context.Context, chainID int64) (uint64, error)
	// GetOldestCachedBlockNumber retrieves the oldest block number in the cache for the given chain.
	GetOldestCachedBlockNumber(ctx context.Context, chainID int64) (uint64, error)
	// DeleteBefore deletes all transfer events before the given block number.
	DeleteBefore(ctx context.Context, chainID int64, before uint64) (err error)
	// DeleteBlockAndAfter deletes transfer events from the block number and after.
	DeleteBlockAndAfter(ctx context.Context, chainID int64, block uint64) (err error)
}
