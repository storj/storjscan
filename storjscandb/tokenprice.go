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

// TokenPriceDB provides access to the database that stores STORJ token price information.
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

// Before gets the first token price with timestamp before provided timestamp.
func (priceQuoteDB priceQuoteDB) Before(ctx context.Context, before time.Time) (_ tokenprice.PriceQuote, err error) {
	defer mon.Task()(&ctx)(&err)
	rows, err := priceQuoteDB.db.First_TokenPrice_By_IntervalStart_Less_OrderBy_Desc_IntervalStart(ctx,
		dbx.TokenPrice_IntervalStart(before))
	if err != nil {
		return tokenprice.PriceQuote{}, ErrPriceQuoteDB.Wrap(err)
	}
	if rows == nil {
		return tokenprice.PriceQuote{}, tokenprice.ErrNoQuotes
	}
	return tokenprice.PriceQuote{
		Timestamp: rows.IntervalStart,
		Price:     rows.Price,
	}, nil
}
