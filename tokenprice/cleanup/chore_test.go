// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package cleanup_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokenprice/cleanup"
)

func TestChore(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		currentTime := time.Now().Truncate(time.Millisecond)
		tokenPriceDates := []time.Time{
			currentTime,
			currentTime.AddDate(0, 0, -29),
			currentTime.AddDate(0, 0, -31),
			currentTime.AddDate(-1, 0, 0),
		}
		for _, date := range tokenPriceDates {
			err := db.TokenPrice().Update(ctx, date, 1)
			require.NoError(t, err)
		}

		// first price quote prior to 30 days should return the record 31 days ago
		price, err := db.TokenPrice().Before(ctx, time.Now().AddDate(0, 0, -30))
		require.NoError(t, err)
		require.Equal(t, currentTime.AddDate(0, 0, -31), price.Timestamp.Local())

		chore := cleanup.NewChore(zaptest.NewLogger(t), db.TokenPrice(), cleanup.Config{
			Interval:   336 * time.Hour,
			RetainDays: 30,
		})

		defer ctx.Check(chore.Close)
		ctx.Go(func() error {
			return chore.Run(ctx)
		})

		chore.Loop.Pause()
		chore.Loop.TriggerWait()

		// after chore, all records 30 days ago or older should be gone.
		price, err = db.TokenPrice().Before(ctx, time.Now().AddDate(0, 0, -30))
		require.Equal(t, err, tokenprice.ErrNoQuotes)
		require.Equal(t, tokenprice.PriceQuote{}, price)
		// but record 29 days ago should still be present
		price, err = db.TokenPrice().Before(ctx, time.Now().AddDate(0, 0, -28))
		require.NoError(t, err)
		require.Equal(t, currentTime.AddDate(0, 0, -29), price.Timestamp.Local())
	})
}
