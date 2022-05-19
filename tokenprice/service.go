// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"
)

// ErrService is token price service error class.
var ErrService = errs.Class("tokenprice service")

// Service retrieves token price.
type Service struct {
	log *zap.Logger
	db  PriceQuoteDB
}

// NewService creates new service.
func NewService(log *zap.Logger, db PriceQuoteDB) *Service {
	return &Service{
		log: log,
		db:  db,
	}
}

// PriceAt retrieves token price at a particular timestamp.
func (service *Service) PriceAt(ctx context.Context, timestamp time.Time) (_ float64, err error) {
	defer mon.Task()(&ctx)(&err)

	quote, err := service.db.Before(ctx, timestamp)
	if err != nil {
		return 0, ErrService.Wrap(err)
	}

	if timestamp.Sub(quote.Timestamp) > time.Minute {
		return 0, ErrService.Wrap(ErrNoQuotes)
	}

	return quote.Price, nil
}
