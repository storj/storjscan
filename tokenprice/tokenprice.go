// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import "math/big"

// CalculateValue calculates value from given token value and price.
func CalculateValue(value *big.Int, price float64) float64 {
	val := new(big.Float).Mul(new(big.Float).SetInt(value), big.NewFloat(price))
	valF, _ := val.Float64()
	return valF
}
