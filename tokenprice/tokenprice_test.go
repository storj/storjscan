// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice_test

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"storj.io/common/currency"
	"storj.io/storjscan/tokenprice"
)

func TestCalculateValue(t *testing.T) {
	var (
		tokenValue = currency.AmountFromBaseUnits(100000000, currency.StorjToken)

		prices = []float64{
			0.9,
			1.05,
			1.10,
			1.25,
			2,
		}
		values = []int64{
			900000,
			1050000,
			1100000,
			1250000,
			2000000,
		}
	)

	for i, pricef := range prices {
		price := currency.AmountFromDecimal(decimal.NewFromFloat(pricef), currency.USDollarsMicro)
		expected := currency.AmountFromBaseUnits(values[i], currency.USDollarsMicro)

		value := tokenprice.CalculateValue(tokenValue, price)
		require.Equal(t, expected, value)
	}
}
