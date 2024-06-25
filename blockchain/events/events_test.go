// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events_test

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/currency"
	"storj.io/common/testcontext"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

func TestEventsDBInsert(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		err := db.Events().Insert(ctx, []events.TransferEvent{
			{
				ChainID:     1337,
				BlockHash:   common.Hash{},
				BlockNumber: 100,
				TxHash:      common.Hash{},
				LogIndex:    10,
				From:        common.Address{},
				To:          common.Address{},
				TokenValue:  currency.AmountFromBaseUnits(100, currency.StorjToken),
			},
		})
		require.NoError(t, err)
	})
}

func TestEventsDBDelete(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName := "test-satellite"

		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)
		err = storjscandbtest.GenerateTestAddresses(ctx, service, satelliteName, 10)
		require.NoError(t, err)

		var testEvents []events.TransferEvent
		for i := 0; i < 10; i++ {
			b := make([]byte, common.HashLength)
			_, err := rand.Read(b)
			require.NoError(t, err)
			claimedWallet, err := db.Wallets().Claim(ctx, satelliteName)
			require.NoError(t, err)

			testEvents = append(testEvents, events.TransferEvent{
				ChainID:     1337,
				BlockHash:   common.HashFromBytes(b),
				BlockNumber: int64(i),
				TxHash:      common.Hash{},
				LogIndex:    10,
				From:        common.Address{},
				To:          claimedWallet.Address,
				TokenValue:  currency.AmountFromBaseUnits(100, currency.StorjToken),
			})
		}

		err = db.Events().Insert(ctx, testEvents)
		require.NoError(t, err)

		err = db.Events().DeleteBefore(ctx, 1337, 5)
		require.NoError(t, err)

		list, err := db.Events().GetBySatellite(ctx, 1337, satelliteName, 0)
		require.NoError(t, err)
		require.Equal(t, 5, len(list))

		err = db.Events().DeleteBlockAndAfter(ctx, 1337, 7)
		require.NoError(t, err)

		list, err = db.Events().GetBySatellite(ctx, 1337, satelliteName, 0)
		require.NoError(t, err)
		require.Equal(t, 2, len(list))
	})
}

func TestEventsDBGet(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName := "test-satellite"

		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)
		err = storjscandbtest.GenerateTestAddresses(ctx, service, satelliteName, 10)
		require.NoError(t, err)

		var testEvents []events.TransferEvent
		for i := 0; i < 10; i++ {
			b := make([]byte, common.HashLength)
			_, err := rand.Read(b)
			require.NoError(t, err)
			claimedWallet, err := db.Wallets().Claim(ctx, satelliteName)
			require.NoError(t, err)

			testEvents = append(testEvents, events.TransferEvent{
				ChainID:     1337,
				BlockHash:   common.HashFromBytes(b),
				BlockNumber: int64(i),
				TxHash:      common.Hash{},
				LogIndex:    10,
				From:        common.Address{},
				To:          claimedWallet.Address,
				TokenValue:  currency.AmountFromBaseUnits(100, currency.StorjToken),
			})
		}

		err = db.Events().Insert(ctx, testEvents)
		require.NoError(t, err)

		list, err := db.Events().GetBySatellite(ctx, 1337, satelliteName, 0)
		require.NoError(t, err)
		require.Equal(t, 10, len(list))
		list, err = db.Events().GetBySatellite(ctx, 1337, satelliteName, 5)
		require.NoError(t, err)
		require.Equal(t, 5, len(list))

		for _, expectedEvent := range list {
			event, err := db.Events().GetByAddress(ctx, 1337, expectedEvent.To, 0)
			require.NoError(t, err)
			require.Equal(t, []events.TransferEvent{expectedEvent}, event)
		}
	})
}

func TestGetBlockNumber(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName := "test-satellite"

		logger := zaptest.NewLogger(t)
		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)
		err = storjscandbtest.GenerateTestAddresses(ctx, service, satelliteName, 10)
		require.NoError(t, err)

		var testEvents []events.TransferEvent
		for i := 0; i < 10; i++ {
			b := make([]byte, common.HashLength)
			_, err := rand.Read(b)
			require.NoError(t, err)
			claimedWallet, err := db.Wallets().Claim(ctx, satelliteName)
			require.NoError(t, err)

			testEvents = append(testEvents, events.TransferEvent{
				ChainID:     1337,
				BlockHash:   common.HashFromBytes(b),
				BlockNumber: int64(i),
				TxHash:      common.Hash{},
				LogIndex:    10,
				From:        common.Address{},
				To:          claimedWallet.Address,
				TokenValue:  currency.AmountFromBaseUnits(100, currency.StorjToken),
			})
		}

		err = db.Events().Insert(ctx, testEvents)
		require.NoError(t, err)

		err = db.Events().DeleteBlockAndAfter(ctx, 1337, 7)
		require.NoError(t, err)

		blockNumber, err := db.Events().GetLatestCachedBlockNumber(ctx, 1337)
		require.NoError(t, err)
		require.Equal(t, uint64(6), blockNumber)

		err = db.Events().DeleteBefore(ctx, 1337, 3)
		require.NoError(t, err)

		blockNumber, err = db.Events().GetOldestCachedBlockNumber(ctx, 1337)
		require.NoError(t, err)
		require.Equal(t, uint64(3), blockNumber)
	})
}
