// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
	"storj.io/storjscan/api"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

func TestEndpoint(t *testing.T) {
	ctx := testcontext.New(t)
	defer ctx.Cleanup()

	logger := zaptest.NewLogger(t)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	connStr := pgtest.PickPostgres(t)
	db, err := storjscandbtest.OpenDB(ctx, logger, connStr, t.Name(), "T")
	require.NoError(t, err)
	defer ctx.Check(db.Close)
	err = db.MigrateToLatest(ctx)
	require.NoError(t, err)

	service, err := wallets.NewHD_test(logger.Named("service"), db.Wallets())
	require.NoError(t, err)

	endpoint := wallets.NewEndpoint(logger.Named("endpoint"), service)
	apiServer := api.NewServer(logger, lis)
	apiServer.NewAPI("/example", endpoint.Register)

	ctx.Go(func() error {
		return apiServer.Run(ctx)
	})
	defer ctx.Check(apiServer.Close)

	addr, err := service.Setup(ctx, 2)
	require.NoError(t, err)
	addresshex := hexutil.Encode(addr)
	var returnAddr []byte
	var returnCount int
	var returnAcct wallets.Account

	cases := []struct {
		desc       string
		url        string
		expected   interface{}
		returnType string
	}{
		{
			desc:       "GetCountDepositAddresses",
			url:        fmt.Sprintf("http://%s/api/v0/example/wallets/count", lis.Addr().String()),
			expected:   2,
			returnType: "count",
		},
		{
			desc:       "GetNewDepositAddress",
			url:        fmt.Sprintf("http://%s/api/v0/example/wallets/", lis.Addr().String()),
			expected:   addr,
			returnType: "address",
		},
		{
			desc:       "GetAccount",
			url:        fmt.Sprintf("http://%s/api/v0/example/wallets/%s", lis.Addr().String(), addresshex),
			expected:   addr,
			returnType: "account",
		},
		{
			desc:       "GetCountClaimedDepositAddresses",
			url:        fmt.Sprintf("http://%s/api/v0/example/wallets/count/claimed", lis.Addr().String()),
			expected:   1,
			returnType: "count",
		},
		{
			desc:       "GetCountUnclaimedDepositAddresses",
			url:        fmt.Sprintf("http://%s/api/v0/example/wallets/count/unclaimed", lis.Addr().String()),
			expected:   1,
			returnType: "count",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, tc.url, nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer ctx.Check(func() error { return resp.Body.Close() })
			require.Equal(t, http.StatusOK, resp.StatusCode)
			if tc.returnType == "count" {
				err = json.NewDecoder(resp.Body).Decode(&returnCount)
				require.NoError(t, err)
				require.Equal(t, tc.expected, returnCount)
			} else if tc.returnType == "address" {
				err = json.NewDecoder(resp.Body).Decode(&returnAddr)
				require.NoError(t, err)
				require.Equal(t, tc.expected, returnAddr)
			} else if tc.returnType == "account" {
				err = json.NewDecoder(resp.Body).Decode(&returnAcct)
				require.NoError(t, err)
				require.Equal(t, tc.expected, returnAcct.Address)
				require.NotNil(t, returnAcct.Claimed)
			}
		})
	}
}
