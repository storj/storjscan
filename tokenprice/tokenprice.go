// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"math/big"
	"time"
)

// CalculateValue calculates value from given token value and price.
func CalculateValue(value *big.Int, price float64) float64 {
	val := new(big.Float).Mul(new(big.Float).SetInt(value), big.NewFloat(price))
	valF, _ := val.Float64()
	return valF
}

// Client is the interface used to query for STORJ token price.
type Client interface {
	// GetLatestPrice gets the latest available ticker price.
	GetLatestPrice(context.Context) (time.Time, float64, error)
	// GetPriceAt gets the ticker price at the specified time.
	GetPriceAt(context.Context, time.Time) (time.Time, float64, error)
}
