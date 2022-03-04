// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
)

var (
	// ErrNoHeader is error thrown when there is no header in db.
	ErrNoHeader = errs.New("HeadersDB: header not found")
	// ErrHeaderCache is headers cache error class.
	ErrHeaderCache = errs.Class("HeadersCache")

	mon = monkit.Package()
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
	// After retrieves block header which block timestamp is after provided time.
	After(ctx context.Context, t time.Time) (Header, error)
	// List retrieves all headers stored in cache db.
	List(ctx context.Context) ([]Header, error)
}

// HeadersCache is a blockchain block header cache.
type HeadersCache struct {
	log *zap.Logger
	db  HeadersDB
}

// NewHeadersCache creates new header cache.
func NewHeadersCache(log *zap.Logger, db HeadersDB) *HeadersCache {
	return &HeadersCache{
		log: log,
		db:  db,
	}
}

// Get retrieves block header from cache by its hash.
func (headersCache *HeadersCache) Get(ctx context.Context, hash Hash) (Header, bool, error) {
	header, err := headersCache.db.Get(ctx, hash)
	if err != nil {
		if errs.Is(err, ErrNoHeader) {
			return Header{}, false, nil
		}

		return Header{}, false, ErrHeaderCache.Wrap(err)
	}

	return header, true, nil
}

// HeaderSearch is block header search context.
type HeaderSearch struct {
	log         *zap.Logger
	db          HeadersDB
	endpoint    string
	blockTime   time.Duration
	threshold   time.Duration
	batchLimit  int64
	searchLimit int
}

// NewHeaderSearch creates new block header search context.
func NewHeaderSearch(log *zap.Logger, endpoint string, blockTime, threshold time.Duration, batchLimit int64, searchLimit int) *HeaderSearch {
	return &HeaderSearch{
		log:         log,
		endpoint:    endpoint,
		blockTime:   blockTime,
		threshold:   threshold,
		batchLimit:  batchLimit,
		searchLimit: searchLimit,
	}
}

// After finds first block after specified timestamp and returns its header.
func (headerSearch *HeaderSearch) After(ctx context.Context, t time.Time) (_ Header, err error) {
	defer mon.Task()(&ctx)(&err)

	c, err := rpc.DialContext(ctx, headerSearch.endpoint)
	if err != nil {
		return Header{}, ErrHeaderCache.Wrap(err)
	}
	defer c.Close()

	client := ethclient.NewClient(c)
	batchClient := newClient(c)

	startBlock, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return Header{}, ErrHeaderCache.Wrap(err)
	}

	blockTime := time.Unix(int64(startBlock.Time), 0)
	if t.After(blockTime) {
		return Header{}, ErrHeaderCache.New("requested time is after most recent block")
	}

	blockNumber := startBlock.Number.Int64()
	delta := blockTime.Sub(t)

	for i := 0; i < headerSearch.searchLimit; i++ {
		select {
		case <-ctx.Done():
			return Header{}, ctx.Err()
		default:
		}

		absDelta := delta
		if absDelta < 0 {
			absDelta = -delta
		}
		if absDelta < headerSearch.threshold {
			backwards := blockTime.After(t)

			var headers []Header
			if backwards {
				headers, err = batchClient.ListBackwards(ctx, blockNumber, headerSearch.batchLimit)
			} else {
				headers, err = batchClient.ListForward(ctx, blockNumber, headerSearch.batchLimit)
			}
			if err != nil {
				return Header{}, ErrHeaderCache.Wrap(err)
			}

			for j := 0; j < len(headers)-1; j++ {
				select {
				case <-ctx.Done():
					return Header{}, ctx.Err()
				default:
				}

				var next, curr Header
				if backwards {
					next, curr = headers[j], headers[j+1]
				} else {
					next, curr = headers[j+1], headers[j]
				}

				if t.After(curr.Timestamp) && (t.Before(next.Timestamp) || t.Equal(next.Timestamp)) {
					return next, nil
				}
			}
		}

		blockCount := int64(delta.Truncate(time.Second) / headerSearch.blockTime)
		nextBlockNumber := blockNumber - blockCount
		if nextBlockNumber < 0 {
			nextBlockNumber = 1
		}
		headerSearch.log.Debug("count", zap.Int64("c", blockCount))

		nextBlock, err := client.HeaderByNumber(ctx, new(big.Int).SetInt64(nextBlockNumber))
		if err != nil {
			return Header{}, ErrHeaderCache.Wrap(err)
		}

		blockNumber = nextBlock.Number.Int64()
		blockTime = time.Unix(int64(nextBlock.Time), 0)
		delta = blockTime.Sub(t)
	}

	return Header{}, ErrHeaderCache.New("reached binary search limit")
}
