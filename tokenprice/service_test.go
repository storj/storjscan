// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zeebo/errs"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/storjscandb/storjscandbtest"
	"storj.io/storjscan/tokenprice"
)

func TestServicePriceAt(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)
		tokenPriceDB := db.TokenPrice()
		now := time.Now().Truncate(time.Second).UTC()

		const price = 10
		require.NoError(t, tokenPriceDB.Update(ctx, now, price))

		service := tokenprice.NewService(log, tokenPriceDB)

		t.Run("price is in safe range", func(t *testing.T) {
			p, err := service.PriceAt(ctx, now.Add(time.Second))
			require.NoError(t, err)
			require.EqualValues(t, price, p)

			p, err = service.PriceAt(ctx, now.Add(30*time.Second))
			require.NoError(t, err)
			require.EqualValues(t, price, p)

			p, err = service.PriceAt(ctx, now.Add(60*time.Second))
			require.NoError(t, err)
			require.EqualValues(t, price, p)
		})

		t.Run("price is too old", func(t *testing.T) {
			_, err := service.PriceAt(ctx, now.Add(2*time.Minute))
			require.Error(t, err)
		})
	})
}

func TestServicePriceAtEmptyDB(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)
		service := tokenprice.NewService(log, db.TokenPrice())

		_, err := service.PriceAt(ctx, time.Now())
		require.True(t, errs.Is(err, tokenprice.ErrNoQuotes))
	})
}
