// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokens"
)

func TestPayments(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)
		connStr := pgtest.PickPostgres(t)

		db, err := storjscandbtest.OpenDB(ctx, logger, connStr, t.Name(), "T")
		require.NoError(t, err)
		defer ctx.Check(db.Close)
		err = db.MigrateToLatest(ctx)
		require.NoError(t, err)

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
			Amount int64
			From   accounts.Account
			Tx     common.Hash
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

			_, err = network.WaitForTx(ctx, tx.Hash())
			require.NoError(t, err)
			testPayments[i].Tx = tx.Hash()
		}

		cache := blockchain.NewHeadersCache(logger, db.Headers(), "", 0, 0, 0, 0)
		service := tokens.NewService(logger, network.HTTPEndpoint(), tokenAddress, cache)

		payments, err := service.Payments(ctx, accs[3].Address)
		require.NoError(t, err)

		for i, payment := range payments {
			testPayment := testPayments[i]
			require.Equal(t, testPayment.From.Address, payment.From)
			require.Equal(t, testPayment.Amount, payment.TokenValue.Int64())
			require.Equal(t, testPayment.Tx, payment.Transaction)
		}
	})
}
