// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package cleanup

import (
	"context"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/storjscan/tokenprice"
)

var mon = monkit.Package()

// Config is a configuration struct for the Chore.
type Config struct {
	Interval   time.Duration `help:"how often to remove old token prices" default:"336h" testDefault:"$TESTINTERVAL"`
	RetainDays int           `help:"number of days of token prices to retain" default:"30"`
}

// Chore to remove old token prices.
//
// architecture: Chore
type Chore struct {
	log    *zap.Logger
	db     tokenprice.PriceQuoteDB
	config Config

	Loop *sync2.Cycle
}

// NewChore creates new chore for removing old token prices.
func NewChore(log *zap.Logger, db tokenprice.PriceQuoteDB, config Config) *Chore {

	return &Chore{
		log:    log,
		db:     db,
		config: config,

		Loop: sync2.NewCycle(config.Interval),
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return chore.Loop.Run(ctx, func(ctx context.Context) error {
		err := chore.RunOnce(ctx)
		if err != nil {
			chore.log.Error("error running token price cleanup chore", zap.Error(err))
		}
		return nil
	})
}

// RunOnce removes old token prices.
func (chore *Chore) RunOnce(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	if chore.config.RetainDays < 0 {
		return errs.New("retain days cannot be less than 0")
	}

	beforeDays := time.Now().UTC().AddDate(0, 0, -chore.config.RetainDays)
	err = chore.db.DeleteBefore(ctx, beforeDays)
	if err != nil {
		chore.log.Error("error removing old token prices", zap.Error(err))
	}

	return nil
}

// Close stops the chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
