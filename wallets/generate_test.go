// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"net"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/stretchr/testify/require"
	"github.com/tyler-smith/go-bip39"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/api"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

func TestGenerate(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		// setup environment
		logger := zaptest.NewLogger(t)

		service, err := wallets.NewService(logger, db.Wallets())
		require.NoError(t, err)

		endpoint := wallets.NewEndpoint(logger, service)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		apiServer := api.NewServer(logger, lis, map[string]string{"eu1": "secret"})
		apiServer.NewAPI("/wallets", endpoint.Register)
		ctx.Go(func() error {
			return apiServer.Run(ctx)
		})
		defer func() {
			err = apiServer.Close()
			require.NoError(t, err)
		}()

		// generate first time
		mnemonic := "leader pause fashion picnic green elder rebuild health valley alert cactus latin skull antique arrest skirt health chaos student will north garbage wagon before"

		addresses1, err := wallets.Generate(ctx, "defaultkey", 0, 5, mnemonic)
		require.NoError(t, err)
		client1 := wallets.NewClient("http://"+lis.Addr().String(), "eu1", "secret")
		err = client1.AddWallets(ctx, addresses1)
		require.NoError(t, err)

		addresses2, err := wallets.Generate(ctx, "defaultkey", 0, 10, mnemonic)
		require.NoError(t, err)
		client2 := wallets.NewClient("http://"+lis.Addr().String(), "eu1", "secret")
		err = client2.AddWallets(ctx, addresses2)
		require.NoError(t, err)

		// claim all of them
		for i := 0; i < 10; i++ {
			_, err := service.Claim(ctx, "eu1")
			require.NoError(t, err)
		}

		// get all the claimed addresses
		addressses, err := service.ListBySatellite(ctx, "eu1")
		require.NoError(t, err)

		// re-derive all the keys based on the info
		seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
		require.NoError(t, err)
		masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
		require.NoError(t, err)

		for a, i := range addressses {
			parts := strings.Split(i, " ")
			require.Len(t, parts, 2)

			dp, err := accounts.ParseDerivationPath(parts[1])
			require.NoError(t, err)
			derived, err := derive(masterKey, dp)
			require.NoError(t, err)

			require.Equal(t, derived.Address, a)
		}

	})
}
