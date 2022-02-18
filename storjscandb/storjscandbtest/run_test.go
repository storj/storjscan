// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandbtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/storjscandb/storjscandbtest"
)

func TestRun(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		tableCmd := `CREATE TABLE test ( 
			number bigint NOT NULL, 
			PRIMARY KEY ( number )
		)`
		_, err := db.Exec(ctx, tableCmd)
		require.NoError(t, err)

		_, err = db.Exec(ctx, "INSERT INTO test ( number ) VALUES ( ? )", int64(1))
		require.NoError(t, err)

		row := db.QueryRowContext(ctx, "SELECT number FROM test")
		require.NoError(t, row.Err())
		var num int64
		require.NoError(t, row.Scan(&num))
		require.Equal(t, int64(1), num)
	})
}
