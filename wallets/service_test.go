// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"context"
	"errors"
	"testing"

	acc "github.com/ethereum/go-ethereum/accounts"
	mm "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

const testInfo string = "test-info"

func TestService(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)

		// test methods before any addresses are in the db
		wallet, err := service.Get(ctx, blockchain.Address{})
		require.Error(t, err)
		require.Nil(t, wallet)

		stats, err := service.GetStats(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, stats.TotalCount)
		require.Equal(t, 0, stats.UnclaimedCount)
		require.Equal(t, 0, stats.ClaimedCount)

		addr, err := service.Claim(ctx, "test")
		require.Error(t, err)
		require.Equal(t, blockchain.Address{}, addr)

		// test happy path
		size := 2
		err = generateTestAddresses(ctx, service, size)
		require.NoError(t, err)

		stats, err = service.GetStats(ctx)
		require.NoError(t, err)
		require.Equal(t, size, stats.TotalCount)
		require.Equal(t, size, stats.UnclaimedCount)
		require.Equal(t, 0, stats.ClaimedCount)

		addr, err = service.Claim(ctx, "test-satellite")
		require.NoError(t, err)
		require.NotEqual(t, "", addr)

		wallet, err = service.Get(ctx, addr)
		require.NoError(t, err)
		require.NotNil(t, wallet.Address)
		require.NotNil(t, wallet.Claimed)
		require.Equal(t, "test-satellite", wallet.Satellite)
		require.Equal(t, testInfo, wallet.Info)
		require.NotNil(t, wallet.CreatedAt)

		stats, err = service.GetStats(ctx)
		require.NoError(t, err)
		require.Equal(t, size, stats.TotalCount)
		require.Equal(t, size-1, stats.UnclaimedCount)
		require.Equal(t, 1, stats.ClaimedCount)

		accts, err := service.ListBySatellite(ctx, "test-satellite")
		require.NoError(t, err)
		require.Equal(t, 1, len(accts))
		info, ok := accts[addr]
		require.True(t, ok)
		require.NotNil(t, info)

		// test unexpected cases
		accts, err = service.ListBySatellite(ctx, "test-satellite-2")
		require.NoError(t, err)
		require.Equal(t, 0, len(accts))

		random, err := blockchain.AddressFromHex("0xc1912fee45d61c87cc5ea59dae31190fffff232d")
		require.NoError(t, err)
		wallet, err = service.Get(ctx, random)
		require.Error(t, err)
		require.Nil(t, wallet)

		addr, err = service.Claim(ctx, "test-satellite")
		require.NoError(t, err)
		require.NotEqual(t, blockchain.Address{}, addr)

		addr, err = service.Claim(ctx, "test-satellite")
		require.Error(t, err)
		require.True(t, errs.Is(err, wallets.ErrNoAvailableWallets))
		require.Equal(t, blockchain.Address{}, addr)
	})
}

func generateTestAddresses(ctx context.Context, service *wallets.Service, count int) error {
	seed, err := mm.NewSeed()
	if err != nil {
		return err
	}

	w, err := mm.NewFromSeed(seed)
	if err != nil {
		return err
	}

	var entries = make(map[blockchain.Address]string)
	next := acc.DefaultIterator(mm.DefaultBaseDerivationPath)
	for i := 0; i < count; i++ {
		account, err := w.Derive(next(), false)
		if err != nil {
			continue
		}
		address, err := w.Address(account)
		if err != nil {
			continue
		}
		entries[address] = "test-info"
	}

	if len(entries) < 1 {
		return errors.New("no addresses created")
	}

	err = service.Register(ctx, entries)
	return err
}
