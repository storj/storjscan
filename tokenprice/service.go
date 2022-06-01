// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"errors"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/tokenprice/coinmarketcap"
)

// ErrService is token price service error class.
var ErrService = errs.Class("tokenprice service")

// Service retrieves token price.
type Service struct {
	log         *zap.Logger
	db          PriceQuoteDB
	client      *coinmarketcap.Client
	priceWindow time.Duration
}

// NewService creates new service.
func NewService(log *zap.Logger, db PriceQuoteDB, client *coinmarketcap.Client, priceWindow time.Duration) *Service {
	return &Service{
		log:         log,
		db:          db,
		client:      client,
		priceWindow: priceWindow,
	}
}

// PriceAt retrieves token price at a particular timestamp.
func (service *Service) PriceAt(ctx context.Context, timestamp time.Time) (_ float64, err error) {
	defer mon.Task()(&ctx)(&err)

	quote, err := service.db.Before(ctx, timestamp)
	if err != nil && !errors.Is(err, ErrNoQuotes) {
		return 0, ErrService.Wrap(err)
	}

	if timestamp.Sub(quote.Timestamp) > service.priceWindow {
		priceTimestamp, price, err := service.client.GetPriceAt(ctx, timestamp.Truncate(time.Minute))
		if err != nil {
			return 0, ErrService.Wrap(err)
		}
		if timestamp.Sub(priceTimestamp) > service.priceWindow {
			return 0, ErrService.New("retrieved price does not meet requirements")
		}
		err = service.db.Update(ctx, priceTimestamp.Truncate(time.Minute), price)
		if err != nil {
			return price, ErrService.Wrap(err)
		}
		return price, nil
	}

	return quote.Price, nil
}

// LatestPrice gets the latest available ticker price.
func (service *Service) LatestPrice(ctx context.Context) (_ time.Time, _ float64, err error) {
	defer mon.Task()(&ctx)(&err)
	return service.client.GetLatestPrice(ctx)
}

// SavePrice stores the token price for the given time window.
func (service *Service) SavePrice(ctx context.Context, timestamp time.Time, price float64) (err error) {
	defer mon.Task()(&ctx)(&err)
	return service.db.Update(ctx, timestamp, price)
}
