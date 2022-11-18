// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"go.opentelemetry.io/otel"
	"os"
	"runtime"
	"time"

	"github.com/zeebo/errs"

	"storj.io/common/currency"
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
func (priceQuoteDB *priceQuoteDB) Update(ctx context.Context, window time.Time, price int64) (err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	err = priceQuoteDB.db.ReplaceNoReturn_TokenPrice(ctx, dbx.TokenPrice_IntervalStart(window.UTC()), dbx.TokenPrice_Price(price))
	return ErrPriceQuoteDB.Wrap(err)
}

// Before gets the first token price with timestamp before provided timestamp.
func (priceQuoteDB priceQuoteDB) Before(ctx context.Context, before time.Time) (_ tokenprice.PriceQuote, err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	rows, err := priceQuoteDB.db.First_TokenPrice_By_IntervalStart_Less_OrderBy_Desc_IntervalStart(ctx,
		dbx.TokenPrice_IntervalStart(before.UTC()))
	if err != nil {
		return tokenprice.PriceQuote{}, ErrPriceQuoteDB.Wrap(err)
	}
	if rows == nil {
		return tokenprice.PriceQuote{}, tokenprice.ErrNoQuotes
	}
	return tokenprice.PriceQuote{
		Timestamp: rows.IntervalStart.UTC(),
		Price:     currency.AmountFromBaseUnits(rows.Price, currency.USDollarsMicro),
	}, nil
}

// DeleteBefore deletes token prices before the given time.
func (priceQuoteDB priceQuoteDB) DeleteBefore(ctx context.Context, before time.Time) (err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	_, err = priceQuoteDB.db.Delete_TokenPrice_By_IntervalStart_Less(ctx, dbx.TokenPrice_IntervalStart(before.UTC()))
	return ErrPriceQuoteDB.Wrap(err)
}
