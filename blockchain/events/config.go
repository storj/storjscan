// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events

import "time"

// Config is a configuration struct for the transfer events cache.
type Config struct {
	CacheRefreshInterval time.Duration `help:"how often to check for new transfer events logs" default:"5s" testDefault:"$TESTINTERVAL"`

	AddressBatchSize int    `help:"number of Addresses to fetch new events for in a single request" default:"100"`
	BlockBatchSize   int    `help:"number of blocks to fetch new events for in a single request" default:"5000"`
	ChainReorgBuffer uint64 `help:"minimum number of blocks to re-query for when looking for new transfer events" default:"15"`
	MaximumQuerySize uint64 `help:"maximum number of blocks prior to the latest block that storjscan can query for" default:"10000"`
}
