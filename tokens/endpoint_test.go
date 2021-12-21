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
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/api"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
	"storj.io/storjscan/tokens"
)

func TestEndpoint(t *testing.T) {
	testeth.Run(t, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		service := tokens.NewService(logger.Named("service"), network.HTTPEndpoint(), tokenAddress)
		endpoint := tokens.NewEndpoint(logger.Named("endpoint"), service)

		apiServer := api.NewServer(logger, lis)
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
		_, err = network.WaitForTx(ctx, tx.Hash())
		require.NoError(t, err)

		url := fmt.Sprintf(
			"http://%s/api/v0/example/payments/%s",
			lis.Addr().String(), accounts[1].Address.String())

		resp, err := http.Get(url)
		require.NoError(t, err)
		defer ctx.Check(resp.Body.Close)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var payments []tokens.Payment
		err = json.NewDecoder(resp.Body).Decode(&payments)
		require.NoError(t, err)
		require.Equal(t, accounts[0].Address, payments[0].From)
		require.Equal(t, int64(1000000), payments[0].TokenValue.Int64())
		require.Equal(t, tx.Hash(), payments[0].Transaction)
	})
}
