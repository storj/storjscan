// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens_test

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
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

func TestEndpoint(t *testing.T) {
	t.Run("Postgres", func(t *testing.T) {
		testEndpoint(t, pgtest.PickPostgres(t))
	})
	t.Run("Cockroach", func(t *testing.T) {
		testEndpoint(t, pgtest.PickCockroach(t))
	})
}

func testEndpoint(t *testing.T, connStr string) {
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

		tokenPriceDB := db.TokenPrice()
		cache := blockchain.NewHeadersCache(logger, db.Headers())
		tokenPrice := tokenprice.NewService(logger, tokenPriceDB, coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		service := tokens.NewService(logger.Named("service"), network.HTTPEndpoint(), tokenAddress, cache, db.Wallets(), tokenPrice, 100)
		endpoint := tokens.NewEndpoint(logger.Named("endpoint"), service)

		apiServer := api.NewServer(logger, lis, map[string]string{"eu1": "eu1secret", "us1": "us1secret"})
		apiServer.NewAPI("/example", endpoint.Register)
		ctx.Go(func() error {
			return apiServer.Run(ctx)
		})
		defer ctx.Check(apiServer.Close)

		client := network.Dial()
		defer client.Close()

		tk, err := testtoken.NewTestToken(tokenAddress, client)
		require.NoError(t, err)

		accounts := network.Accounts()

		// transfer to accounts[1] from accounts[0]
		opts := network.TransactOptions(ctx, accounts[0], 1)
		tx, err := tk.Transfer(opts, accounts[1].Address, big.NewInt(1000000))
		require.NoError(t, err)
		recpt, err := network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		// transfer to accounts[2] from accounts[0]
		opts = network.TransactOptions(ctx, accounts[0], 2)
		_, err = tk.Transfer(opts, accounts[2].Address, big.NewInt(1000000))
		require.NoError(t, err)
		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		// transfer to accounts[2] from accounts[0]
		opts = network.TransactOptions(ctx, accounts[0], 3)
		_, err = tk.Transfer(opts, accounts[2].Address, big.NewInt(1000001))
		require.NoError(t, err)
		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		_, err = db.Create_Wallet(ctx,
			dbx.Wallet_Address(accounts[1].Address.Bytes()),
			dbx.Wallet_Satellite("eu1"),
			dbx.Wallet_Create_Fields{
				Claimed: dbx.Wallet_Claimed(time.Now()),
			})
		require.NoError(t, err)

		_, err = db.Create_Wallet(ctx,
			dbx.Wallet_Address(accounts[2].Address.Bytes()),
			dbx.Wallet_Satellite("us1"),
			dbx.Wallet_Create_Fields{
				Claimed: dbx.Wallet_Claimed(time.Now()),
			})
		require.NoError(t, err)

		// fill token price DB.
		const price = 2
		firstBlock := network.Ethereum().BlockChain().GetBlockByNumber(1)

		startTime := time.Unix(int64(firstBlock.Time()), 0).Add(-time.Minute)
		for i := 0; i < 10; i++ {
			window := startTime.Add(time.Duration(i) * time.Minute)
			require.NoError(t, tokenPriceDB.Update(ctx, window, price))
		}

		// get payments of one wallet

		t.Run("/payments/{address} without authentication", func(t *testing.T) {
			url := fmt.Sprintf(
				"http://%s/api/v0/example/payments/%s",
				lis.Addr().String(), accounts[1].Address.String())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			require.NoError(t, err)

			// without authentication we should get access denied
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})

		t.Run("/payments/{address} REST endpoint is working", func(t *testing.T) {
			url := fmt.Sprintf(
				"http://%s/api/v0/example/payments/%s",
				lis.Addr().String(), accounts[1].Address.String())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			require.NoError(t, err)

			req.SetBasicAuth("eu1", "eu1secret")
			val := req.URL.Query()
			val.Add("from", "1")
			req.URL.RawQuery = val.Encode()

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()

			defer ctx.Check(func() error { return resp.Body.Close() })
			require.Equal(t, http.StatusOK, resp.StatusCode)

			var payments []tokens.Payment
			err = json.NewDecoder(resp.Body).Decode(&payments)
			require.NoError(t, err)
			require.Equal(t, accounts[0].Address, payments[0].From)
			require.EqualValues(t, 1000000, payments[0].TokenValue)
			require.EqualValues(t, 1000000*price, payments[0].USDValue)
			require.Equal(t, recpt.BlockHash, payments[0].BlockHash)
			require.Equal(t, recpt.BlockNumber.Int64(), payments[0].BlockNumber)
			require.Equal(t, tx.Hash(), payments[0].Transaction)
			require.Equal(t, 0, payments[0].LogIndex)
		})

		t.Run("/payments REST endpoint is working", func(t *testing.T) {
			url := fmt.Sprintf(
				"http://%s/api/v0/example/payments",
				lis.Addr().String())
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			require.NoError(t, err)

			req.SetBasicAuth("us1", "us1secret")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()

			defer ctx.Check(func() error { return resp.Body.Close() })
			require.Equal(t, http.StatusOK, resp.StatusCode)

			currentHead, err := client.HeaderByNumber(ctx, nil)
			require.NoError(t, err)
			latestBlockHeader := blockchain.Header{
				Hash:      currentHead.Hash(),
				Number:    currentHead.Number.Int64(),
				Timestamp: time.Unix(int64(currentHead.Time), 0).UTC(),
			}

			var payments tokens.LatestPayments
			err = json.NewDecoder(resp.Body).Decode(&payments)
			require.NoError(t, err)
			require.Equal(t, latestBlockHeader, payments.LatestBlock)
			require.Len(t, payments.Payments, 2)
			require.Equal(t, accounts[2].Address, payments.Payments[0].To)
		})
	})
}
