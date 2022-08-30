// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"

	"storj.io/common/currency"
)

// CalculateValue calculates value from given token value and price.
func CalculateValue(value, price currency.Amount) currency.Amount {
	val := value.AsDecimal().Mul(price.AsDecimal())
	return currency.AmountFromDecimal(val, price.Currency())
}

// Client is the interface used to query for STORJ token price.
type Client interface {
	// GetLatestPrice gets the latest available ticker price.
	GetLatestPrice(context.Context) (time.Time, currency.Amount, error)
	// GetPriceAt gets the ticker price at the specified time.
	GetPriceAt(context.Context, time.Time) (time.Time, currency.Amount, error)
	// Ping checks that the third-party api is available for use.
	Ping(ctx context.Context) (int, error)
}
