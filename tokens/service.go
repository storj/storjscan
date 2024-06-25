// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

import (
	"context"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/currency"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/common"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokens/erc20"
	"storj.io/storjscan/wallets"
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
	log        *zap.Logger
	endpoints  []common.EthEndpoint
	headers    *blockchain.HeadersCache
	walletDB   wallets.DB
	tokenPrice *tokenprice.Service
	batchSize  int
}

// NewService creates new token service instance.
func NewService(
	log *zap.Logger,
	endpoints []common.EthEndpoint,
	cache *blockchain.HeadersCache,
	walletDB wallets.DB,
	tokenPrice *tokenprice.Service,
	batchSize int) *Service {
	return &Service{
		log:        log,
		endpoints:  endpoints,
		headers:    cache,
		walletDB:   walletDB,
		tokenPrice: tokenPrice,
		batchSize:  batchSize,
	}
}

// Payments retrieves all ERC20 token payments across all configured endpoints starting from a particular block per chain for ethereum address.
func (service *Service) Payments(ctx context.Context, address common.Address, from map[int64]int64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for address", zap.String("wallet", address.Hex()))

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
		payments, err := service.retrievePaymentsForAddresses(ctx, endpoint, []common.Address{address}, from[endpoint.ChainID], uint64(latestBlock.Number))
		if err != nil {
			return LatestPayments{}, ErrService.Wrap(err)
		}
		allPayments = append(allPayments, payments...)
		latestBlocks = append(latestBlocks, latestBlock)
	}

	return LatestPayments{
		LatestBlocks: latestBlocks,
		Payments:     allPayments,
	}, nil
}

// AllPayments returns all the payments across all configured endpoints starting from a particular block per chain associated with the current satellite.
func (service *Service) AllPayments(ctx context.Context, satelliteID string, from map[int64]int64) (_ LatestPayments, err error) {
	defer mon.Task()(&ctx)(&err)
	service.log.Debug("payments request received for satellite", zap.String("satelliteID", satelliteID))

	if satelliteID == "" {
		// it shouldn't be possible if auth is properly set up
		return LatestPayments{}, ErrService.New("api identifier is empty")
	}
	walletsOfSatellite, err := service.walletDB.ListBySatellite(ctx, satelliteID)
	if err != nil {
		return LatestPayments{}, ErrService.Wrap(err)
	}

	allWallets := asList(walletsOfSatellite)
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
		// query the rpc API in batches
		for i := 0; i < len(allWallets); i += service.batchSize {
			var addresses []common.Address

			for a := i; a-i < service.batchSize && a < len(allWallets); a++ {
				addresses = append(addresses, allWallets[a])
			}

			payments, err := service.retrievePaymentsForAddresses(ctx, endpoint, addresses, from[endpoint.ChainID], uint64(latestBlock.Number))
			if err != nil {
				return LatestPayments{}, ErrService.Wrap(err)
			}
			allPayments = append(allPayments, payments...)
		}
		latestBlocks = append(latestBlocks, latestBlock)
	}

	return LatestPayments{
		LatestBlocks: latestBlocks,
		Payments:     allPayments,
	}, nil
}

// retrievePaymentsForAddresses retrieves all ERC20 token payments for given addresses from start block to end block.
// a nil end block means the latest block.
func (service *Service) retrievePaymentsForAddresses(ctx context.Context, endpoint common.EthEndpoint, addresses []common.Address, start int64, end uint64) (_ []Payment, err error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return []Payment{{}}, ErrService.Wrap(err)
	}
	defer client.Close()

	if err != nil {
		return []Payment{{}}, ErrService.Wrap(err)
	}

	opts := &bind.FilterOpts{
		Start:   uint64(start),
		End:     &end,
		Context: ctx,
	}

	contract, err := common.AddressFromHex(endpoint.Contract)
	if err != nil {
		return []Payment{{}}, ErrService.Wrap(err)
	}

	token, err := erc20.NewERC20(contract, client)
	if err != nil {
		return []Payment{{}}, ErrService.Wrap(err)
	}

	iter, err := token.FilterTransfer(opts, nil, addresses)
	if err != nil {
		return []Payment{{}}, ErrService.Wrap(err)
	}
	defer func() { err = errs.Combine(err, ErrService.Wrap(iter.Close())) }()

	var payments []Payment
	for iter.Next() {
		header, err := service.headers.Get(ctx, client, endpoint.ChainID, iter.Event.Raw.BlockHash)
		if err != nil {
			return []Payment{{}}, ErrService.Wrap(err)
		}
		price, err := service.tokenPrice.PriceAt(ctx, header.Timestamp)
		if err != nil {
			return []Payment{{}}, ErrService.Wrap(err)
		}

		payments = append(payments, paymentFromEvent(endpoint.ChainID, iter.Event, header.Timestamp, price))
		service.log.Debug("found payment",
			zap.Int64("Chain ID", payments[len(payments)-1].ChainID),
			zap.String("Transaction Hash", payments[len(payments)-1].Transaction.String()),
			zap.Int64("Block Number", payments[len(payments)-1].BlockNumber),
			zap.Int("Log Index", payments[len(payments)-1].LogIndex),
			zap.String("USD Value", payments[len(payments)-1].USDValue.AsDecimal().String()),
		)
	}
	return payments, ErrService.Wrap(errs.Combine(err, iter.Error(), iter.Close()))
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

func paymentFromEvent(chainID int64, event *erc20.ERC20Transfer, timestamp time.Time, price currency.Amount) Payment {
	tokenValue := currency.AmountFromBaseUnits(event.Value.Int64(), currency.StorjToken)
	return Payment{
		ChainID:     chainID,
		From:        event.From,
		To:          event.To,
		TokenValue:  tokenValue,
		USDValue:    tokenprice.CalculateValue(tokenValue, price),
		BlockHash:   event.Raw.BlockHash,
		BlockNumber: int64(event.Raw.BlockNumber),
		Transaction: event.Raw.TxHash,
		LogIndex:    int(event.Raw.Index),
		Timestamp:   timestamp,
	}
}

func asList(addresses map[common.Address]string) (res []common.Address) {
	for k := range addresses {
		res = append(res, k)
	}
	return res
}
