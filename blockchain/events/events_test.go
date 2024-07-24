// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events_test

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/currency"
	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
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
				BlockNumber: uint64(i),
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
				BlockNumber: uint64(i),
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
				BlockNumber: uint64(i),
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

func TestEventsCache(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testEventsCache(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testEventsCache(t, pgtest.PickCockroach(t))
	})
}

func testEventsCache(t *testing.T, connStr string) {
	testeth.Run(t, 1, 5, func(ctx *testcontext.Context, t *testing.T, networks []*testeth.Network) {
		logger := zaptest.NewLogger(t)
		network := networks[0]
		satelliteName := "test-satellite"

		db, err := storjscandbtest.OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		jsonEndpoint := `[{"Name":"Geth", "URL": "` + network.HTTPEndpoint() + `", "Contract": "` + network.TokenAddress().Hex() + `", "ChainID": "` + fmt.Sprint(network.ChainID()) + `"}]`
		var ethEndpoints []common.EthEndpoint
		err = json.Unmarshal([]byte(jsonEndpoint), &ethEndpoints)
		require.NoError(t, err)

		client := network.Dial()
		defer client.Close()

		tk, err := testtoken.NewTestToken(network.TokenAddress(), client)
		require.NoError(t, err)

		accs := network.Accounts()

		opts := network.TransactOptions(ctx, accs[0], 1)
		tx, err := tk.Transfer(opts, accs[1].Address, big.NewInt(1000000))
		require.NoError(t, err)
		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		opts = network.TransactOptions(ctx, accs[0], 2)
		tx, err = tk.Transfer(opts, accs[2].Address, big.NewInt(1000000))
		require.NoError(t, err)
		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		testPayments := []struct {
			Amount      int64
			From        accounts.Account
			To          accounts.Account
			BlockHash   common.Hash
			BlockNumber int64
			Tx          common.Hash
			LogIndex    int
		}{
			{Amount: 10000, From: accs[0], To: accs[3]},
			{Amount: 10000, From: accs[1], To: accs[3]},
			{Amount: 10000, From: accs[2], To: accs[3]},
			{Amount: 1000, From: accs[2], To: accs[3]},
			{Amount: 2000, From: accs[1], To: accs[3]},
			{Amount: 3000, From: accs[0], To: accs[3]},
			{Amount: 1000, From: accs[2], To: accs[4]},
			{Amount: 2000, From: accs[1], To: accs[4]},
			{Amount: 3000, From: accs[0], To: accs[4]},
		}
		for i, testPayment := range testPayments {
			nonce, err := client.PendingNonceAt(ctx, testPayment.From.Address)
			require.NoError(t, err)

			opts = network.TransactOptions(ctx, testPayment.From, int64(nonce))
			tx, err = tk.Transfer(opts, testPayment.To.Address, big.NewInt(testPayment.Amount))
			require.NoError(t, err)

			recpt, err := network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)
			testPayments[i].BlockHash = recpt.BlockHash
			testPayments[i].BlockNumber = recpt.BlockNumber.Int64()
			testPayments[i].Tx = tx.Hash()
			testPayments[i].LogIndex = 0
		}

		// add the wallets to the DB
		insertedWallet, err := db.Wallets().Insert(ctx, satelliteName, accs[3].Address, "")
		require.NoError(t, err)
		claimedWallet, err := db.Wallets().Claim(ctx, satelliteName)
		require.NoError(t, err)
		require.Equal(t, insertedWallet.Address, claimedWallet.Address)
		require.Equal(t, claimedWallet.Address, accs[3].Address)
		insertedWallet, err = db.Wallets().Insert(ctx, satelliteName, accs[4].Address, "")
		require.NoError(t, err)
		claimedWallet, err = db.Wallets().Claim(ctx, satelliteName)
		require.NoError(t, err)
		require.Equal(t, insertedWallet.Address, claimedWallet.Address)
		require.Equal(t, claimedWallet.Address, accs[4].Address)

		eventsCache := events.NewEventsCache(logger, db.Events(), db.Wallets(), events.Config{
			CacheRefreshInterval: 10,
			AddressBatchSize:     100,
			BlockBatchSize:       100,
			ChainReorgBuffer:     15,
			MaximumQuerySize:     10000,
		})

		eventsList, err := eventsCache.GetTransferEvents(ctx, network.ChainID().Uint64(), satelliteName, 0)
		require.NoError(t, err)
		require.Equal(t, 0, len(eventsList))
		eventsList, err = eventsCache.GetTransferEvents(ctx, network.ChainID().Uint64(), accs[3].Address, 0)
		require.NoError(t, err)
		require.Equal(t, 0, len(eventsList))

		// run the transfer events cache chore
		eventsCacheChore := events.NewChore(logger, eventsCache, ethEndpoints, 10)
		defer ctx.Check(eventsCacheChore.Close)
		ctx.Go(func() error {
			return eventsCacheChore.Run(ctx)
		})
		eventsCacheChore.Loop.Pause()
		eventsCacheChore.Loop.TriggerWait()

		eventsList, err = eventsCache.GetTransferEvents(ctx, network.ChainID().Uint64(), satelliteName, 0)
		require.NoError(t, err)
		require.Equal(t, 9, len(eventsList))
		eventsList, err = eventsCache.GetTransferEvents(ctx, network.ChainID().Uint64(), accs[3].Address, 0)
		require.NoError(t, err)
		require.Equal(t, 6, len(eventsList))
		eventsList, err = eventsCache.GetTransferEvents(ctx, network.ChainID().Uint64(), accs[4].Address, 0)
		require.NoError(t, err)
		require.Equal(t, 3, len(eventsList))
	})
}
