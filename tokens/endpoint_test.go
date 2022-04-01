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
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokens"
)

func TestEndpoint(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		connStr := pgtest.PickPostgres(t)
		db, err := storjscandbtest.OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		cache := blockchain.NewHeadersCache(logger, db.Headers())
		service := tokens.NewService(logger.Named("service"), network.HTTPEndpoint(), tokenAddress, cache)
		endpoint := tokens.NewEndpoint(logger.Named("endpoint"), service)

		apiServer := api.NewServer(logger, lis, map[string]string{"eu1": "eu1secret"})
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

		opts := network.TransactOptions(ctx, accounts[0], 1)
		tx, err := tk.Transfer(opts, accounts[1].Address, big.NewInt(1000000))
		require.NoError(t, err)
		recpt, err := network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		url := fmt.Sprintf(
			"http://%s/api/v0/example/payments/%s",
			lis.Addr().String(), accounts[1].Address.String())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)

		// without authentication we should get access denied
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		req.SetBasicAuth("eu1", "eu1secret")
		val := req.URL.Query()
		val.Add("from", "1")
		req.URL.RawQuery = val.Encode()

		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer ctx.Check(func() error { return resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var payments []tokens.Payment
		err = json.NewDecoder(resp.Body).Decode(&payments)
		require.NoError(t, err)
		require.Equal(t, accounts[0].Address, payments[0].From)
		require.Equal(t, int64(1000000), payments[0].TokenValue.Int64())
		require.Equal(t, recpt.BlockHash, payments[0].BlockHash)
		require.Equal(t, recpt.BlockNumber.Int64(), payments[0].BlockNumber)
		require.Equal(t, tx.Hash(), payments[0].Transaction)
		require.Equal(t, 0, payments[0].LogIndex)
	})
}
