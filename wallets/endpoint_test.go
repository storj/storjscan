// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

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

	// TODO add 2 addresses to db

	var returnAddr []byte
	var returnCount int
	cases := []struct {
		desc      string
		url       string
		expected  interface{}
		returnVal interface{}
	}{
		{
			desc:      "GetCountDepositAddresses",
			url:       fmt.Sprintf("http://%s/api/v0/example/wallets/count", lis.Addr().String()),
			expected:  2,
			returnVal: returnCount,
		},
		{
			desc:      "GetCountUnclaimedDepositAddresses",
			url:       fmt.Sprintf("http://%s/api/v0/example/wallets/count/unclaimed", lis.Addr().String()),
			expected:  2,
			returnVal: returnCount,
		},
		{
			desc:      "GetNewDepositAddress",
			url:       fmt.Sprintf("http://%s/api/v0/example/wallets/", lis.Addr().String()),
			expected:  []byte{},
			returnVal: returnAddr,
		},
		{
			desc:      "GetCountClaimedDepositAddresses",
			url:       fmt.Sprintf("http://%s/api/v0/example/wallets/count/claimed", lis.Addr().String()),
			expected:  1,
			returnVal: returnCount,
		},
		{
			desc:      "GetCountUnclaimedDepositAddresses",
			url:       fmt.Sprintf("http://%s/api/v0/example/wallets/count/unclaimed", lis.Addr().String()),
			expected:  1,
			returnVal: returnCount,
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
			err = json.NewDecoder(resp.Body).Decode(tc.returnVal)
			require.NoError(t, err)
			require.Equal(t, tc.expected, tc.returnVal)
		})
	}
}
