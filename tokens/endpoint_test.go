// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens_test

import (
	"encoding/base64"
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
	"storj.io/common/uuid"
	"storj.io/storjscan/api"
	"storj.io/storjscan/private/testeth"
	"storj.io/storjscan/private/testeth/testtoken"
	"storj.io/storjscan/tokens"
)

func TestEndpoint(t *testing.T) {
	testeth.Run(t, nil, func(ctx *testcontext.Context, t *testing.T, tokenAddress common.Address, network *testeth.Network) {
		logger := zaptest.NewLogger(t)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		service := tokens.NewService(logger.Named("service"), network.HTTPEndpoint(), tokenAddress)
		endpoint := tokens.NewEndpoint(logger.Named("endpoint"), service)

		apiKey, err := uuid.New()
		require.NoError(t, err)

		apiServer := api.NewServer(logger, lis, [][]byte{apiKey.Bytes()})
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		require.NoError(t, err)
		req.Header.Add("STORJSCAN_API_KEY", base64.URLEncoding.EncodeToString(apiKey.Bytes()))

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer ctx.Check(func() error { return resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var payments []tokens.Payment
		err = json.NewDecoder(resp.Body).Decode(&payments)
		require.NoError(t, err)
		require.Equal(t, accounts[0].Address, payments[0].From)
		require.Equal(t, int64(1000000), payments[0].TokenValue.Int64())
		require.Equal(t, tx.Hash(), payments[0].Transaction)
	})
}
