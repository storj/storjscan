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
	events       *events.Service
	tokenPrice   *tokenprice.Service
}

// NewService creates new token service instance.
func NewService(
	log *zap.Logger,
	endpoints []common.EthEndpoint,
	headersCache *blockchain.HeadersCache,
	events *events.Service,
	tokenPrice *tokenprice.Service) *Service {
	return &Service{
		log:          log,
		endpoints:    endpoints,
		headersCache: headersCache,
		events:       events,
		tokenPrice:   tokenPrice,
	}
}

// Payments retrieves all ERC20 token payments across all configured endpoints starting from a particular block per chain for ethereum address.
func (service *Service) Payments(ctx context.Context, address common.Address, from map[uint64]uint64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for address", zap.String("wallet", address.Hex()))
	lastestBlocks, newEvents, err := service.events.GetForAddress(ctx, service.endpoints, []common.Address{address}, from)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}
	return service.toPayments(ctx, lastestBlocks, newEvents)
}

// AllPayments returns all the payments across all configured endpoints starting from a particular block per chain associated with the current satellite.
func (service *Service) AllPayments(ctx context.Context, satelliteID string, from map[uint64]uint64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for satellite", zap.String("satelliteID", satelliteID))
	lastestBlocks, newEvents, err := service.events.GetForSatellite(ctx, service.endpoints, satelliteID, from)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}
	return service.toPayments(ctx, lastestBlocks, newEvents)
}

func (service *Service) toPayments(ctx context.Context, scannedBlocks map[uint64]blockchain.Header, newEvents []events.TransferEvent) (_ LatestPayments, err error) {
	var allPayments []Payment
	var latestBlocks []blockchain.Header
	for _, endpoint := range service.endpoints {
		payments, err := service.toPaymentsForEndpoint(ctx, endpoint, newEvents)
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
		latestBlocks = append(latestBlocks, scannedBlocks[endpoint.ChainID])
		allPayments = append(allPayments, payments...)
	}

	return LatestPayments{
		LatestBlocks: latestBlocks,
		Payments:     allPayments,
	}, nil
}

func (service *Service) toPaymentsForEndpoint(ctx context.Context, endpoint common.EthEndpoint, newEvents []events.TransferEvent) (_ []Payment, err error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return []Payment{}, ErrService.Wrap(err)
	}
	defer client.Close()

	var payments []Payment
	for _, event := range newEvents {
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
			zap.Uint64("Chain ID", payments[len(payments)-1].ChainID),
			zap.String("Transaction Hash", payments[len(payments)-1].Transaction.String()),
			zap.Uint64("Block Number", payments[len(payments)-1].BlockNumber),
			zap.Uint("Log Index", payments[len(payments)-1].LogIndex),
			zap.String("USD Value", payments[len(payments)-1].USDValue.AsDecimal().String()),
		)
	}
	return payments, ErrService.Wrap(err)
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
func (service *Service) GetChainIds(ctx context.Context) (chainIds map[uint64]string, err error) {
	defer mon.Task()(&ctx)(&err)
	chainIds = make(map[uint64]string)
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
