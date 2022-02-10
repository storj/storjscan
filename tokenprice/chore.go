// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/storjscan/tokenprice/coinmarketcap"
)

// ErrChore is an error class for coinmarketcap API client error.
var ErrChore = errs.Class("Chore")

// Config is a configuration struct for the Chore.
type Config struct {
	Interval time.Duration `help:"how often to run the chore" default:"1m" testDefault:"$TESTINTERVAL"`

	CoinmarketcapConfig coinmarketcap.Config
}

// Chore to save storj ticker price to local DB.
//
// architecture: Chore
type Chore struct {
	log    *zap.Logger
	db     PriceQuoteDB
	client *coinmarketcap.Client

	Loop *sync2.Cycle
}

// NewChore creates new chore for saving storj ticker price to local DB.
func NewChore(log *zap.Logger, db PriceQuoteDB, client *coinmarketcap.Client, interval time.Duration) *Chore {

	return &Chore{
		log:    log,
		db:     db,
		client: client,
		Loop:   sync2.NewCycle(interval),
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
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
	timeWindow, price, err := chore.client.GetLatestPrice(ctx)
	if err != nil {
		return err
	}
	err = chore.db.Update(ctx, timeWindow.Truncate(time.Minute), price)
	return err
}

// Close stops the chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
