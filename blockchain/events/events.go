// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events

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
	"storj.io/storjscan/tokens/erc20"
	"storj.io/storjscan/wallets"
)

// TransferEvent holds a transfer event raised by an ERC20 contract.
type TransferEvent struct {
	ChainID     int64
	From        common.Address
	To          common.Address
	BlockHash   common.Hash
	BlockNumber int64
	TxHash      common.Hash
	LogIndex    int
	TokenValue  currency.Amount
}

// Service for blockchain transfer events.
type Service struct {
	log       *zap.Logger
	walletsDB wallets.DB

	config Config
	// map satellite and chainID to the last scanned block number
	lastScannedBlock map[string]map[int64]int64
}

// NewEventsService creates a new transfer events service.
func NewEventsService(log *zap.Logger, walletsDB wallets.DB, config Config) *Service {
	lastScannedBlock := make(map[string]map[int64]int64)
	return &Service{
		log:              log,
		walletsDB:        walletsDB,
		config:           config,
		lastScannedBlock: lastScannedBlock,
	}
}

// GetForSatellite returns with the latest transfer events from the blockchain for a given satellite.
func (events *Service) GetForSatellite(ctx context.Context, endpoints []common.EthEndpoint, satelliteID string, from map[int64]int64) (map[int64]blockchain.Header, []TransferEvent, error) {
	lastScan := events.lastScannedBlock[satelliteID]
	for chain, block := range lastScan {
		if from[chain] < block {
			from[chain] = block
		}
	}
	wallets, err := events.walletsDB.ListBySatellite(ctx, satelliteID)
	if err != nil {
		return nil, nil, err
	}
	walletsList := make([]common.Address, 0, len(wallets))
	for wallet := range wallets {
		walletsList = append(walletsList, wallet)
	}
	updatedScannedBlocks, newEvents, err := events.getEvents(ctx, endpoints, walletsList, from)
	if err != nil {
		return nil, nil, err
	}

	for chain, block := range updatedScannedBlocks {
		if events.lastScannedBlock[satelliteID] == nil {
			events.lastScannedBlock[satelliteID] = make(map[int64]int64)
		}
		if block.Number > int64(events.config.ChainReorgBuffer) {
			events.lastScannedBlock[satelliteID][chain] = block.Number - int64(events.config.ChainReorgBuffer)
		} else {
			events.lastScannedBlock[satelliteID][chain] = 0
		}
	}
	return updatedScannedBlocks, newEvents, nil
}

// GetForAddress returns with the latest transfer events from the blockchain for a given address.
func (events *Service) GetForAddress(ctx context.Context, endpoints []common.EthEndpoint, address []common.Address, from map[int64]int64) (map[int64]blockchain.Header, []TransferEvent, error) {
	return events.getEvents(ctx, endpoints, address, from)
}

func (events *Service) getEvents(ctx context.Context, endpoints []common.EthEndpoint, address []common.Address, from map[int64]int64) (map[int64]blockchain.Header, []TransferEvent, error) {
	scannedBlocks := make(map[int64]blockchain.Header)
	newEvents := make([]TransferEvent, 0)
	for _, endpoint := range endpoints {
		latestChainBlockHeader, err := getChainLatestBlockHeader(ctx, endpoint.URL, endpoint.ChainID)
		if err != nil {
			events.log.Error("failed to get latest block number", zap.String("URL", endpoint.URL))
			return nil, nil, err
		}

		endpointEvents, err := events.getEventsForEndpoint(ctx, endpoint, uint64(from[endpoint.ChainID]), uint64(latestChainBlockHeader.Number), address)
		if err != nil {
			events.log.Error("failed to refresh events", zap.String("URL", endpoint.URL))
			return nil, nil, err
		}
		scannedBlocks[endpoint.ChainID] = latestChainBlockHeader
		newEvents = append(newEvents, endpointEvents...)
	}
	return scannedBlocks, newEvents, nil
}

