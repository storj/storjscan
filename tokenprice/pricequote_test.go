// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/currency"
	"storj.io/common/testcontext"
	"storj.io/storjscan/storjscandb/storjscandbtest"
)

func TestPriceQuoteDBBefore(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {
		tokenPriceDB := db.TokenPrice()
		now := time.Now().Truncate(time.Second).UTC()

		const priceCount = 10
		for i := 0; i < priceCount; i++ {
			require.NoError(t, tokenPriceDB.Update(ctx, now.Add(time.Duration(i)*time.Second), int64(i)*1000000))
		}

		pq, err := tokenPriceDB.Before(ctx, now.Add(priceCount*time.Second))
		require.NoError(t, err)
		require.Equal(t, now.Add((priceCount-1)*time.Second), pq.Timestamp.UTC())
		require.EqualValues(t, currency.AmountFromBaseUnits((priceCount-1)*1000000, currency.USDollarsMicro), pq.Price)
	})
}
