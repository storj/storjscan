// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package cleanup

import (
	"context"
	"go.opentelemetry.io/otel"
	"os"
	"runtime"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/storjscan/blockchain"
)

// Config is a configuration struct for the Chore.
type Config struct {
	Interval   time.Duration `help:"how often to remove old block headers" default:"336h" testDefault:"$TESTINTERVAL"`
	RetainDays int           `help:"number of days of block headers to retain" default:"30"`
}

// Chore to remove old block headers.
//
// architecture: Chore
type Chore struct {
	log    *zap.Logger
	db     blockchain.HeadersDB
	config Config

	Loop *sync2.Cycle
}

// NewChore creates new chore for removing old block headers.
func NewChore(log *zap.Logger, db blockchain.HeadersDB, config Config) *Chore {

	return &Chore{
		log:    log,
		db:     db,
		config: config,

		Loop: sync2.NewCycle(config.Interval),
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()
	return chore.Loop.Run(ctx, func(ctx context.Context) error {
		err := chore.RunOnce(ctx)
		if err != nil {
			chore.log.Error("error running block header cleanup chore", zap.Error(err))
		}
		return nil
	})
}

// RunOnce removes old block headers.
func (chore *Chore) RunOnce(ctx context.Context) (err error) {
	pc, _, _, _ := runtime.Caller(0)
	ctx, span := otel.Tracer(os.Getenv("SERVICE_NAME")).Start(ctx, runtime.FuncForPC(pc).Name())
	defer func() {
		span.RecordError(err)
		span.End()
	}()

	if chore.config.RetainDays < 0 {
		return errs.New("retain days cannot be less than 0")
	}

	beforeDays := time.Now().UTC().AddDate(0, 0, -chore.config.RetainDays)
	err = chore.db.DeleteBefore(ctx, beforeDays)
	if err != nil {
		chore.log.Error("error removing old block headers", zap.Error(err))
	}

	return nil
}

// Close stops the chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
