// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/api"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
	"storj.io/storjscan/storjscandb/dbx"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokenprice/coinmarketcap"
	"storj.io/storjscan/tokenprice/coinmarketcaptest"
	"storj.io/storjscan/tokens"
)

func TestPayments(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testPayments(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testPayments(t, pgtest.PickCockroach(t))
	})
}

func testPayments(t *testing.T, connStr string) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		db, err := storjscandbtest.OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		client := network.Dial()
		defer client.Close()

		tk, err := testtoken.NewTestToken(tokenAddress, client)
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
			BlockHash   common.Hash
			BlockNumber int64
			Tx          common.Hash
			LogIndex    int
		}{
			{Amount: 10000, From: accs[0]},
			{Amount: 10000, From: accs[1]},
			{Amount: 10000, From: accs[2]},
			{Amount: 1000, From: accs[2]},
			{Amount: 2000, From: accs[1]},
			{Amount: 3000, From: accs[0]},
		}
		for i, testPayment := range testPayments {
			nonce, err := client.PendingNonceAt(ctx, testPayment.From.Address)
			require.NoError(t, err)

			opts = network.TransactOptions(ctx, testPayment.From, int64(nonce))
			tx, err = tk.Transfer(opts, accs[3].Address, big.NewInt(testPayment.Amount))
			require.NoError(t, err)

			recpt, err := network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)
			testPayments[i].BlockHash = recpt.BlockHash
			testPayments[i].BlockNumber = recpt.BlockNumber.Int64()
			testPayments[i].Tx = tx.Hash()
			testPayments[i].LogIndex = 0
		}

		// fill token price DB.
		tokenPriceDB := db.TokenPrice()
		firstBlock := network.Ethereum().BlockChain().GetBlockByNumber(1)
		const price = 2

		startTime := time.Unix(int64(firstBlock.Time()), 0).Add(-time.Minute)
		for i := 0; i < 10; i++ {
			window := startTime.Add(time.Duration(i) * time.Minute)
			require.NoError(t, tokenPriceDB.Update(ctx, window, price))
		}

		cache := blockchain.NewHeadersCache(logger, db.Headers())
		tokenPrice := tokenprice.NewService(logger, tokenPriceDB, coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)
		service := tokens.NewService(logger, network.HTTPEndpoint(), tokenAddress, cache, nil, tokenPrice, 100)

		payments, err := service.Payments(ctx, accs[3].Address, 0)
		require.NoError(t, err)

		for i, payment := range payments {
			testPayment := testPayments[i]
			require.Equal(t, testPayment.From.Address, payment.From)
			require.Equal(t, testPayment.Amount, payment.TokenValue.Int64())
			require.EqualValues(t, testPayment.Amount*price, payment.USDValue)
			require.Equal(t, testPayment.Tx, payment.Transaction)
		}
	})
}

func TestAllPayments(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testAllPayments(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testAllPayments(t, pgtest.PickCockroach(t))
	})
}

