// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"time"

	"github.com/zeebo/errs"

	"storj.io/storjscan/storjscandb/dbx"
	"storj.io/storjscan/tokenprice"
)

// ErrPriceQuoteDB indicates about internal headers DB error.
var ErrPriceQuoteDB = errs.Class("PriceQuoteDB")

// ensures that priceQuoteDB implements tokenprice.PriceEntryDB.
var _ tokenprice.PriceQuoteDB = (*priceQuoteDB)(nil)

// TokenPriceDB is the token price database dbx postgres implementation that stores STORJ token price information.
//
// architecture: Database
type priceQuoteDB struct {
	db *dbx.DB
}

// Update updates the stored token price for the given time window, or creates a new entry if it does not exist.
func (priceQuoteDB *priceQuoteDB) Update(ctx context.Context, window time.Time, price float64) (err error) {
	defer mon.Task()(&ctx)(&err)
	err = priceQuoteDB.db.ReplaceNoReturn_TokenPrice(ctx, dbx.TokenPrice_IntervalStart(window), dbx.TokenPrice_Price(price))
	return ErrPriceQuoteDB.Wrap(err)
}

// GetFirst gets the first token price with timestamp greater than provided window.
func (priceQuoteDB *priceQuoteDB) GetFirst(ctx context.Context, window time.Time) (pq tokenprice.PriceQuote, err error) {
	defer mon.Task()(&ctx)(&err)
	rows, err := priceQuoteDB.db.First_TokenPrice_By_IntervalStart_Greater_OrderBy_Asc_IntervalStart(ctx,
		dbx.TokenPrice_IntervalStart(window))
	if err != nil {
		return tokenprice.PriceQuote{}, ErrPriceQuoteDB.Wrap(err)
	}
	return tokenprice.PriceQuote{
		Timestamp: rows.IntervalStart,
		Price:     rows.Price,
	}, nil
}
