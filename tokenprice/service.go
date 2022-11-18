// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"errors"
	"go.opentelemetry.io/otel"
	"os"
	"runtime"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/currency"
)

// ErrService is token price service error class.
var ErrService = errs.Class("tokenprice service")

// Service retrieves token price.
type Service struct {
	log         *zap.Logger
	db          PriceQuoteDB
	client      Client
	priceWindow time.Duration
}

// NewService creates new service.
func NewService(log *zap.Logger, db PriceQuoteDB, client Client, priceWindow time.Duration) *Service {
	return &Service{
		log:         log,
		db:          db,
		client:      client,
		priceWindow: priceWindow,
	}
}

// PriceAt retrieves token price at a particular timestamp.
func (service *Service) PriceAt(ctx context.Context, timestamp time.Time) (_ currency.Amount, err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
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
func (service *Service) LatestPrice(ctx context.Context) (_ time.Time, _ currency.Amount, err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	service.log.Debug("retrieving latest price")
	timestamp, price, err := service.client.GetLatestPrice(ctx)
	return timestamp, price, ErrService.Wrap(err)
}

// SavePrice stores the token price for the given time window.
func (service *Service) SavePrice(ctx context.Context, timestamp time.Time, price currency.Amount) (err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	return ErrService.Wrap(service.db.Update(ctx, timestamp, price.BaseUnits()))
}

// Ping checks that the third-party api is available for use.
func (service *Service) Ping(ctx context.Context) (statusCode int, err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	return service.client.Ping(ctx)
}
