// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/common"
)

var (
	// ErrNoHeader is error thrown when there is no header in db.
	ErrNoHeader = errs.New("HeadersDB: header not found")
)

// Header holds ethereum blockchain block header indexed data.
// No need to keep number as big.Int right now as block count on ethereum mainnet is far from overflowing int64 capacity.
type Header struct {
	ChainID   uint64
	Hash      common.Hash
	Number    uint64
	Timestamp time.Time
}

// HeadersDB is ethereum blockchain block header indexed cache.
//
// architecture: Database
type HeadersDB interface {
	// Insert inserts new header to cache db.
	Insert(ctx context.Context, header Header) error
	// Delete deletes header from db by hash.
	Delete(ctx context.Context, ChainID uint64, hash common.Hash) error
	// DeleteBefore deletes headers before the given time.
	DeleteBefore(ctx context.Context, before time.Time) (err error)
	// Get retrieves header by hash.
	Get(ctx context.Context, ChainID uint64, hash common.Hash) (Header, error)
	// GetByNumber retrieves header by number.
	GetByNumber(ctx context.Context, ChainID uint64, number uint64) (Header, error)
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
func (headersCache *HeadersCache) Get(ctx context.Context, client *ethclient.Client, chainID uint64, hash common.Hash) (Header, error) {
	headersCache.log.Debug("fetching header", zap.Uint64("Chain ID", chainID), zap.String("hash", hash.String()))
	header, err := headersCache.db.Get(ctx, chainID, hash)
	switch {
	case err == nil:
		return header, nil
	case errs.Is(err, ErrNoHeader):
		ethHeader, err := client.HeaderByHash(ctx, hash)
		if err != nil {
			return Header{}, err
		}

		header := Header{
			// Note: we are using the provided hash here instead of what was fetched/computed by geth. This is done
			// to allow support for additional blockchains that do not adhere strictly to the Ethereum block header
			// format.
			Hash:      hash,
			ChainID:   chainID,
			Number:    ethHeader.Number.Uint64(),
			Timestamp: time.Unix(int64(ethHeader.Time), 0).UTC(),
		}
		if chainID == 1 && ethHeader.Hash() != hash {
			headersCache.log.Warn("ethereum header hash mismatch! geth library may be out of date.", zap.String("geth hash", ethHeader.Hash().String()), zap.String("node hash", hash.String()))
		}
		headersCache.log.Debug("header not found: inserting new header", zap.Uint64("Chain ID", header.ChainID), zap.String("hash", header.Hash.String()))
		if err = headersCache.db.Insert(ctx, header); err != nil {
			return Header{}, err
		}

		return header, nil
	default:
		return Header{}, err
	}
}
