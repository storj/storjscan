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
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokens/erc20"
	"storj.io/storjscan/wallets"
)

// ErrService - tokens service error class.
var ErrService = errs.Class("tokens service")

// Config holds tokens service configuration.
type Config struct {
	Endpoint string `help:"Ethereum RPC endpoint" devDefault:"http://localhost:8545" releaseDefault:"/home/storj/.ethereum/geth.ipc"`
	Contract string `help:"Address of the STORJ token to scan for transactions" default:"0xb64ef51c888972c908cfacf59b47c1afbc0ab8ac"`
}

// Service for querying ERC20 token information from ethereum chain.
//
// architecture: Service
type Service struct {
	log        *zap.Logger
	endpoint   string
	token      blockchain.Address
	headers    *blockchain.HeadersCache
	walletDB   wallets.DB
	tokenPrice *tokenprice.Service
	batchSize  int
}

// NewService creates new token service instance.
func NewService(
	log *zap.Logger,
	endpoint string,
	token blockchain.Address,
	cache *blockchain.HeadersCache,
	walletDB wallets.DB,
	tokenPrice *tokenprice.Service,
	batchSize int) *Service {
	return &Service{
		log:        log,
		endpoint:   endpoint,
		token:      token,
		headers:    cache,
		walletDB:   walletDB,
		tokenPrice: tokenPrice,
		batchSize:  batchSize,
	}
}

// Payments retrieves all ERC20 token payments starting from particular block for ethereum address.
func (service *Service) Payments(ctx context.Context, address blockchain.Address, from int64) (_ []Payment, err error) {
	defer mon.Task()(&ctx)(&err)

	client, err := ethclient.DialContext(ctx, service.endpoint)
	if err != nil {
		return nil, ErrService.Wrap(err)
	}
	defer client.Close()

	token, err := erc20.NewERC20(service.token, client)
	if err != nil {
		return nil, ErrService.Wrap(err)
	}

	opts := &bind.FilterOpts{
		Start:   uint64(from),
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
		header, err := service.headers.Get(ctx, client, iter.Event.Raw.BlockHash)
		if err != nil {
			return nil, ErrService.Wrap(err)
		}
		price, err := service.tokenPrice.PriceAt(ctx, header.Timestamp)
		if err != nil {
			return nil, ErrService.Wrap(err)
		}

		payments = append(payments, paymentFromEvent(iter.Event, header.Timestamp, price))

	}

	return payments, ErrService.Wrap(errs.Combine(err, iter.Error(), iter.Close()))
}

// AllPayments returns all the payments associated with the current satellite.
func (service *Service) AllPayments(ctx context.Context, satelliteID string, from int64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)

	if satelliteID == "" {
		// it shouldn't be possible if auth is properly set up
		return LatestPayments{}, ErrService.New("api identifier is empty")
	}

	walletsOfSatellite, err := service.walletDB.ListBySatellite(ctx, satelliteID)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}
	client, err := ethclient.DialContext(ctx, service.endpoint)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}
	defer client.Close()

	token, err := erc20.NewERC20(service.token, client)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}
	latestBlock, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}

	var allPayments []Payment
	allWallets := asList(walletsOfSatellite)
	end := latestBlock.Number.Uint64()

	// query the rpc API in batches
	for i := 0; i < len(allWallets); i += service.batchSize {

		var addresses []blockchain.Address

		for a := i; a-i < service.batchSize && a < len(allWallets); a++ {
			addresses = append(addresses, allWallets[a])
		}

		opts := &bind.FilterOpts{
			Start:   uint64(from),
			End:     &end,
			Context: ctx,
		}
		iter, err := token.FilterTransfer(opts, nil, addresses)
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}

		for iter.Next() {
			header, err := service.headers.Get(ctx, client, iter.Event.Raw.BlockHash)
			if err != nil {
				return LatestPayments{}, ErrService.Wrap(errs.Combine(err, iter.Close()))
			}
			price, err := service.tokenPrice.PriceAt(ctx, header.Timestamp)
			if err != nil {
				return LatestPayments{}, ErrService.Wrap(errs.Combine(err, iter.Close()))
			}

			allPayments = append(allPayments, paymentFromEvent(iter.Event, header.Timestamp, price))
		}

		if err := errs.Combine(iter.Error(), iter.Close()); err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
	}

	header := blockchain.Header{
		Hash:      latestBlock.Hash(),
		Number:    latestBlock.Number.Int64(),
		Timestamp: time.Unix(int64(latestBlock.Time), 0).UTC(),
	}
	return LatestPayments{
		LatestBlock: header,
		Payments:    allPayments,
	}, nil
}

// Ping checks that blockchain service is available for use.
func (service *Service) Ping(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	client, err := ethclient.DialContext(ctx, service.endpoint)
	if err != nil {
		return err
	}
	defer client.Close()
	// check if service is reachable by getting the latest block
	_, err = client.HeaderByNumber(ctx, nil)
	return err
}

func paymentFromEvent(event *erc20.ERC20Transfer, timestamp time.Time, price float64) Payment {
	return Payment{
		From:        event.From,
		To:          event.To,
		TokenValue:  event.Value,
		USDValue:    tokenprice.CalculateValue(event.Value, price),
		BlockHash:   event.Raw.BlockHash,
		BlockNumber: int64(event.Raw.BlockNumber),
		Transaction: event.Raw.TxHash,
		LogIndex:    int(event.Raw.Index),
		Timestamp:   timestamp,
	}
}

func asList(addresses map[blockchain.Address]string) (res []blockchain.Address) {
	for k := range addresses {
		res = append(res, k)
	}
	return res
}
