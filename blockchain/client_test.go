// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain_test

import (
	"sort"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/private/testeth"
)

func TestBatchClientForward(t *testing.T) {
	testeth.Run(t, testeth.DisableDeployContract, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		chain := network.Ethereum().BlockChain()
		chainDB := network.Ethereum().ChainDb()
		wallet, err := network.EtherbaseWallet()
		require.NoError(t, err)

		genesisBlock := chain.CurrentBlock()

		blockGen := testeth.NewBlockGen(chain.Config(), wallet, chain, chain.Engine(), chainDB)
		blocks, err := blockGen.GenerateChain(ctx, genesisBlock.Header(), 1000, time.Minute)
		require.NoError(t, err)
		_, err = chain.InsertChain(blocks)
		require.NoError(t, err)

		client, err := blockchain.Dial(ctx, network.HTTPEndpoint())
		require.NoError(t, err)

		headers, err := client.ListForward(ctx, 0, 100)
		require.NoError(t, err)
		require.Equal(t, 100, len(headers))
		require.Equal(t, int64(0), headers[0].Number)
		require.Equal(t, int64(99), headers[len(headers)-1].Number)

		sorted := make([]blockchain.Header, len(headers))
		copy(sorted, headers)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Number < sorted[j].Number
		})
		require.Equal(t, sorted, headers)
	})
}

func TestBatchClientBackwards(t *testing.T) {
	testeth.Run(t, testeth.DisableDeployContract, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		chain := network.Ethereum().BlockChain()
		chainDB := network.Ethereum().ChainDb()
		wallet, err := network.EtherbaseWallet()
		require.NoError(t, err)

		genesisBlock := chain.CurrentBlock()

		blockGen := testeth.NewBlockGen(chain.Config(), wallet, chain, chain.Engine(), chainDB)
		blocks, err := blockGen.GenerateChain(ctx, genesisBlock.Header(), 1000, time.Minute)
		require.NoError(t, err)
		_, err = chain.InsertChain(blocks)
		require.NoError(t, err)

		client, err := blockchain.Dial(ctx, network.HTTPEndpoint())
		require.NoError(t, err)

		headers, err := client.ListBackwards(ctx, 1000, 100)
		require.NoError(t, err)
		require.Equal(t, 100, len(headers))
		require.Equal(t, int64(1000), headers[0].Number)
		require.Equal(t, int64(901), headers[len(headers)-1].Number)

		sorted := make([]blockchain.Header, len(headers))
		copy(sorted, headers)
		sort.Slice(sorted, func(i, j int) bool {
			return sorted[i].Number > sorted[j].Number
		})
		require.Equal(t, sorted, headers)
	})
}
