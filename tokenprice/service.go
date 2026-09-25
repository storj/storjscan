// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/currency"
)

// ErrService is token price service error class.
var ErrService = errs.Class("tokenprice service")

// Price provides the USD price of the token.
type Price interface {
	// PriceAt retrieves token price at a particular timestamp.
	PriceAt(ctx context.Context, timestamp time.Time) (currency.Amount, error)
	// Ping checks that the price source is available for use.
	Ping(ctx context.Context) (statusCode int, err error)
}

var (
	_ Price = (*CoinmarketcapPrice)(nil)
	_ Price = (*FixedPrice)(nil)
)

// CoinmarketcapPrice retrieves token price from coinmarketcap, and caches it in the DB.
type CoinmarketcapPrice struct {
	log         *zap.Logger
	db          PriceQuoteDB
	client      Client
	priceWindow time.Duration
}

// NewCoinmarketcapPrice creates new coinmarketcap based token price service.
func NewCoinmarketcapPrice(log *zap.Logger, db PriceQuoteDB, client Client, priceWindow time.Duration) *CoinmarketcapPrice {
	return &CoinmarketcapPrice{
		log:         log,
		db:          db,
		client:      client,
		priceWindow: priceWindow,
	}
}

// PriceAt retrieves token price at a particular timestamp.
func (service *CoinmarketcapPrice) PriceAt(ctx context.Context, timestamp time.Time) (_ currency.Amount, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("retrieving price at", zap.String("timestamp", timestamp.String()))

	quote, err := service.db.Before(ctx, timestamp)
	if err != nil && !errors.Is(err, ErrNoQuotes) {
		return currency.Amount{}, ErrService.Wrap(err)
	}

	if timestamp.Sub(quote.Timestamp) > service.priceWindow {
		priceTimestamp, price, err := service.client.GetPriceAt(ctx, timestamp.Truncate(time.Minute))
		if err != nil {
			return currency.Amount{}, ErrService.Wrap(err)
		}
		if timestamp.Sub(priceTimestamp) > service.priceWindow {
			return currency.Amount{}, ErrService.New("retrieved price does not meet requirements")
		}
		err = service.db.Update(ctx, priceTimestamp.Truncate(time.Minute), price.BaseUnits())
		if err != nil {
			return currency.Amount{}, ErrService.Wrap(err)
		}
		return price, nil
	}

	return quote.Price, nil
}

// LatestPrice gets the latest available ticker price.
func (service *CoinmarketcapPrice) LatestPrice(ctx context.Context) (_ time.Time, _ currency.Amount, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("retrieving latest price")
	timestamp, price, err := service.client.GetLatestPrice(ctx)
	return timestamp, price, ErrService.Wrap(err)
}

// SavePrice stores the token price for the given time window.
func (service *CoinmarketcapPrice) SavePrice(ctx context.Context, timestamp time.Time, price currency.Amount) (err error) {
	defer mon.Task()(&ctx)(&err)
	return ErrService.Wrap(service.db.Update(ctx, timestamp, price.BaseUnits()))
}

// Ping checks that the third-party api is available for use.
func (service *CoinmarketcapPrice) Ping(ctx context.Context) (statusCode int, err error) {
	defer mon.Task()(&ctx)(&err)
	return service.client.Ping(ctx)
}

// FixedPrice always returns the same token price.
type FixedPrice struct {
	price currency.Amount
}

// NewFixedPrice creates new token price service with a fixed price.
func NewFixedPrice(price currency.Amount) *FixedPrice {
	return &FixedPrice{price: price}
}

// PriceAt returns the fixed price.
func (service *FixedPrice) PriceAt(ctx context.Context, timestamp time.Time) (_ currency.Amount, err error) {
	return service.price, nil
}

// Ping always succeeds, as there is no external dependency.
func (service *FixedPrice) Ping(ctx context.Context) (statusCode int, err error) {
	return http.StatusOK, nil
}
