// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth_test

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/private/testeth"
)

func TestBlockGen(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		chain := network.Ethereum().BlockChain()
		chainDB := network.Ethereum().ChainDb()
		wallet, err := network.EtherbaseWallet()
		require.NoError(t, err)

		require.NoError(t, chain.Reset())
		genesisBlock := chain.CurrentBlock()

		const count = 1000
		ts := time.Now()

		blockGen := testeth.NewBlockGen(chain.Config(), wallet, chain, chain.Engine(), chainDB)
		blocks, err := blockGen.GenerateChain(ctx, genesisBlock.Header(), count, time.Minute)
		require.NoError(t, err)
		require.Equal(t, count, len(blocks))

		n, err := chain.InsertChain(blocks)
		require.NoError(t, err)
		require.Equal(t, count, n)

		td := time.Now().Sub(ts)
		t.Log("Generate and insert chain duration:", td)

		newHead := chain.CurrentBlock().Header()
		require.Equal(t, blocks[len(blocks)-1].Hash(), newHead.Hash())
		require.Equal(t, int64(count), newHead.Number.Int64())

		t.Log("Lat block timestamp:", time.Unix(int64(newHead.Time), 0))
		t.Log("Lat block number:", newHead.Number.Int64())
	})
}
