// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/api"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

func TestEndpoint(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		satelliteName := "test-satellite"
		logger := zaptest.NewLogger(t)
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		service, err := wallets.NewService(logger.Named("service"), db.Wallets())
		require.NoError(t, err)
		endpoint := wallets.NewEndpoint(logger.Named("endpoint"), service)

		apiServer := api.NewServer(logger, lis, map[string]string{satelliteName: "secret"})
		apiServer.NewAPI("/wallets", endpoint.Register)
		ctx.Go(func() error {
			return apiServer.Run(ctx)
		})
		defer ctx.Check(apiServer.Close)

		err = generateTestAddresses(ctx, service, satelliteName, 1)
		require.NoError(t, err)

		// happy path
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/api/v0/wallets/claim", lis.Addr().String()), nil)
		require.NoError(t, err)

		// we should get access denied without authentication
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		req.SetBasicAuth(satelliteName, "secret")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer ctx.Check(func() error { return resp.Body.Close() })
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var returnAddr *string
		err = json.NewDecoder(resp.Body).Decode(&returnAddr)
		require.NoError(t, err)
		require.NotNil(t, returnAddr)

		addresses, err := service.ListBySatellite(ctx, satelliteName)
		require.NoError(t, err)
		require.Equal(t, 1, len(addresses))

		// unexpected path (no more addresses available)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://%s/api/v0/wallets/claim", lis.Addr().String()), nil)
		require.NoError(t, err)

		req.SetBasicAuth(satelliteName, "secret")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer ctx.Check(func() error { return resp.Body.Close() })
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	})
}
