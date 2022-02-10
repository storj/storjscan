// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"
)

// PriceQuote represents an entry in the token_price table.
type PriceQuote struct {
	Timestamp time.Time
	Price     float64
}

// PriceQuoteDB is STORJ token price database.
//
// architecture: Database
type PriceQuoteDB interface {
	// Update updates the stored token price for the given time window, or creates a new entry if it does not exist.
	Update(ctx context.Context, window time.Time, price float64) error

	// GetFirst gets the first token price with timestamp greater than provided window.
	GetFirst(ctx context.Context, window time.Time) (PriceQuote, error)
}
