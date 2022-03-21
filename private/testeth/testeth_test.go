// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth_test

import (
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
)

func TestWalletsUnlocked(t *testing.T) {
	testeth.Run(t, testeth.DisableDeployContract, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		client := network.Dial()
		defer client.Close()

		am := network.Ethereum().AccountManager()

		wallets := am.Wallets()
		require.Equal(t, 10, len(wallets))

		for _, wallet := range am.Wallets() {
			status, err := wallet.Status()
			require.NoError(t, err)
			require.Equal(t, "Unlocked", status)
		}
	})
}

func TestTokenInitialSupply(t *testing.T) {
	testeth.Run(t, nil, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		client := network.Dial()
		defer client.Close()

		accounts := network.Accounts()

		tk, err := testtoken.NewTestToken(tokenAddress, client)
		require.NoError(t, err)

		totalSupply, err := tk.TotalSupply(&bind.CallOpts{Context: ctx})
		require.NoError(t, err)
		balance, err := tk.BalanceOf(&bind.CallOpts{Context: ctx}, accounts[0].Address)
		require.NoError(t, err)

		require.Equal(t, totalSupply, balance)
	})
}
