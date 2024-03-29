// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
)

// ErrChore is an error class for coinmarketcap API client error.
var ErrChore = errs.Class("Chore")

// Chore to save storj ticker price to local DB.
//
// architecture: Chore
type Chore struct {
	log     *zap.Logger
	service *Service

	Loop *sync2.Cycle
}

// NewChore creates new chore for saving storj ticker price to local DB.
func NewChore(log *zap.Logger, service *Service, interval time.Duration) *Chore {

	return &Chore{
		log:     log,
		service: service,
		Loop:    sync2.NewCycle(interval),
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return chore.Loop.Run(ctx, func(ctx context.Context) error {
		err := chore.RunOnce(ctx)
		if err != nil {
			chore.log.Error("Error running token price chore", zap.Error(ErrChore.Wrap(err)))
			return nil
		}
		return nil
	})
}

// RunOnce gets the latest storj ticker price and saves it to the DB.
func (chore *Chore) RunOnce(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	timeWindow, price, err := chore.service.LatestPrice(ctx)
	if err != nil {
		return err
	}
	err = chore.service.SavePrice(ctx, timeWindow.Truncate(time.Minute), price)
	return err
}

// Close stops the chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
