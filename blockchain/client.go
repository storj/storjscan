// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package blockchain

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/zeebo/errs"
)

// ErrClient is client error class.
var ErrClient = errs.Class("Client")

// Client is ethereum rpc client for making batch block header requests.
type Client struct {
	conn *rpc.Client
}

// Dial dials endpoint and initiates new rpc client.
func Dial(ctx context.Context, endpoint string) (*Client, error) {
	c, err := rpc.DialContext(ctx, endpoint)
	if err != nil {
		return nil, err
	}

	return &Client{conn: c}, nil
}

// newClient creates new instance of client from rpc client.
func newClient(c *rpc.Client) *Client {
	return &Client{conn: c}
}

// ListBackwards list block headers backwards till limit is reached in one batch.
func (client *Client) ListBackwards(ctx context.Context, blockNumber, limit int64) ([]Header, error) {
	var batch batchRequest

	for i := blockNumber; i > blockNumber-limit; i-- {
		batch.Add(i)
	}
	headers, err := client.batchCall(ctx, batch)
	if err != nil {
		return nil, ErrClient.Wrap(err)
	}

	return headers, nil
}

// ListForward list block headers forward till limit is reached in one batch.
func (client *Client) ListForward(ctx context.Context, blockNumber, limit int64) ([]Header, error) {
	var batch batchRequest

	for i := blockNumber; i < blockNumber+limit; i++ {
		batch.Add(i)
	}
	headers, err := client.batchCall(ctx, batch)
	if err != nil {
		return nil, ErrClient.Wrap(err)
	}

	return headers, nil
}

// batchRequest holds batch elements and headers needed to execute batch call.
type batchRequest struct {
	Elements []rpc.BatchElem
	Headers  []*types.Header
}

// Add adds new block header to batch request.
func (batch *batchRequest) Add(number int64) {
	blockNumber := new(big.Int).SetInt64(number)
	header := new(types.Header)

	elem := rpc.BatchElem{
		Method: "eth_getBlockByNumber",
		Args:   []interface{}{hexutil.EncodeBig(blockNumber), false},
		Result: header,
	}
	batch.Elements = append(batch.Elements, elem)
	batch.Headers = append(batch.Headers, header)
}

// batchCall executes block header batch request. Fails if any request returned error.
func (client *Client) batchCall(ctx context.Context, batch batchRequest) ([]Header, error) {
	err := client.conn.BatchCallContext(ctx, batch.Elements)
	if err != nil {
		return nil, err
	}

	for _, elem := range batch.Elements {
		err = errs.Combine(err, elem.Error)
		if err != nil {
			continue
		}
	}
	if err != nil {
		return nil, err
	}

	var list []Header
	for _, header := range batch.Headers {
		list = append(list, Header{
			Hash:      header.Hash(),
			Number:    header.Number.Int64(),
			Timestamp: time.Unix(int64(header.Time), 0),
		})
	}

	return list, nil
}
