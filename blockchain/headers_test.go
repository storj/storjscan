// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain_test

import (
	"crypto/rand"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/common/testrand"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/storjscandb/storjscandbtest"
)

func TestHeadersDBInsert(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		err := db.Headers().Insert(ctx, blockchain.Hash{}, 0, time.Now().UTC())
		require.NoError(t, err)
	})
}

func TestHeadersDBDelete(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)

		header := blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    0,
			Timestamp: time.Now().UTC(),
		}

		err = db.Headers().Insert(ctx, header.Hash, header.Number, header.Timestamp)
		require.NoError(t, err)

		err = db.Headers().Delete(ctx, header.Hash)
		require.NoError(t, err)
	})
}

func TestHeadersDBGet(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)

		header := blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    1,
			Timestamp: time.Now().Round(time.Microsecond).UTC(),
		}

		err = db.Headers().Insert(ctx, header.Hash, header.Number, header.Timestamp)
		require.NoError(t, err)

		t.Run("Get by hash", func(t *testing.T) {
			dbHeader, err := db.Headers().Get(ctx, header.Hash)
			require.NoError(t, err)
			require.Equal(t, header.Hash, dbHeader.Hash)
			require.Equal(t, header.Number, dbHeader.Number)
			require.Equal(t, header.Timestamp, dbHeader.Timestamp)
		})
		t.Run("Get by number", func(t *testing.T) {
			dbHeader, err := db.Headers().GetByNumber(ctx, header.Number)
			require.NoError(t, err)
			require.Equal(t, header.Hash, dbHeader.Hash)
			require.Equal(t, header.Number, dbHeader.Number)
			require.Equal(t, header.Timestamp, dbHeader.Timestamp)
		})
	})
}

func TestHeadersDBList(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		now := time.Now().Round(time.Microsecond).Add(-time.Hour).UTC()
		var headers []blockchain.Header

		// create block headers.
		for i := int64(0); i < 10; i++ {
			b := make([]byte, common.HashLength)
			_, err := rand.Read(b)
			require.NoError(t, err)

			header := blockchain.Header{
				Hash:      blockchain.HashFromBytes(b),
				Number:    i,
				Timestamp: now.Add(time.Duration(i) * time.Minute),
			}
			headers = append(headers, header)
		}
		// insert headers into db.
		for _, header := range headers {
			err := db.Headers().Insert(ctx, header.Hash, header.Number, header.Timestamp)
			require.NoError(t, err)
		}

		list, err := db.Headers().List(ctx)
		require.NoError(t, err)
		require.Equal(t, len(headers), len(list))

		sort.Slice(headers, func(i, j int) bool {
			return headers[i].Timestamp.After(headers[j].Timestamp)
		})
		for i, header := range headers {
			require.Equal(t, header.Hash, list[i].Hash)
			require.Equal(t, header.Number, list[i].Number)
			require.Equal(t, header.Timestamp, list[i].Timestamp)
		}
	})
}

func TestHeadersCache(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		logger := zaptest.NewLogger(t)
		now := time.Now().Round(time.Microsecond).UTC()

		var hash blockchain.Hash
		b := testrand.BytesInt(32)
		copy(hash[:], b)

		err := db.Headers().Insert(ctx, hash, 1, now)
		require.NoError(t, err)

		cache := blockchain.NewHeadersCache(logger, db.Headers())
		header, err := cache.Get(ctx, &ethclient.Client{}, hash)
		require.NoError(t, err)
		require.Equal(t, hash, header.Hash)
		require.EqualValues(t, 1, header.Number)
		require.Equal(t, now, header.Timestamp)
	})
}

func TestHeadersCacheMissingHeader(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testHeadersCacheMissingHeader(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testHeadersCacheMissingHeader(t, pgtest.PickCockroach(t))
	})
}

func testHeadersCacheMissingHeader(t *testing.T, connStr string) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		db, err := storjscandbtest.OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		client := network.Dial()
		defer client.Close()

		fullHeader, err := client.HeaderByNumber(ctx, new(big.Int).SetInt64(1))
		require.NoError(t, err)
		hash := fullHeader.Hash()
		headerTime := time.Unix(int64(fullHeader.Time), 0).UTC()

		cache := blockchain.NewHeadersCache(logger, db.Headers())
		header, err := cache.Get(ctx, client, hash)
		require.NoError(t, err)
		require.Equal(t, hash, header.Hash)
		require.EqualValues(t, 1, header.Number)
		require.Equal(t, headerTime, header.Timestamp)

		// check that header was written to db
		header, err = db.Headers().Get(ctx, hash)
		require.NoError(t, err)
		require.Equal(t, hash, header.Hash)
		require.EqualValues(t, 1, header.Number)
		require.Equal(t, headerTime, header.Timestamp)
	})
}
