// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package coinmarketcap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/zeebo/errs"
)

// ErrClient is an error class for coinmarketcap API client error.
var ErrClient = errs.Class("Client")

const (
	// storjID is the permanent CoinMarketCap ID associated with STORJ token.
	storjID = "1772"
	// usdSymbol is the ticker symbol for U.S. Dollars.
	usdSymbol = "USD"
)

// Config holds coinmarketcap configuration.
type Config struct {
	BaseURL string        `help:"base URL for ticker price API" default:"https://pro-api.coinmarketcap.com" testDefault:"$TESTBASEURL"`
	APIKey  string        `help:"API Key used to access coinmarketcap" default:"" testDefault:"$TESTAPIKEY"`
	Timeout time.Duration `help:"coinmarketcap API response timeout" default:"10s" testDefault:"$TESTTIMEOUT"`
}

// Client is used to query the coinmarketcap API for the STORJ token price.
// implements tokenprice.Client interface.
type Client struct {
	httpClient *http.Client
	baseURL    string
	apiKey     string
}

// NewClient returns a new token price client.
func NewClient(config Config) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		baseURL: config.BaseURL,
		apiKey:  config.APIKey,
	}
}

// GetLatestPrice gets the latest available ticker price.
// todo - verify fields in status, and add alerts.
func (c *Client) GetLatestPrice(ctx context.Context) (time.Time, float64, error) {
	q := url.Values{}
	q.Add("id", storjID)
	q.Add("convert", usdSymbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/cryptocurrency/quotes/latest", nil)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}

	req.Header.Set("Accepts", "application/json")
	req.Header.Add("X-CMC_PRO_API_KEY", c.apiKey)
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}

	defer func() { err = errs.Combine(ErrClient.Wrap(err), resp.Body.Close()) }()

	var formattedResp quoteLatestResponse

	if err = json.NewDecoder(resp.Body).Decode(&formattedResp); err != nil {
		return time.Time{}, 0, ErrClient.New("error decoding response body: %s. server returned status code: %d", err, resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		if formattedResp.Status.ErrorMessage != "" {
			return time.Time{}, 0, ErrClient.New("server returned error code: %d - %s", formattedResp.Status.ErrorCode, formattedResp.Status.ErrorMessage)
		}
		return time.Time{}, 0, ErrClient.New("unexpected status code: %d", resp.StatusCode)
	}

	timestamp, err := time.Parse(time.RFC3339Nano, formattedResp.Data[storjID].Quote[usdSymbol].LastUpdated)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}
	return timestamp, formattedResp.Data[storjID].Quote[usdSymbol].Price, nil
}

// GetPriceAt gets the ticker price at the specified time.
// todo - verify fields in status, and add alerts.
func (c *Client) GetPriceAt(ctx context.Context, requestedTimestamp time.Time) (time.Time, float64, error) {
	q := url.Values{}
	q.Add("id", storjID)
	q.Add("convert", usdSymbol)
	q.Add("time_start", strconv.FormatInt(requestedTimestamp.UnixMilli(), 10))
	q.Add("time_end", strconv.FormatInt(requestedTimestamp.UnixMilli()+1, 10))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/cryptocurrency/quotes/historical", nil)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}

	req.Header.Set("Accepts", "application/json")
	req.Header.Add("X-CMC_PRO_API_KEY", c.apiKey)
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}

	defer func() { err = errs.Combine(ErrClient.Wrap(err), resp.Body.Close()) }()

	var formattedResp quoteHistoricResponse

	if err = json.NewDecoder(resp.Body).Decode(&formattedResp); err != nil {
		return time.Time{}, 0, ErrClient.New("error decoding response body: %s. server returned status code: %d", err, resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		if formattedResp.Status.ErrorMessage != "" {
			return time.Time{}, 0, ErrClient.New("server returned error code: %d - %s", formattedResp.Status.ErrorCode, formattedResp.Status.ErrorMessage)
		}
		return time.Time{}, 0, ErrClient.New("unexpected status code: %d", resp.StatusCode)
	}

	returnedTimestamp, err := time.Parse(time.RFC3339Nano, formattedResp.Data[storjID].Quotes[0].Quote[usdSymbol].Timestamp)
	if err != nil {
		return time.Time{}, 0, ErrClient.Wrap(err)
	}
	return returnedTimestamp, formattedResp.Data[storjID].Quotes[0].Quote[usdSymbol].Price, nil
}

// Ping checks that the coinmarketcap third-party api is available for use.
func (c *Client) Ping(ctx context.Context) (statusCode int, err error) {
	q := url.Values{}
	q.Add("id", storjID)
	q.Add("convert", usdSymbol)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/key/info", nil)
	if err != nil {
		return statusCode, err
	}

	req.Header.Set("Accepts", "application/json")
	req.Header.Add("X-CMC_PRO_API_KEY", c.apiKey)

	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return http.StatusServiceUnavailable, err
	}

	return resp.StatusCode, resp.Body.Close()
}

// TestClient implements the Client interface for test purposes (bypassing coinmarketcap 3rd party api calls).
type TestClient struct{}

// NewTestClient returns a new test token price client.
func NewTestClient() *TestClient {
	return &TestClient{}
}

// GetLatestPrice gets the latest available ticker price.
func (tc *TestClient) GetLatestPrice(ctx context.Context) (time.Time, float64, error) {
	return time.Now(), 1, nil
}

// GetPriceAt gets the ticker price at the specified time.
func (tc *TestClient) GetPriceAt(ctx context.Context, requestedTimestamp time.Time) (time.Time, float64, error) {
	return requestedTimestamp, 1, nil
}

// Ping checks that the api is available for use.
func (tc *TestClient) Ping(ctx context.Context) (statusCode int, err error) {
	return http.StatusOK, nil
}
