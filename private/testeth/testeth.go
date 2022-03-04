// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"storj.io/common/testcontext"
)

// Config holds testeth.Run configuration
type Config struct {
	DeployContract bool
}

// Reconfigure allows to change config values.
type Reconfigure func(config *Config)

// DisableDeployContract disables contract deployment.
func DisableDeployContract(config *Config) {
	config.DeployContract = false
}

// Run creates Ethereum test network with deployed test token and executes test function.
func Run(t *testing.T, reconfigure Reconfigure, test func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *Network)) {
	config := Config{
		DeployContract: true,
	}
	if reconfigure != nil {
		reconfigure(&config)
	}

	t.Run("Ethereum", func(t *testing.T) {
		ctx := testcontext.NewWithTimeout(t, 10*time.Minute)
		defer ctx.Cleanup()

		network, err := NewNetwork()
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(network.Close)

		if err = network.Start(); err != nil {
			t.Fatal(err)
		}

		var tokenAddress common.Address
		if config.DeployContract {
			tokenAddress, err = DeployToken(ctx, network, 1000000)
			if err != nil {
				t.Fatal(err)
			}
		}

		test(ctx, t, tokenAddress, network)
	})
}
