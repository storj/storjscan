// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"context"
	"math/big"

	"storj.io/storjscan/private/testeth/testtoken"
)

// DeployToken deploys test token to provided network using coinbase account.
func DeployToken(ctx context.Context, network *Network, supply int64) error {
	client := network.Dial()
	defer client.Close()

	nonce, err := client.PendingNonceAt(ctx, network.developer.Address)
	if err != nil {
		return err
	}

	s, d := big.NewInt(supply), new(big.Int)
	d.Exp(big.NewInt(10), big.NewInt(18), nil)
	s.Mul(s, d)

	addr, tx, _, err := testtoken.DeployTestToken(network.TransactOptions(ctx, network.developer, int64(nonce)), client, s)
	if err != nil {
		return err
	}

	_, err = network.WaitForTx(ctx, tx.Hash())
	if err != nil {
		return err
	}
	network.token = addr
	return nil
}