func testAllPayments(t *testing.T, connStr string) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		db, err := storjscandbtest.OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		client := network.Dial()
		defer client.Close()

		tk, err := testtoken.NewTestToken(tokenAddress, client)
		require.NoError(t, err)

		accs := network.Accounts()

		// transfer 1000000 a0 -> [a1..a3]
		for i := 1; i < 4; i++ {
			opts := network.TransactOptions(ctx, accs[0], int64(i))
			tx, err := tk.Transfer(opts, accs[i].Address, big.NewInt(1000000))
			require.NoError(t, err)
			_, err = network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)
		}

		// create pool of addresses from [a4..a9] and claim them:
		for i := 4; i < 10; i++ {
			var optional dbx.Wallet_Create_Fields

			// 4,5,6 are unclaimed
			if i > 6 {
				optional.Claimed = dbx.Wallet_Claimed(time.Now())
			}
			apiKey := "eu1"
			if i == 7 {
				apiKey = "us1"
			}
			_, err = db.Create_Wallet(ctx,
				dbx.Wallet_Address(accs[i].Address.Bytes()),
				dbx.Wallet_Satellite(apiKey),
				optional)
			require.NoError(t, err)

		}

		// do actual transfers (from acc[1..3] --> a[7..9])
		testPayments := []struct {
			Amount      int64
			From        accounts.Account
			To          accounts.Account
			BlockHash   common.Hash
			BlockNumber int64
			Tx          common.Hash
			LogIndex    int
		}{
			{Amount: 10000, From: accs[1], To: accs[7]},
			{Amount: 10000, From: accs[2], To: accs[8]},
			{Amount: 10000, From: accs[3], To: accs[9]},
			{Amount: 1000, From: accs[3], To: accs[7]},
			{Amount: 2000, From: accs[3], To: accs[8]},
			{Amount: 3000, From: accs[2], To: accs[9]},

			// sending to unclaimed address
			{Amount: 3000, From: accs[2], To: accs[6]},
		}
		for i, testPayment := range testPayments {
			nonce, err := client.PendingNonceAt(ctx, testPayment.From.Address)
			require.NoError(t, err)

			opts := network.TransactOptions(ctx, testPayment.From, int64(nonce))
			tx, err := tk.Transfer(opts, testPayment.To.Address, big.NewInt(testPayment.Amount))
			require.NoError(t, err)

			recpt, err := network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)
			testPayments[i].BlockHash = recpt.BlockHash
			testPayments[i].BlockNumber = recpt.BlockNumber.Int64()
			testPayments[i].Tx = tx.Hash()
			testPayments[i].LogIndex = 0
		}

		// fill token price DB.
		tokenPriceDB := db.TokenPrice()
		firstBlock := network.Ethereum().BlockChain().GetBlockByNumber(1)
		const price = 2

		startTime := time.Unix(int64(firstBlock.Time()), 0).Add(-time.Minute)
		for i := 0; i < 10; i++ {
			window := startTime.Add(time.Duration(i) * time.Minute)
			require.NoError(t, tokenPriceDB.Update(ctx, window, price))
		}

		cache := blockchain.NewHeadersCache(logger, db.Headers())
		tokenPrice := tokenprice.NewService(logger, tokenPriceDB, coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)
		service := tokens.NewService(logger, network.HTTPEndpoint(), tokenAddress, cache, db.Wallets(), tokenPrice, 100)

		currentHead, err := client.HeaderByNumber(ctx, nil)
		require.NoError(t, err)
		latestBlockHeader := blockchain.Header{
			Hash:      currentHead.Hash(),
			Number:    currentHead.Number.Int64(),
			Timestamp: time.Unix(int64(currentHead.Time), 0).UTC(),
		}

		t.Run("eu1 from block 0", func(t *testing.T) {
			payments, err := service.AllPayments(api.SetAPIIdentifier(ctx, "eu1"), "eu1", 1)
			require.NoError(t, err)

			// 4 transactions out of 6
			require.Equal(t, latestBlockHeader, payments.LatestBlock)
			require.Equal(t, 4, len(payments.Payments))

			txEqual(t, testPayments[1], payments.Payments[0])
			require.EqualValues(t, testPayments[1].Amount*price, payments.Payments[0].USDValue)
			txEqual(t, testPayments[2], payments.Payments[1])
			require.EqualValues(t, testPayments[2].Amount*price, payments.Payments[1].USDValue)
			txEqual(t, testPayments[4], payments.Payments[2])
			require.EqualValues(t, testPayments[4].Amount*price, payments.Payments[2].USDValue)
			txEqual(t, testPayments[5], payments.Payments[3])
			require.EqualValues(t, testPayments[5].Amount*price, payments.Payments[3].USDValue)

		})
		t.Run("eu1 with specified block", func(t *testing.T) {
			payments, err := service.AllPayments(api.SetAPIIdentifier(ctx, "eu1"), "eu1", testPayments[4].BlockNumber)
			require.NoError(t, err)

			// 2 transactions out of 6
			require.Equal(t, latestBlockHeader, payments.LatestBlock)
			require.Equal(t, 2, len(payments.Payments))

			txEqual(t, testPayments[4], payments.Payments[0])
			require.EqualValues(t, testPayments[4].Amount*price, payments.Payments[0].USDValue)
			txEqual(t, testPayments[5], payments.Payments[1])
			require.EqualValues(t, testPayments[5].Amount*price, payments.Payments[1].USDValue)
		})
	})

}

func TestPing(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		service := tokens.NewService(zaptest.NewLogger(t), network.HTTPEndpoint(), tokenAddress, nil, nil, nil, 100)
		err := service.Ping(ctx)
		require.NoError(t, err)
	})
}

func txEqual(t *testing.T, s struct {
	Amount      int64
	From        accounts.Account
	To          accounts.Account
	BlockHash   common.Hash
	BlockNumber int64
	Tx          common.Hash
	LogIndex    int
}, payment tokens.Payment) {
	require.Equal(t, s.From.Address, payment.From)
	require.Equal(t, s.To.Address, payment.To)
	require.Equal(t, s.Amount, payment.TokenValue.Int64())
	require.Equal(t, s.Tx, payment.Transaction)
}
