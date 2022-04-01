// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
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

// HeadersCache cache for blockchain block headers.
type HeadersCache struct {
	log *zap.Logger
	db  HeadersDB
}

// NewHeadersCache creates new headers cache.
func NewHeadersCache(log *zap.Logger, db HeadersDB) *HeadersCache {
	return &HeadersCache{
		log: log,
		db:  db,
	}
}

// Get retrieves block header from cache storage or fetches header from client and caches it.
// TODO: remove direct dependency on go-eth client from public API.
func (headersCache *HeadersCache) Get(ctx context.Context, client *ethclient.Client, hash Hash) (Header, error) {
	header, err := headersCache.db.Get(ctx, hash)
	switch {
	case err == nil:
		return header, nil
	case errs.Is(err, ErrNoHeader):
		ethHeader, err := client.HeaderByHash(ctx, hash)
		if err != nil {
			return Header{}, err
		}

		header := Header{
			Hash:      ethHeader.Hash(),
			Number:    ethHeader.Number.Int64(),
			Timestamp: time.Unix(int64(ethHeader.Time), 0).UTC(),
		}
		if err = headersCache.db.Insert(ctx, header.Hash, header.Number, header.Timestamp); err != nil {
			return Header{}, err
		}

		return header, nil
	default:
		return Header{}, err
	}
}
