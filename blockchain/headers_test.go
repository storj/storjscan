// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain_test

import (
	"crypto/rand"
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/blockchain"
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
