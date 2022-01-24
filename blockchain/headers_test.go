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
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
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
			Timestamp: time.Now().UTC(),
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
		now := time.Now().Add(-time.Hour).UTC()
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

func TestHeadersCacheGet(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)
		cache := blockchain.NewHeadersCache(log, db.Headers())

		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)

		header := blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    1,
			Timestamp: time.Now().UTC(),
		}

		err = db.Headers().Insert(ctx, header.Hash, header.Number, header.Timestamp)
		require.NoError(t, err)

		dbHeader, ok, err := cache.Get(ctx, header.Hash)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, header.Hash, dbHeader.Hash)
		require.Equal(t, header.Number, dbHeader.Number)
		require.Equal(t, header.Timestamp, dbHeader.Timestamp)
	})
}

func TestHeadersCacheMissingHeader(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)
		cache := blockchain.NewHeadersCache(log, db.Headers())

		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)
		hash := blockchain.HashFromBytes(b)

		_, ok, err := cache.Get(ctx, hash)
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestHeaderSearchAfter(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)
		chain := network.Ethereum().BlockChain()
		chainDB := network.Ethereum().ChainDb()
		wallet, err := network.EtherbaseWallet()
		require.NoError(t, err)

		require.NoError(t, chain.Reset())
		genesisBlock := chain.CurrentBlock()
		s := time.Unix(int64(genesisBlock.Time()), 0)

		const blockTime = 17 * time.Second

		blockGen := testeth.NewBlockGen(chain.Config(), wallet, chain, chain.Engine(), chainDB)
		blocks, err := blockGen.GenerateChain(ctx, genesisBlock.Header(), 30000, blockTime)
		require.NoError(t, err)
		_, err = chain.InsertChain(blocks)
		require.NoError(t, err)

		search := blockchain.NewHeaderSearch(logger.Named("HeaderSearch"),
			network.HTTPEndpoint(),
			15*time.Second,
			5*time.Minute,
			30,
			10)

		target := s.Add(24 * time.Hour)

		header, err := search.After(ctx, target)
		require.NoError(t, err)
		require.True(t, header.Timestamp.After(target))
		require.True(t, header.Timestamp.Sub(target) < blockTime)
	})
}
