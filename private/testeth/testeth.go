// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"

	"storj.io/common/testcontext"
)

// Run creates Ethereum test network with deployed test token and executes test function.
func Run(t *testing.T, numNetworks, numAccounts int, test func(ctx *testcontext.Context, t *testing.T, network []*Network)) {
	t.Run("Ethereum", func(t *testing.T) {
		t.Parallel()
		ctx := testcontext.New(t)
		defer ctx.Cleanup()

		// node config
		nodeConfig := node.DefaultConfig
		nodeConfig.Name = "testeth"
		nodeConfig.DataDir = ""
		nodeConfig.HTTPHost = "127.0.0.1"
		nodeConfig.HTTPPort = 0
		nodeConfig.AuthPort = 0
		nodeConfig.HTTPModules = append(nodeConfig.HTTPModules, "eth")
		nodeConfig.P2P.MaxPeers = 0
		nodeConfig.P2P.ListenAddr = ""
		nodeConfig.P2P.NoDial = true
		nodeConfig.P2P.NoDiscovery = true
		nodeConfig.P2P.DiscoveryV5 = false

		var networks []*Network
		defer func() {
			for _, network := range networks {
				ctx.Check(network.Close)
			}
		}()
		for i := 0; i < numNetworks; i++ {
			// eth config
			ethConfig := ethconfig.Defaults
			ethConfig.NetworkId = 1337 + uint64(i)
			ethConfig.SyncMode = ethconfig.FullSync
			ethConfig.Miner.GasPrice = big.NewInt(params.GWei)
			ethConfig.FilterLogCacheSize = 100

			network, err := NewNetwork(nodeConfig, ethConfig, numAccounts)
			if err != nil {
				t.Fatal(err)
			}

			if err = network.Start(); err != nil {
				t.Fatal(err)
			}

			err = DeployToken(ctx, network, 1000000)
			if err != nil {
				t.Fatal(err)
			}
			networks = append(networks, network)
		}
		test(ctx, t, networks)
	})
}
