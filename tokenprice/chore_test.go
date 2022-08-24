// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/currency"
	"storj.io/common/testcontext"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokenprice/coinmarketcap"
	"storj.io/storjscan/tokenprice/coinmarketcaptest"
)

func TestChore(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		service := tokenprice.NewService(zaptest.NewLogger(t), db.TokenPrice(), coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)
		chore := tokenprice.NewChore(zaptest.NewLogger(t), service, time.Second*5)

		defer ctx.Check(chore.Close)
		ctx.Go(func() error {
			return chore.Run(ctx)
		})

		chore.Loop.Pause()
		chore.Loop.TriggerWait()
		tokenPrice, err := db.TokenPrice().Before(ctx, time.Now())
		require.Nil(t, err)
		require.NotNil(t, tokenPrice)
		require.NotEqual(t, time.Time{}, tokenPrice.Timestamp)
		require.False(t, currency.AmountFromBaseUnits(0, currency.USDollarsMicro).Equal(tokenPrice.Price))
	})
}
