// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/common"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

const testInfo string = "test-info"

func TestService(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName := "test-satellite"

		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)

		// test methods before any addresses are in the db
		wallet, err := service.Get(ctx, "eu1", common.Address{})
		require.Error(t, err)
		require.Nil(t, wallet)

		stats, err := service.GetStats(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, stats.TotalCount)
		require.Equal(t, 0, stats.UnclaimedCount)
		require.Equal(t, 0, stats.ClaimedCount)

		addr, err := service.Claim(ctx, "test")
		require.Error(t, err)
		require.Equal(t, common.Address{}, addr)

		// test happy path
		size := 2
		err = generateTestAddresses(ctx, service, satelliteName, size)
		require.NoError(t, err)

		stats, err = service.GetStats(ctx)
		require.NoError(t, err)
		require.Equal(t, size, stats.TotalCount)
		require.Equal(t, size, stats.UnclaimedCount)
		require.Equal(t, 0, stats.ClaimedCount)

		addr, err = service.Claim(ctx, satelliteName)
		require.NoError(t, err)
		require.NotEqual(t, "", addr)

		wallet, err = service.Get(ctx, satelliteName, addr)
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

		accts, err := service.ListBySatellite(ctx, satelliteName)
		require.NoError(t, err)
		require.Equal(t, 1, len(accts))
		info, ok := accts[addr]
		require.True(t, ok)
		require.NotNil(t, info)

		// test unexpected cases
		accts, err = service.ListBySatellite(ctx, "test-satellite-2")
		require.NoError(t, err)
		require.Equal(t, 0, len(accts))

		random, err := common.AddressFromHex("0xc1912fee45d61c87cc5ea59dae31190fffff232d")
		require.NoError(t, err)
		wallet, err = service.Get(ctx, "eu1", random)
		require.Error(t, err)
		require.Nil(t, wallet)

		addr, err = service.Claim(ctx, satelliteName)
		require.NoError(t, err)
		require.NotEqual(t, common.Address{}, addr)

		addr, err = service.Claim(ctx, satelliteName)
		require.Error(t, err)
		require.True(t, errs.Is(err, wallets.ErrNoAvailableWallets))
		require.Equal(t, common.Address{}, addr)
	})
}

func generateTestAddresses(ctx context.Context, service *wallets.Service, satellite string, count int) error {
	seed := make([]byte, 64)
	_, err := rand.Read(seed)
	if err != nil {
		return err
	}

	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return err
	}

	var inserts []wallets.InsertWallet
	next := accounts.DefaultIterator(accounts.DefaultBaseDerivationPath)
	for i := 0; i < count; i++ {
		account, err := derive(masterKey, next())
		if err != nil {
			return err
		}
		inserts = append(inserts, wallets.InsertWallet{
			Address: account.Address,
			Info:    "test-info",
		})
	}

	if len(inserts) < 1 {
		return errors.New("no addresses created")
	}

	err = service.Register(ctx, satellite, inserts)
	return err
}

func derive(masterKey *hdkeychain.ExtendedKey, path accounts.DerivationPath) (accounts.Account, error) {
	var err error
	key := masterKey
	for _, n := range path {
		key, err = key.Derive(n)
		if err != nil {
			return accounts.Account{}, errs.Wrap(err)
		}
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		return accounts.Account{}, errs.Wrap(err)
	}
	privateKeyECDSA := privateKey.ToECDSA()
	publicKey := privateKeyECDSA.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return accounts.Account{}, errs.New("failed to get public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return accounts.Account{
		Address: address,
		URL: accounts.URL{
			Scheme: "",
			Path:   path.String(),
		},
	}, nil
}

func TestListWallets(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName1 := "test-satellite-1"
		satelliteName2 := "test-satellite-2"
		size := 6

		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)

		// add the wallets to the DB, 6 wallets for each satellite
		err = generateTestAddresses(ctx, service, satelliteName1, size)
		require.NoError(t, err)
		err = generateTestAddresses(ctx, service, satelliteName2, size)
		require.NoError(t, err)

		// claim 1 wallet on satellite1 and 2 wallets on satellite2
		claimedWallet1, err := db.Wallets().Claim(ctx, satelliteName1)
		require.NoError(t, err)
		claimedWallet2A, err := db.Wallets().Claim(ctx, satelliteName2)
		require.NoError(t, err)
		claimedWallet2B, err := db.Wallets().Claim(ctx, satelliteName2)
		require.NoError(t, err)

		// random wallet address not in the DB
		random, err := common.AddressFromHex("0xc1912fee45d61c87cc5ea59dae31190fffff232d")
		require.NoError(t, err)

		// test list wallets for satellite1
		wallets1, err := service.ListBySatellite(ctx, satelliteName1)
		require.NoError(t, err)
		require.Equal(t, 1, len(wallets1))
		require.NotNil(t, wallets1[claimedWallet1.Address])

		// test list wallets for satellite2
		wallets2, err := service.ListBySatellite(ctx, satelliteName2)
		require.NoError(t, err)
		require.Equal(t, 2, len(wallets2))
		require.NotEmpty(t, wallets2[claimedWallet2A.Address])
		require.NotEmpty(t, wallets2[claimedWallet2B.Address])
		require.Empty(t, wallets2[random])

		// test list wallets for all satellites
		walletsAll, err := db.Wallets().ListAll(ctx)
		require.NoError(t, err)
		require.Equal(t, 3, len(walletsAll))
		require.NotEmpty(t, walletsAll[claimedWallet1.Address])
		require.NotEmpty(t, walletsAll[claimedWallet2A.Address])
		require.NotEmpty(t, walletsAll[claimedWallet2B.Address])
		require.Empty(t, walletsAll[random])
	})
}
