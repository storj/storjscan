// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"

	"storj.io/common/currency"
)

var (
	// ErrNoQuotes is error when no quotes were found in DB.
	ErrNoQuotes = errs.New("no quotes in db")

	mon = monkit.Package()
)

// PriceQuote represents an entry in the token_price table.
type PriceQuote struct {
	Timestamp time.Time
	Price     currency.Amount
}

// PriceQuoteDB is STORJ token price database.
//
// architecture: Database
type PriceQuoteDB interface {
	// Update updates the stored token price for the given time window, or creates a new entry if it does not exist.
	Update(ctx context.Context, window time.Time, price int64) error

	// Before gets the first token price with timestamp before provided timestamp.
	Before(ctx context.Context, before time.Time) (PriceQuote, error)
}
