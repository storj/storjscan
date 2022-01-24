// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"storj.io/common/testcontext"
)

// Run creates Ethereum test network with deployed test token and executes test function.
func Run(t *testing.T, test func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *Network)) {
	t.Run("Ethereum", func(t *testing.T) {
		//t.Parallel()
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

		tokenAddress, err := DeployToken(ctx, network, 1000000)
		if err != nil {
			t.Fatal(err)
		}

		test(ctx, t, tokenAddress, network)
	})
}
