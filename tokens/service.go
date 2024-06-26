// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/currency"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/tokenprice"
)

// ErrService - tokens service error class.
var ErrService = errs.Class("tokens service")

// Config holds tokens service configuration.
type Config struct {
	Endpoints string `help:"List of RPC endpoints [{Name:<Name>,URL:<URL>,Contract:<Contract Address>,ChainID:<Chain ID>},...]" devDefault:"[{'Name':'Geth','URL':'http://localhost:8545','Contract':'0xb64ef51c888972c908cfacf59b47c1afbc0ab8ac','ChainID':'1337'}]" releaseDefault:"[{'Name':'Ethereum Mainnet','URL':'/home/storj/.ethereum/geth.ipc','Contract':'0xb64ef51c888972c908cfacf59b47c1afbc0ab8ac','ChainID':'1'}]"`
}

// Service for querying ERC20 token information from ethereum chain.
//
// architecture: Service
type Service struct {
	log          *zap.Logger
	endpoints    []common.EthEndpoint
	headersCache *blockchain.HeadersCache
	eventsCache  *events.Cache
	tokenPrice   *tokenprice.Service
}

// NewService creates new token service instance.
func NewService(
	log *zap.Logger,
	endpoints []common.EthEndpoint,
	headersCache *blockchain.HeadersCache,
	eventsCache *events.Cache,
	tokenPrice *tokenprice.Service) *Service {
	return &Service{
		log:          log,
		endpoints:    endpoints,
		headersCache: headersCache,
		eventsCache:  eventsCache,
		tokenPrice:   tokenPrice,
	}
}

// Payments retrieves all ERC20 token payments across all configured endpoints starting from a particular block per chain for ethereum address.
func (service *Service) Payments(ctx context.Context, address common.Address, from map[int64]int64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for address", zap.String("wallet", address.Hex()))
	return service.payments(ctx, address, from)
}

// AllPayments returns all the payments across all configured endpoints starting from a particular block per chain associated with the current satellite.
func (service *Service) AllPayments(ctx context.Context, satelliteID string, from map[int64]int64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for satellite", zap.String("satelliteID", satelliteID))

	if satelliteID == "" {
		// it shouldn't be possible if auth is properly set up
		return LatestPayments{}, ErrService.New("api identifier is empty")
	}
	return service.payments(ctx, satelliteID, from)
}

func (service *Service) payments(ctx context.Context, identifier interface{}, from map[int64]int64) (_ LatestPayments, err error) {
	var allPayments []Payment
	var latestBlocks []blockchain.Header
	for _, endpoint := range service.endpoints {
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
		latestBlock, err := getCurrentLatestBlock(ctx, endpoint)
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
		payments, err := service.retrievePayments(ctx, endpoint, identifier, from[endpoint.ChainID])
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
		latestBlocks = append(latestBlocks, latestBlock)
		allPayments = append(allPayments, payments...)
	}

	return LatestPayments{
		LatestBlocks: latestBlocks,
		Payments:     allPayments,
	}, nil
}

func (service *Service) retrievePayments(ctx context.Context, endpoint common.EthEndpoint, identifier interface{}, start int64) (_ []Payment, err error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return []Payment{}, ErrService.Wrap(err)
	}
	defer client.Close()

	transferEvents, err := service.eventsCache.GetTransferEvents(ctx, endpoint.ChainID, identifier, uint64(start))
	var payments []Payment
	for _, event := range transferEvents {
		header, err := service.headersCache.Get(ctx, client, event.ChainID, event.BlockHash)
		if err != nil {
			return []Payment{}, ErrService.Wrap(err)
		}
		price, err := service.tokenPrice.PriceAt(ctx, header.Timestamp)
		if err != nil {
			return []Payment{}, ErrService.Wrap(err)
		}

		payments = append(payments, paymentFromEvent(event, header.Timestamp, price))
		service.log.Debug("found payment",
			zap.Int64("Chain ID", payments[len(payments)-1].ChainID),
			zap.String("Transaction Hash", payments[len(payments)-1].Transaction.String()),
			zap.Int64("Block Number", payments[len(payments)-1].BlockNumber),
			zap.Int("Log Index", payments[len(payments)-1].LogIndex),
			zap.String("USD Value", payments[len(payments)-1].USDValue.AsDecimal().String()),
		)
	}
	return payments, ErrService.Wrap(err)
}

func getCurrentLatestBlock(ctx context.Context, endpoint common.EthEndpoint) (_ blockchain.Header, err error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return blockchain.Header{}, ErrService.Wrap(err)
	}
	defer client.Close()

	if err != nil {
		return blockchain.Header{}, ErrService.Wrap(err)
	}

	latestBlock, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return blockchain.Header{}, ErrService.Wrap(err)
	}
	return blockchain.Header{
		ChainID:   endpoint.ChainID,
		Hash:      latestBlock.Hash(),
		Number:    latestBlock.Number.Int64(),
		Timestamp: time.Unix(int64(latestBlock.Time), 0).UTC(),
	}, nil
}

// PingAll checks if configured blockchain services are available for use.
func (service *Service) PingAll(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	for _, endpoint := range service.endpoints {
		err = ping(ctx, endpoint)
		if err != nil {
			return ErrService.Wrap(err)
		}
	}
	return err
}

func ping(ctx context.Context, endpoint common.EthEndpoint) (err error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return err
	}
	defer client.Close()
	// check if service is reachable by getting the latest block
	_, err = client.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	return err
}

// GetChainIds returns the chain ids of the currently configured endpoints.
func (service *Service) GetChainIds(ctx context.Context) (chainIds map[int64]string, err error) {
	defer mon.Task()(&ctx)(&err)
	chainIds = make(map[int64]string)
	for _, endpoint := range service.endpoints {
		chainIds[endpoint.ChainID] = endpoint.Name
	}
	return chainIds, nil
}

// GetEndpoints returns the currently configured endpoints.
func (service *Service) GetEndpoints() []common.EthEndpoint {
	return service.endpoints
}

func paymentFromEvent(event events.TransferEvent, timestamp time.Time, price currency.Amount) Payment {
	return Payment{
		ChainID:     event.ChainID,
		From:        event.From,
		To:          event.To,
		TokenValue:  event.TokenValue,
		USDValue:    tokenprice.CalculateValue(event.TokenValue, price),
		BlockHash:   event.BlockHash,
		BlockNumber: event.BlockNumber,
		Transaction: event.TxHash,
		LogIndex:    event.LogIndex,
		Timestamp:   timestamp,
	}
}
