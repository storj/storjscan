// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"storj.io/storjscan/tokenprice"
)

func TestCalculateValue(t *testing.T) {
	var (
		tokenValue = big.NewInt(10000000)

		prices = []float64{
			0.9,
			1.05,
			1.10,
			1.25,
			2,
		}
		expected = []float64{
			9000000,
			10500000,
			11000000,
			12500000,
			20000000,
		}
	)

	for i, price := range prices {
		value := tokenprice.CalculateValue(tokenValue, price)
		require.Equal(t, expected[i], value)
	}
}
