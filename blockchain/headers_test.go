// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain_test

import (
	"context"
	"crypto/rand"
	"math/big"
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
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
		cache := blockchain.NewHeadersCache(log, db.Headers(), "", 0, 0, 0, 0)

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
		cache := blockchain.NewHeadersCache(log, db.Headers(), "", 0, 0, 0, 0)

		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)
		hash := blockchain.HashFromBytes(b)

		_, ok, err := cache.Get(ctx, hash)
		require.NoError(t, err)
		require.False(t, ok)
	})
}

func TestHeadersCacheAfter(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)
		connStr := pgtest.PickPostgres(t)

		db, err := storjscandbtest.OpenDB(ctx, logger, connStr, t.Name(), "T")
		require.NoError(t, err)
		defer ctx.Check(db.Close)
		err = db.MigrateToLatest(ctx)
		require.NoError(t, err)

		chain := network.Ethereum().BlockChain()
		chainDB := network.Ethereum().ChainDb()
		wallet, err := network.EtherbaseWallet()
		require.NoError(t, err)

		require.NoError(t, chain.Reset())
		genesisBlock := chain.CurrentBlock()

		blockGen := testeth.NewBlockGen(chain.Config(), wallet, chain, chain.Engine(), chainDB)
		blocks, err := blockGen.GenerateChain(ctx, genesisBlock.Header(), 20000, 17*time.Second)
		require.NoError(t, err)
		_, err = chain.InsertChain(blocks)
		require.NoError(t, err)

		curr := chain.CurrentBlock()
		s := time.Unix(int64(genesisBlock.Time()), 0)
		e := time.Unix(int64(curr.Time()), 0)
		t.Log(s, e)

		cache := blockchain.NewHeadersCache(logger,
			db.Headers(),
			network.HTTPEndpoint(),
			15*time.Second,
			5*time.Minute,
			30,
			1)

		header, err := cache.After(ctx, s.Add(24*time.Hour))
		t.Log(header)
		require.NoError(t, err)
	})
}

func TestHeadersCache(t *testing.T) {
	ctx := context.Background()

	client, err := rpc.DialContext(ctx, "https://mainnet.infura.io/v3/ba396b0926614be1a934d9d49dd94a17")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	eth := ethclient.NewClient(client)

	start, err := eth.HeaderByNumber(ctx, new(big.Int).SetInt64(14128405))
	if err != nil {
		t.Fatal(err)
	}
	t.Log(start.Number.String())

	createBatch := func(start, count int64) ([]rpc.BatchElem, []*types.Header) {
		var batch []rpc.BatchElem
		var headers []*types.Header

		for i := start - 1; i >= start-count; i-- {
			blockNumber := new(big.Int).SetInt64(i)
			header := new(types.Header)

			batch = append(batch, rpc.BatchElem{
				Method: "eth_getBlockByNumber",
				Args:   []interface{}{hexutil.EncodeBig(blockNumber), false},
				Result: header,
			})
			headers = append(headers, header)
		}

		return batch, headers
	}

	var batchDur []time.Duration
	var durMetric []time.Duration

	execBatch := func(num, count int64) ([]*types.Header, error) {
		batch, headers := createBatch(num, count)

		st := time.Now()
		err = client.BatchCallContext(ctx, batch)
		if err != nil {
			return nil, err
		}
		et := time.Now()

		d := et.Sub(st)
		t.Log("dur", d)
		durMetric = append(durMetric, d)

		for _, elem := range batch {
			if elem.Error != nil {
				return nil, err
			}
		}

		return headers, nil
	}

	init := start.Number.Int64()
	initT := time.Unix(int64(start.Time), 0)
	num := init

	const count = 60

	for i := 0; i < 0; i++ {
		headers, err := execBatch(num, count)
		if err != nil {
			t.Fatal(err)
		}

		num = headers[len(headers)-1].Number.Int64()
		bt := time.Unix(int64(headers[len(headers)-1].Time), 0)
		bd := time.Unix(int64(headers[0].Time), 0).Sub(bt)
		batchDur = append(batchDur, bd)
		t.Log(num, bt)
		t.Log(bd)
		t.Log(bd / count)
		t.Log()
	}

	now := time.Now()
	now = now.Truncate(time.Hour)
	now = now.Add(-10 * time.Hour * 24)

	const blockTime = 15 * time.Second
	const threshold = 5 * time.Minute

	delta := initT.Sub(now)
	startBlockNumber := init
	bcount := int64(delta / blockTime)
	t.Log("count", bcount)
	t.Log("delta", delta)
	t.Log()

	for i := 0; i < 5; i++ {
		exp, err := eth.HeaderByNumber(ctx, new(big.Int).SetInt64(startBlockNumber-bcount))
		if err != nil {
			t.Fatal(err)
		}
		expT := time.Unix(int64(exp.Time), 0)

		if expT.After(now) {
			t.Log("after")
		} else {
			t.Log("before")
		}

		delta = expT.Sub(now)
		startBlockNumber = exp.Number.Int64()
		bcount = int64(delta / blockTime)

		t.Log(exp.Number)
		t.Log("count", bcount)
		t.Log("delta", delta)
		t.Log("target", now)
		t.Log("exp", expT)

		absDelta := delta
		if delta < 0 {
			absDelta = -delta
		}
		if absDelta < threshold {
			//t.Log("new delta", delta)
			t.Log("threshold reached")

			headers, err := execBatch(exp.Number.Int64(), count)
			if err != nil {
				t.Fatal(err)
			}

			for j := 0; j < len(headers); j++ {
				// skip last element
				if j == len(headers)-1 {
					break
				}

				currC := headers[j]
				prevT := headers[j+1]
				t.Log(prevT.Number, time.Unix(int64(prevT.Time), 0))
				t.Log(currC.Number, time.Unix(int64(currC.Time), 0))

				if int64(prevT.Time) < now.Unix() && now.Unix() <= int64(currC.Time) {
					t.Log("candidate found")
					t.Log(currC.Number, time.Unix(int64(currC.Time), 0))
					break
				} else {
					continue
				}
			}
			break
		}

		t.Log()
	}

	t.Log(durMetric)
	t.Log(batchDur)
	t.Log()
}
