// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"net"
	"strings"
	"testing"

	mm "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/stretchr/testify/require"
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

		err = wallets.Generate(ctx, wallets.GenerateConfig{
			Min:       0,
			Max:       5,
			Address:   "http://" + lis.Addr().String(),
			APIKey:    "eu1",
			APISecret: "secret",
		}, mnemonic)
		require.NoError(t, err)

		err = wallets.Generate(ctx, wallets.GenerateConfig{
			Min:       0,
			Max:       10,
			Address:   "http://" + lis.Addr().String(),
			APIKey:    "eu1",
			APISecret: "secret",
		}, mnemonic)
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
		seed, err := mm.NewSeedFromMnemonic(mnemonic)
		require.NoError(t, err)
		w, err := mm.NewFromSeed(seed)
		require.NoError(t, err)

		for a, i := range addressses {
			parts := strings.Split(i, " ")
			require.Len(t, parts, 2)

			dp, err := mm.ParseDerivationPath(parts[1])
			require.NoError(t, err)
			derived, err := w.Derive(dp, false)
			require.NoError(t, err)

			require.Equal(t, derived.Address, a)
		}

	})
}
