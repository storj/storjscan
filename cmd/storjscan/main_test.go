// Copyright (C) 2023 Storj Labs, Inc.
// See LICENSE for copying information.

package main_test

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/api"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"
)

func TestImport(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		// setup environment
		logger := zaptest.NewLogger(t)

		service, err := wallets.NewService(logger, db.Wallets())
		require.NoError(t, err)

		endpoint := wallets.NewEndpoint(logger, service)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)

		apiServer := api.NewServer(logger, lis, map[string]string{"eu1": "secret"})
		apiServer.NewAPI("/wallets", endpoint.Register)
		ctx.Go(func() error {
			return apiServer.Run(ctx)
		})
		defer func() {
			err = apiServer.Close()
			require.NoError(t, err)
		}()

		mnemonic := "leader pause fashion picnic green elder rebuild health valley alert cactus latin skull antique arrest skirt health chaos student will north garbage wagon before"

		addresses, err := wallets.Generate(ctx, "defaultkey", 0, 5, mnemonic)
		require.NoError(t, err)

		// sort descending
		sort.Slice(addresses, func(i, j int) bool {
			return bytes.Compare(addresses[i].Address.Bytes(), addresses[j].Address.Bytes()) > 0
		})

		importFilePath := ctx.File("wallets.csv")
		importFile, err := os.Create(importFilePath)
		require.NoError(t, err)

		fmt.Fprintln(importFile, "address,info")
		for _, address := range addresses {
			fmt.Fprintf(importFile, "%s,%s\n", address.Address.String(), address.Info)
		}
		require.NoError(t, importFile.Close())

		exe := ctx.Compile("storj.io/storjscan/cmd/storjscan")
		cmd := exec.Command(exe, "import", "--input-file", importFilePath, "--address", "http://"+lis.Addr().String(), "--api-key", "eu1", "--api-secret", "secret")
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "Error running test: %s", out)

		// verify that addresses are claimed in the import order
		for _, expectedAddress := range addresses {
			address, err := service.Claim(ctx, "eu1")
			require.NoError(t, err)

			require.Equal(t, expectedAddress, address)
		}

		// no more wallets to claim
		_, err = service.Claim(ctx, "eu1")
		require.Error(t, err)
	})
}
