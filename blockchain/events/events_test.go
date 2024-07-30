// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
	"storj.io/storjscan/storjscandb/storjscandbtest"
)

func TestEventsService(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testEventsService(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testEventsService(t, pgtest.PickCockroach(t))
	})
}

func testEventsService(t *testing.T, connStr string) {
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

		eventsService := events.NewEventsService(logger, db.Wallets(), events.Config{
			AddressBatchSize: 100,
			BlockBatchSize:   100,
			ChainReorgBuffer: 15,
			MaximumQuerySize: 10000,
		})

		_, eventsList, err := eventsService.GetForSatellite(ctx, ethEndpoints, satelliteName, map[int64]int64{network.ChainID().Int64(): 0})
		require.NoError(t, err)
		require.Equal(t, 9, len(eventsList))
		_, eventsList, err = eventsService.GetForAddress(ctx, ethEndpoints, []common.Address{accs[3].Address}, map[int64]int64{network.ChainID().Int64(): 0})
		require.NoError(t, err)
		require.Equal(t, 6, len(eventsList))
		_, eventsList, err = eventsService.GetForAddress(ctx, ethEndpoints, []common.Address{accs[4].Address}, map[int64]int64{network.ChainID().Int64(): 0})
		require.NoError(t, err)
		require.Equal(t, 3, len(eventsList))
	})
}
