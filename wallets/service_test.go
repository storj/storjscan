// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets_test

import (
	"testing"

	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/wallets"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"storj.io/common/testcontext"
	"storj.io/private/dbutil/pgtest"
)

func TestService(t *testing.T) {
	ctx := testcontext.New(t)
	defer ctx.Cleanup()

	logger := zaptest.NewLogger(t)

	connStr := pgtest.PickPostgres(t)
	db, err := storjscandbtest.OpenDB(ctx, logger, connStr, t.Name(), "T")
	require.NoError(t, err)
	defer ctx.Check(db.Close)
	err = db.MigrateToLatest(ctx)
	require.NoError(t, err)

	service, err := wallets.NewHD_test(logger.Named("service"), db.Wallets())
	require.NoError(t, err)

	expectedAddr, err := service.Setup(ctx, 10)
	require.NoError(t, err)
	total, err := service.GetCountDepositAddresses(ctx)
	require.NoError(t, err)
	require.Equal(t, 10, total)
	c, err := service.GetCountClaimedDepositAddresses(ctx, true)
	require.NoError(t, err)
	require.Equal(t, 0, c)
	c, err = service.GetCountClaimedDepositAddresses(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 10, c)

	addr, err := service.GetNewDepositAddress(ctx)
	require.NoError(t, err)
	require.Equal(t, expectedAddr, addr) //Is there a better way to compare byte slice equality?

	c, err = service.GetCountClaimedDepositAddresses(ctx, true)
	require.NoError(t, err)
	require.Equal(t, 1, c)
	c, err = service.GetCountClaimedDepositAddresses(ctx, false)
	require.NoError(t, err)
	require.Equal(t, 9, c)

	acct, err := service.GetAccount(ctx, addr)
	require.NoError(t, err)
	require.NotNil(t, acct.Address)
	require.NotNil(t, acct.Claimed)
}
