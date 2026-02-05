// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
)

func TestWalletsUnlocked(t *testing.T) {
	testeth.Run(t, 1, 10, func(ctx *testcontext.Context, t *testing.T, networks []*testeth.Network) {
		network := networks[0]
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
	testeth.Run(t, 1, 1, func(ctx *testcontext.Context, t *testing.T, networks []*testeth.Network) {
		network := networks[0]
		client := network.Dial()
		defer client.Close()

		accounts := network.Accounts()

		tk, err := testtoken.NewTestToken(network.TokenAddress(), client)
		require.NoError(t, err)

		totalSupply, err := tk.TotalSupply(&bind.CallOpts{Context: ctx})
		require.NoError(t, err)
		balance, err := tk.BalanceOf(&bind.CallOpts{Context: ctx}, accounts[0].Address)
		require.NoError(t, err)

		require.Equal(t, totalSupply, balance)
	})
}

func TestTokenTransfer(t *testing.T) {
	testeth.Run(t, 1, 2, func(ctx *testcontext.Context, t *testing.T, networks []*testeth.Network) {
		network := networks[0]
		client := network.Dial()
		defer client.Close()

		accounts := network.Accounts()

		tk, err := testtoken.NewTestToken(network.TokenAddress(), client)
		require.NoError(t, err)

		totalSupply, err := tk.TotalSupply(&bind.CallOpts{Context: ctx})
		require.NoError(t, err)

		amount := totalSupply
		tx, err := tk.Transfer(network.TransactOptions(ctx, accounts[0], 1), accounts[1].Address, amount)
		require.NoError(t, err)

		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		balance, err := tk.BalanceOf(&bind.CallOpts{Context: ctx}, accounts[1].Address)
		require.NoError(t, err)

		require.Equal(t, totalSupply, balance)
	})
}

func TestTokenTransferMultipleNetworks(t *testing.T) {
	testeth.Run(t, 10, 2, func(ctx *testcontext.Context, t *testing.T, networks []*testeth.Network) {
		// connect to both networks
		var clients []*ethclient.Client
		defer func() {
			for _, client := range clients {
				client.Close()
			}
		}()
		for _, network := range networks {
			client := network.Dial()
			clients = append(clients, client)
		}

		// create test tokens for both networks
		var allAccounts [][]accounts.Account
		var totalSupplys []*big.Int
		var testTokens []*testtoken.TestToken
		for i, network := range networks {
			tk, err := testtoken.NewTestToken(network.TokenAddress(), clients[i])
			require.NoError(t, err)

			accounts := network.Accounts()
			totalSupply, err := tk.TotalSupply(&bind.CallOpts{Context: ctx})
			require.NoError(t, err)
			testTokens = append(testTokens, tk)
			allAccounts = append(allAccounts, accounts)
			totalSupplys = append(totalSupplys, totalSupply)
		}

		// transfer tokens and verify balance on each network
		for i, network := range networks {
			tx, err := testTokens[i].Transfer(network.TransactOptions(ctx, allAccounts[i][0], 1), allAccounts[i][1].Address, totalSupplys[i])
			require.NoError(t, err)

			_, err = network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)

			balance, err := testTokens[i].BalanceOf(&bind.CallOpts{Context: ctx}, allAccounts[i][1].Address)
			require.NoError(t, err)

			require.Equal(t, totalSupplys[i], balance)
		}
	})
}
