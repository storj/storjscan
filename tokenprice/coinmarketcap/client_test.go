// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package coinmarketcap_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"storj.io/common/testcontext"
	"storj.io/storjscan/tokenprice/coinmarketcap"
	"storj.io/storjscan/tokenprice/coinmarketcaptest"
)

type errorResponse struct {
	Status struct {
		ErrorCode    int
		ErrorMessage string
	}
	Data []interface{}
}

func TestClientGetLatestPrice(t *testing.T) {
	ctx := testcontext.New(t)
	client := coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t))
	time, price, err := client.GetLatestPrice(ctx)
	require.NoError(t, err)
	require.NotNil(t, time)
	require.NotNil(t, price)
}

func TestClientGetLatestPriceBadUrl(t *testing.T) {
	ctx := testcontext.New(t)
	client := coinmarketcap.NewClient(getConfigBadURL(t))
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
	client := coinmarketcap.NewClient(getConfigBadKey(ts.URL))
	_, _, err := client.GetLatestPrice(ctx)
	require.Error(t, err)
}

func TestClientGetPriceAt(t *testing.T) {
	ctx := testcontext.New(t)
	client := coinmarketcap.NewClient(coinmarketcaptest.GetConfig(t))
	time, price, err := client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, time)
	require.NotNil(t, price)
}

func TestClientGetPriceAtBadUrl(t *testing.T) {
	ctx := testcontext.New(t)
	client := coinmarketcap.NewClient(getConfigBadURL(t))
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
	client := coinmarketcap.NewClient(getConfigBadKey(ts.URL))
	_, _, err := client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.Error(t, err)
}

func getErrorResponseBadKey() errorResponse {
	var response errorResponse
	response.Status.ErrorCode = 1001
	response.Status.ErrorMessage = "This API Key is invalid."
	response.Data = []interface{}{}
	return response
}

// getConfigBadURL get a coinmarketcap configuration with a bad URL.
func getConfigBadURL(t *testing.T) coinmarketcap.Config {
	return coinmarketcap.Config{
		Timeout: time.Second * 5,
		BaseURL: "http://this.wont.work:1234",
		APIKey:  coinmarketcaptest.PickAPIKey(t),
	}
}

// getConfigBadKey get a coinmarketcap configuration with a bad API key.
func getConfigBadKey(url string) coinmarketcap.Config {
	return coinmarketcap.Config{
		Timeout: time.Second * 5,
		BaseURL: url,
		APIKey:  "123abc",
	}
}

func Test_TestClient(t *testing.T) {
	ctx := testcontext.New(t)
	client := coinmarketcap.NewTestClient()
	ts, price, err := client.GetLatestPrice(ctx)
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, float64(1), price)
	ts, price, err = client.GetPriceAt(ctx, time.Now().Add(-5*time.Minute))
	require.NoError(t, err)
	require.NotNil(t, ts)
	require.Equal(t, float64(1), price)
}
