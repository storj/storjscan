// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package coinmarketcaptest

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"storj.io/storjscan/tokenprice/coinmarketcap"
)

const (
	// TestURLEndpoint is the endpoint for testing queries to coinmarketcap API.
	TestURLEndpoint = "https://sandbox-api.coinmarketcap.com"
)

// GetConfig get a standard coinmarketcap configuration.
func GetConfig(t *testing.T) coinmarketcap.Config {
	return coinmarketcap.Config{
		Timeout: time.Second * 5,
		BaseURL: TestURLEndpoint,
		APIKey:  PickAPIKey(t),
	}
}

// APIKey is the api key for requesting data from the coinmarketcap API.
var apiKey = flag.String("coinmarketcap-api-key", os.Getenv("COINMARKETCAP_API_KEY"), "coinmarketcap api key, \"omit\" is used to omit the tests from output")

// TB interface to skip the current test.
type TB interface {
	Skip(...interface{})
}

// PickAPIKey picks one coinmarketcap api key from flag.
func PickAPIKey(t TB) string {
	if *apiKey == "" || strings.EqualFold(*apiKey, "omit") {
		t.Skip("coinmarketcap api key flag missing, example: -COINMARKETCAP_API_KEY=<coinmarketcap api key>")
	}
	return *apiKey
}
