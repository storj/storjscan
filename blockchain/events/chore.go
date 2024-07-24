// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package events

import (
	"context"
	"time"

	"github.com/spacemonkeygo/monkit/v3"
	"go.uber.org/zap"

	"storj.io/common/sync2"
	"storj.io/storjscan/common"
)

var mon = monkit.Package()

// Chore to update the log transfer events cache.
//
// architecture: Chore
type Chore struct {
	log         *zap.Logger
	endpoints   []common.EthEndpoint
	eventsCache *Cache

	Loop *sync2.Cycle
}

// NewChore creates a new chore for updating the log transfer events cache.
func NewChore(log *zap.Logger, eventsCache *Cache, endpoints []common.EthEndpoint, refreshInterval time.Duration) *Chore {

	return &Chore{
		log:         log,
		endpoints:   endpoints,
		eventsCache: eventsCache,

		Loop: sync2.NewCycle(refreshInterval),
	}
}

// Run starts the chore.
func (chore *Chore) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return chore.Loop.Run(ctx, func(ctx context.Context) error {
		err := chore.RunOnce(ctx)
		if err != nil {
			chore.log.Error("error running log transfer events cache chore", zap.Error(err))
		}
		return nil
	})
}

// RunOnce updates the log transfer events cache.
func (chore *Chore) RunOnce(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return nil
}

// Close stops the chore.
func (chore *Chore) Close() error {
	chore.Loop.Close()
	return nil
}
