// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package events

import (
	"context"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/currency"
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

// DB is an ERC20 contract transfer event cache.
//
// architecture: Database
type DB interface {
	// Insert inserts new transfer event to cache db.
	Insert(ctx context.Context, transferEvent []TransferEvent) error
	// GetBySatellite retrieves transfer events for satellite addresses on and after the given block number.
	GetBySatellite(ctx context.Context, chainID int64, satellite string, start uint64) ([]TransferEvent, error)
	// GetByAddress retrieves transfer events for the wallet address on and after the given block number.
	GetByAddress(ctx context.Context, chainID int64, to common.Address, start uint64) ([]TransferEvent, error)
	// GetLatestCachedBlockNumber retrieves the latest block number in the cache for the given chain.
	GetLatestCachedBlockNumber(ctx context.Context, chainID int64) (uint64, error)
	// GetOldestCachedBlockNumber retrieves the oldest block number in the cache for the given chain.
	GetOldestCachedBlockNumber(ctx context.Context, chainID int64) (uint64, error)
	// DeleteBefore deletes all transfer events before the given block number.
	DeleteBefore(ctx context.Context, chainID int64, before uint64) (err error)
	// DeleteBlockAndAfter deletes transfer events from the block number and after.
	DeleteBlockAndAfter(ctx context.Context, chainID int64, block uint64) (err error)
}

// Cache for blockchain transfer events.
type Cache struct {
	log       *zap.Logger
	eventsDB  DB
	walletsDB wallets.DB

	config Config
}

// NewEventsCache creates a new transfer events cache.
func NewEventsCache(log *zap.Logger, eventsDB DB, walletsDB wallets.DB, config Config) *Cache {
	return &Cache{
		log:       log,
		eventsDB:  eventsDB,
		walletsDB: walletsDB,
		config:    config,
	}
}

// GetTransferEvents retrieves transfer events from the cache for the given wallet address or satellite.
func (eventsCache *Cache) GetTransferEvents(ctx context.Context, chainID int64, identifier interface{}, start uint64) ([]TransferEvent, error) {
	switch value := identifier.(type) {
	case string:
		return eventsCache.eventsDB.GetBySatellite(ctx, chainID, value, start)
	case common.Address:
		return eventsCache.eventsDB.GetByAddress(ctx, chainID, value, start)
	}
	return nil, errs.New("invalid identifier type. Must be satellite or address.")
}

// UpdateCache updates the cache with the latest transfer events from the blockchain.
func (eventsCache *Cache) UpdateCache(ctx context.Context, endpoints []common.EthEndpoint) error {

	for _, endpoint := range endpoints {
		latestCachedBlockNumber, err := eventsCache.eventsDB.GetLatestCachedBlockNumber(ctx, endpoint.ChainID)
		if err != nil {
			return err
		}

		latestChainBlockNumber, err := getChainLatestBlockNumber(ctx, endpoint.URL)
		if err != nil {
			return err
		}

		startSearch, err := eventsCache.removePendingBlocks(ctx, latestCachedBlockNumber, latestChainBlockNumber, endpoint.ChainID)
		if err != nil {
			return err
		}

		err = eventsCache.refreshEvents(ctx, endpoint, startSearch, latestChainBlockNumber)
		if err != nil {
			return err
		}
	}
	return nil
}

// removes pending blocks from the cache (if any) and returns with the latest confirmed block number in the cache.
func (eventsCache *Cache) removePendingBlocks(ctx context.Context, latestCachedBlockNumber, latestChainBlockNumber uint64, chainID int64) (_ uint64, err error) {
	startSearch := uint64(0)
	if latestCachedBlockNumber > 0 {
		startSearch = latestCachedBlockNumber + 1
	}
	if latestCachedBlockNumber+eventsCache.config.ChainReorgBuffer > latestChainBlockNumber {
		// need to remove the "pending blocks"
		if startSearch > eventsCache.config.ChainReorgBuffer {
			if latestChainBlockNumber > latestCachedBlockNumber {
				startSearch = startSearch - eventsCache.config.ChainReorgBuffer + (latestChainBlockNumber - latestCachedBlockNumber)
			} else {
				startSearch -= eventsCache.config.ChainReorgBuffer
			}
			err = eventsCache.eventsDB.DeleteBlockAndAfter(ctx, chainID, startSearch)
		} else {
			startSearch = 0
		}
	}
	return startSearch, err
}

func (eventsCache *Cache) refreshEvents(ctx context.Context, endpoint common.EthEndpoint, start, latestChainBlockNumber uint64) error {
	client, err := ethclient.DialContext(ctx, endpoint.URL)
	if err != nil {
		return err
	}
	defer client.Close()

	contractAdress, err := common.AddressFromHex(endpoint.Contract)
	if err != nil {
		return err
	}
	token, err := erc20.NewERC20(contractAdress, client)
	if err != nil {
		return err
	}

	// shouldn't happen, but just in case
	if start > latestChainBlockNumber {
		return nil
	}
	if (latestChainBlockNumber - start) > eventsCache.config.MaximumQuerySize {
		start = latestChainBlockNumber - eventsCache.config.MaximumQuerySize
	}
	for j := 0; j < int(latestChainBlockNumber-start); j += eventsCache.config.BlockBatchSize {
		end := uint64(j + eventsCache.config.BlockBatchSize)
		opts := &bind.FilterOpts{
			Start:   start,
			End:     &end,
			Context: ctx,
		}
		if end > latestChainBlockNumber {
			opts.End = nil
		}

		allWallets, err := eventsCache.walletsDB.ListAll(ctx)
		if err != nil {
			return err
		}
		walletsList := asList(allWallets)

		for i := 0; i < len(walletsList); i += eventsCache.config.AddressBatchSize {
			var addresses []common.Address

			for a := i; a-i < eventsCache.config.AddressBatchSize && a < len(allWallets); a++ {
				addresses = append(addresses, walletsList[a])
			}

			err := eventsCache.processBatch(ctx, token, opts, addresses, endpoint.ChainID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (eventsCache *Cache) processBatch(ctx context.Context, token *erc20.ERC20, opts *bind.FilterOpts, addresses []common.Address, chainID int64) error {
	iter, err := token.FilterTransfer(opts, nil, addresses)
	if err != nil {
		return err
	}
	defer func() { err = errs.Combine(err, errs.Wrap(iter.Close())) }()

	newEvents := make([]TransferEvent, 0)
	for iter.Next() {
		eventsCache.log.Debug("found transfer event",
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
	if err := eventsCache.eventsDB.Insert(ctx, newEvents); err != nil {
		return err
	}
	return nil
}

func getChainLatestBlockNumber(ctx context.Context, url string) (_ uint64, err error) {
	client, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	latestBlock, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	return uint64(latestBlock.Number.Int64()), nil
}

func asList(addresses map[common.Address]string) (res []common.Address) {
	for k := range addresses {
		res = append(res, k)
	}
	return res
}