func (events *Service) getEventsForEndpoint(ctx context.Context, endpoint common.EthEndpoint, start, latestChainBlockNumber uint64, walletsList []common.Address) ([]TransferEvent, error) {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	contractAdress, err := common.AddressFromHex(endpoint.Contract)
	if err != nil {
		return nil, err
	}
	token, err := erc20.NewERC20(contractAdress, client)
	if err != nil {
		events.log.Error("failed to bind to ERC20 contract", zap.String("Contract", contractAdress.Hex()), zap.String("URL", endpoint.URL))
		return nil, err
	}

	// shouldn't happen, but just in case
	if start > latestChainBlockNumber {
		return nil, nil
	}
	if (latestChainBlockNumber - start) > uint64(events.config.MaximumQuerySize) {
		start = latestChainBlockNumber - uint64(events.config.MaximumQuerySize)
	}
	newEvents := make([]TransferEvent, 0)
	for j := int(start); j < int(latestChainBlockNumber); j += events.config.BlockBatchSize {
		end := uint64(j + events.config.BlockBatchSize)
		opts := &bind.FilterOpts{
			Start:   uint64(j),
			End:     &end,
			Context: ctx,
		}
		if end > latestChainBlockNumber {
			opts.End = nil
		}

		for i := 0; i < len(walletsList); i += events.config.AddressBatchSize {
			var addresses []common.Address

			for a := i; a-i < events.config.AddressBatchSize && a < len(walletsList); a++ {
				addresses = append(addresses, walletsList[a])
			}

			batchEvents, err := events.processBatch(token, opts, addresses, endpoint.ChainID)
			if err != nil {
				return nil, err
			}
			newEvents = append(newEvents, batchEvents...)
		}
	}
	return newEvents, nil
}

func (events *Service) processBatch(token *erc20.ERC20, opts *bind.FilterOpts, addresses []common.Address, chainID int64) ([]TransferEvent, error) {
	iter, err := token.FilterTransfer(opts, nil, addresses)
	if err != nil {
		events.log.Error("failed to search for transfer events", zap.Int64("Chain ID", chainID))
		return nil, err
	}
	defer func() { err = errs.Combine(err, errs.Wrap(iter.Close())) }()

	newEvents := make([]TransferEvent, 0)
	for iter.Next() {
		events.log.Debug("found transfer event",
			zap.Int64("Chain ID", chainID),
			zap.String("From", iter.Event.From.String()),
			zap.String("To", iter.Event.To.String()),
			zap.String("Transaction Hash", iter.Event.Raw.TxHash.String()),
			zap.Uint64("Block Number", iter.Event.Raw.BlockNumber),
			zap.Int("Log Index", int(iter.Event.Raw.Index)),
		)
		tokenValue := currency.AmountFromBaseUnits(iter.Event.Value.Int64(), currency.StorjToken)
		newEvents = append(newEvents, TransferEvent{
			ChainID:     chainID,
			From:        iter.Event.From,
			To:          iter.Event.To,
			BlockHash:   iter.Event.Raw.BlockHash,
			BlockNumber: int64(iter.Event.Raw.BlockNumber),
			TxHash:      iter.Event.Raw.TxHash,
			LogIndex:    int(iter.Event.Raw.Index),
			TokenValue:  tokenValue,
		})
	}
	return newEvents, nil
}

func getChainLatestBlockHeader(ctx context.Context, url string, chainID int64) (_ blockchain.Header, err error) {
	client, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return blockchain.Header{}, err
	}
	defer client.Close()

	latestBlock, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return blockchain.Header{}, err
	}
	return blockchain.Header{
		Hash:      latestBlock.Hash(),
		Number:    latestBlock.Number.Int64(),
		ChainID:   chainID,
		Timestamp: time.Unix(int64(latestBlock.Time), 0).UTC(),
	}, nil
}
