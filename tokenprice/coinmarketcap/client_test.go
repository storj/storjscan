// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package coinmarketcap

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
)

const (
	testURLEndpoint = "https://sandbox-api.coinmarketcap.com"
)

func getenv(priority ...string) string {
	for _, p := range priority {
		v := os.Getenv(p)
		if v != "" {
			return v
		}
	}
	return ""
}

// apiKey is the api key for requesting data from the coinmarketcap API.
var apiKey = flag.String("coinmarketcap-api-key", getenv("COINMARKETCAP_API_KEY"), "coinmarketcap api key, \"omit\" is used to omit the tests from output")

type TB interface {
	Skip(...interface{})
}

func TestClientGetLatestPrice(t *testing.T) {
	ctx := testcontext.New(t)
	client := NewClient(testURLEndpoint, pickAPIKey(t), &http.Client{Timeout: time.Second * 5})
	time, price, err := client.GetLatestPrice(ctx)
	require.NoError(t, err)
	require.NotNil(t, time)
	require.NotNil(t, price)
}

func TestClientGetLatestPriceBadUrl(t *testing.T) {
	ctx := testcontext.New(t)
	client := NewClient("http://this.wont.work:1234", "123abc", &http.Client{Timeout: time.Second * 5})
	_, _, err := client.GetLatestPrice(ctx)
	require.Error(t, err)
}

func TestClientGetLatestPriceBadKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		err := json.NewEncoder(w).Encode(getErrorResponseBadKey())
		require.NoError(t, err)
	}))
	defer ts.Close()

	ctx := testcontext.New(t)
	client := NewClient(ts.URL, pickAPIKey(t), &http.Client{Timeout: time.Second * 5})
	_, _, err := client.GetLatestPrice(ctx)
	require.Error(t, err)
}

func TestClientGetPriceAt(t *testing.T) {
	ctx := testcontext.New(t)
	client := NewClient(testURLEndpoint, pickAPIKey(t), &http.Client{Timeout: time.Second * 5})
	time, price, err := client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, time)
	require.NotNil(t, price)
}

func TestClientGetPriceAtBadUrl(t *testing.T) {
	ctx := testcontext.New(t)
	client := NewClient("http://this.wont.work:1234", "123abc", &http.Client{Timeout: time.Second * 5})
	_, _, err := client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.Error(t, err)
}

func TestClientGetPriceAtBadKey(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		err := json.NewEncoder(w).Encode(getErrorResponseBadKey())
		require.NoError(t, err)
	}))
	defer ts.Close()

	ctx := testcontext.New(t)
	client := NewClient(ts.URL, pickAPIKey(t), &http.Client{Timeout: time.Second * 5})
	_, _, err := client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.Error(t, err)
}

// pickAPIKey picks one coinmarketcap api key from flag.
func pickAPIKey(t TB) string {
	if *apiKey == "" || strings.EqualFold(*apiKey, "omit") {
		t.Skip("coinmarketcap api key flag missing, example: -COINMARKETCAP_API_KEY=<coinmarketcap api key>")
	}
	return *apiKey
}

func getErrorResponseBadKey() *quoteLatestResponse {
	errMessage := "This API Key is invalid."
	return &quoteLatestResponse{
		Status: status{
			ErrorCode:    1001,
			ErrorMessage: errMessage,
		},
		Data: map[string]quoteLatestData{},
	}
}
