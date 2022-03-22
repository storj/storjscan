// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"context"
	"time"

	"github.com/zeebo/errs"
)

var (
	// ErrNoHeader is error thrown when there is no header in db.
	ErrNoHeader = errs.New("HeadersDB: header not found")
)

// Header holds ethereum blockchain block header indexed data.
// No need to keep number as big.Int right now as block count on ethereum mainnet is far from overflowing int64 capacity.
type Header struct {
	Hash      Hash
	Number    int64
	Timestamp time.Time
}

// HeadersDB is ethereum blockchain block header indexed cache.
//
// architecture: Database
type HeadersDB interface {
	// Insert inserts new header to cache db.
	Insert(ctx context.Context, hash Hash, number int64, timestamp time.Time) error
	// Delete deletes header from db by hash.
	Delete(ctx context.Context, hash Hash) error
	// Get retrieves header by hash.
	Get(ctx context.Context, hash Hash) (Header, error)
	// GetByNumber retrieves header by number.
	GetByNumber(ctx context.Context, number int64) (Header, error)
	// List retrieves all headers stored in cache db.
	List(ctx context.Context) ([]Header, error)
}
