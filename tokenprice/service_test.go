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

func TestServicePriceAt(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)
		tokenPriceDB := db.TokenPrice()
		now := time.Now().Truncate(time.Second).UTC()

		price := currency.AmountFromBaseUnits(10, currency.USDollarsMicro)
		require.NoError(t, tokenPriceDB.Update(ctx, now, price.BaseUnits()))

		service := tokenprice.NewService(log, tokenPriceDB, coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)

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
			// price in DB is out of range, and we cannot obtain a price in the future, so error should be thrown.
			p, err := service.PriceAt(ctx, now.Add(2*time.Minute))
			require.Error(t, err)
			require.Zero(t, p)
		})
		t.Run("price is too new", func(t *testing.T) {
			// price in DB is out of range, so request is made to get a new price and update DB
			p, err := service.PriceAt(ctx, now.Add(-5*time.Minute))
			require.NoError(t, err)
			require.NotZero(t, p)
		})
	})
}

func TestServicePriceAtEmptyDB(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		log := zaptest.NewLogger(t)

		service := tokenprice.NewService(log, db.TokenPrice(), coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t)), time.Minute)

		p, err := service.PriceAt(ctx, time.Now())
		require.NoError(t, err)
		require.NotZero(t, p)
	})
}
