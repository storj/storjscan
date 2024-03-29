// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/zeebo/errs"
)

// Client is a REST client for wallet endpoints.
type Client struct {
	APIKey    string
	APISecret string
	Endpoint  string
}

// NewClient creates a new wallet client from HTTP endpoint address and secret.
func NewClient(endpoint string, apiKey string, secret string) *Client {
	return &Client{
		Endpoint:  endpoint,
		APIKey:    apiKey,
		APISecret: secret,
	}
}

// AddWallets sends claimable generated addresses to the backend.
func (w *Client) AddWallets(ctx context.Context, inserts []InsertWallet) error {
	return w.httpPost(ctx, w.Endpoint+"/api/v0/wallets/", inserts)
}

// httpPost is a helper to submit any post request with proper error handling.
func (w *Client) httpPost(ctx context.Context, url string, request interface{}) (err error) {
	defer mon.Task()(&ctx)(&err)

	body, err := json.Marshal(request)
	if err != nil {
		return errs.Wrap(err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return errs.Wrap(err)
	}
	req.SetBasicAuth(w.APIKey, w.APISecret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return errs.Wrap(err)
	}
	defer func() {
		err = errs.Combine(err, resp.Body.Close())
	}()

	if resp.StatusCode > 200 {
		body, readErr := io.ReadAll(resp.Body)
		err = errs.Combine(errs.New("HTTP status %d for %s, %s", resp.StatusCode, url, string(body)), readErr)
		return
	}
	return
}
