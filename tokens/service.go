// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/tokens/erc20"
)

// ErrService - tokens service error class.
var ErrService = errs.Class("tokens service")

// Config holds tokens service configuration.
type Config struct {
	Endpoint string
	Contract string
}

// Service for querying ERC20 token information from ethereum chain.
//
// architecture: Service
type Service struct {
	log      *zap.Logger
	endpoint string
	token    blockchain.Address
}

// NewService creates new token service instance.
func NewService(log *zap.Logger, endpoint string, token blockchain.Address) *Service {
	return &Service{
		log:      log,
		endpoint: endpoint,
		token:    token,
	}
}

// Payments retrieves all ERC20 token payments for ethereum address.
func (service *Service) Payments(ctx context.Context, address blockchain.Address) (_ []Payment, err error) {
	defer mon.Task()(&ctx)(&err)

	client, err := ethclient.DialContext(ctx, service.endpoint)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	token, err := erc20.NewERC20(service.token, client)
	if err != nil {
		return nil, ErrService.Wrap(err)
	}

	opts := &bind.FilterOpts{
		Start:   0,
		End:     nil,
		Context: ctx,
	}
	iter, err := token.FilterTransfer(opts, nil, []common.Address{address})
	if err != nil {
		return nil, ErrService.Wrap(err)
	}
	defer func() { err = errs.Combine(err, ErrService.Wrap(iter.Close())) }()

	var payments []Payment
	for iter.Next() {
		header, err := client.HeaderByHash(ctx, iter.Event.Raw.BlockHash)
		if err != nil {
			return payments, ErrService.Wrap(err)
		}

		payments = append(payments, Payment{
			From:        iter.Event.From,
			TokenValue:  iter.Event.Value,
			Transaction: iter.Event.Raw.TxHash,
			Timestamp:   time.Unix(int64(header.Time), 0),
		})
	}

	return payments, ErrService.Wrap(iter.Error())
}
