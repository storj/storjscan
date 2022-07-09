// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokenprice

import (
	"time"

	"storj.io/storjscan/tokenprice/coinmarketcap"
)

// Config is a configuration struct for the token price service.
type Config struct {
	Interval            time.Duration `help:"how often to run the chore" default:"1m" testDefault:"$TESTINTERVAL"`
	PriceWindow         time.Duration `help:"max allowable duration between the requested and available ticker price timestamps" default:"1m" testDefault:"$TESTPRICEWINDOW"`
	CoinmarketcapConfig coinmarketcap.Config
	UseTestPrices       bool `help:"use test prices instead of coninmaketcap" default:"false"`
}
