// Copyright (C) 2024 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"time"

	"github.com/zeebo/errs"

	"storj.io/common/currency"
	"storj.io/private/dbutil/pgutil"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/storjscandb/dbx"
)

// ErrEventsDB indicates about internal log transfer events DB error.
var ErrEventsDB = errs.Class("EventsDB")

// ensures that eventsDB implements events.DB.
var _ events.DB = (*eventsDB)(nil)

// eventsDB contains access to the database that stores log transfer events.
//
// architecture: Database
type eventsDB struct {
	db *dbx.DB
}

func (eventsDB eventsDB) Insert(ctx context.Context, transferEvent []events.TransferEvent) (err error) {
	defer mon.Task()(&ctx)(&err)

	cmd := `INSERT INTO transfer_events(
				chain_id,
				block_hash,
				block_number,
				transaction,
				log_index,
				from_address,
				to_address,
				token_value,
				created_at
			) SELECT
				UNNEST($1::INT8[]),
				UNNEST($2::BYTEA[]),
				UNNEST($3::INT8[]),
				UNNEST($4::BYTEA[]),
				UNNEST($5::INT4[]),
				UNNEST($6::BYTEA[]),
				UNNEST($7::BYTEA[]),
				UNNEST($8::INT8[]),
				$9
			ON CONFLICT (chain_id, block_hash, log_index)
			  DO UPDATE SET
			      block_number = EXCLUDED.block_number,
			      transaction = EXCLUDED.transaction,
			      from_address = EXCLUDED.from_address,
			      to_address = EXCLUDED.to_address,
			      token_value = EXCLUDED.token_value,
			      created_at = EXCLUDED.created_at
			  `
	var (
		chainIDs      = make([]int64, 0, len(transferEvent))
		blockHashes   = make([][]byte, 0, len(transferEvent))
		blockNumbers  = make([]int64, 0, len(transferEvent))
		transactions  = make([][]byte, 0, len(transferEvent))
		logIndexes    = make([]int32, 0, len(transferEvent))
		fromAddresses = make([][]byte, 0, len(transferEvent))
		toAddresses   = make([][]byte, 0, len(transferEvent))
		tokenValues   = make([]int64, 0, len(transferEvent))

		createdAt = time.Now()
	)
	for i := range transferEvent {
		event := transferEvent[i]
		chainIDs = append(chainIDs, int64(event.ChainID))
		blockHashes = append(blockHashes, event.BlockHash[:])
		blockNumbers = append(blockNumbers, int64(event.BlockNumber))
		transactions = append(transactions, event.TxHash[:])
		logIndexes = append(logIndexes, int32(event.LogIndex))
		fromAddresses = append(fromAddresses, event.From[:])
		toAddresses = append(toAddresses, event.To[:])
		tokenValues = append(tokenValues, event.TokenValue.BaseUnits())
	}

	_, err = eventsDB.db.ExecContext(ctx, cmd,
		pgutil.Int8Array(chainIDs),
		pgutil.ByteaArray(blockHashes),
		pgutil.Int8Array(blockNumbers),
		pgutil.ByteaArray(transactions),
		pgutil.Int4Array(logIndexes),
		pgutil.ByteaArray(fromAddresses),
		pgutil.ByteaArray(toAddresses),
		pgutil.Int8Array(tokenValues),
		createdAt)
	return err
}

func (eventsDB eventsDB) GetBySatellite(ctx context.Context, chainID uint64, satellite string, start uint64) (_ []events.TransferEvent, err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return []events.TransferEvent{}, ErrEventsDB.New("invalid chainID 0 specified")
	}
	query := `SELECT * FROM transfer_events WHERE chain_id = $1 AND block_number >= $2 AND to_address IN (SELECT address FROM wallets WHERE satellite = $3 AND claimed IS NOT NULL) ORDER BY block_number ASC`
	dbxEvents, err := eventsDB.db.QueryContext(ctx, query, chainID, start, satellite)
	if err != nil {
		return nil, ErrEventsDB.Wrap(err)
	}
	defer func() { err = errs.Combine(err, dbxEvents.Close()) }()

	var list []events.TransferEvent
	for dbxEvents.Next() {
		var dbxEvent dbx.TransferEvent
		if err := dbxEvents.Scan(&dbxEvent.ChainId, &dbxEvent.BlockHash, &dbxEvent.BlockNumber, &dbxEvent.Transaction,
			&dbxEvent.LogIndex, &dbxEvent.FromAddress, &dbxEvent.ToAddress, &dbxEvent.TokenValue, &dbxEvent.CreatedAt); err != nil {
			return nil, ErrEventsDB.Wrap(err)
		}
		list = append(list, fromDBXEvent(&dbxEvent))
	}
	return list, dbxEvents.Err()
}

func (eventsDB eventsDB) GetByAddress(ctx context.Context, chainID uint64, to common.Address, start uint64) (_ []events.TransferEvent, err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return []events.TransferEvent{}, ErrEventsDB.New("invalid chainID 0 specified")
	}
	dbxEvents, err := eventsDB.db.All_TransferEvent_By_ChainId_And_ToAddress_And_BlockNumber_GreaterOrEqual_OrderBy_Asc_BlockNumber(ctx,
		dbx.TransferEvent_ChainId(int64(chainID)),
		dbx.TransferEvent_ToAddress(to[:]),
		dbx.TransferEvent_BlockNumber(int64(start)))
	if err != nil {
		return nil, ErrEventsDB.Wrap(err)
	}

	var list []events.TransferEvent
	for _, dbxEvent := range dbxEvents {
		list = append(list, fromDBXEvent(dbxEvent))
	}

	return list, nil
}

func (eventsDB eventsDB) GetLatestCachedBlockNumber(ctx context.Context, chainID uint64) (_ uint64, err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return 0, ErrEventsDB.New("invalid chainID 0 specified")
	}
	dbxEvent, err := eventsDB.db.First_TransferEvent_BlockNumber_By_ChainId_OrderBy_Desc_BlockNumber(ctx, dbx.TransferEvent_ChainId(int64(chainID)))
	if dbxEvent == nil {
		return 0, nil
	}
	if err != nil {
		return 0, ErrEventsDB.Wrap(err)
	}
	return uint64(dbxEvent.BlockNumber), nil
}

func (eventsDB eventsDB) GetOldestCachedBlockNumber(ctx context.Context, chainID uint64) (_ uint64, err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return 0, ErrEventsDB.New("invalid chainID 0 specified")
	}
	dbxEvent, err := eventsDB.db.First_TransferEvent_BlockNumber_By_ChainId_OrderBy_Asc_BlockNumber(ctx, dbx.TransferEvent_ChainId(int64(chainID)))
	if dbxEvent == nil {
		return 0, nil
	}
	if err != nil {
		return 0, ErrEventsDB.Wrap(err)
	}
	return uint64(dbxEvent.BlockNumber), nil
}

func (eventsDB eventsDB) DeleteBlockAndAfter(ctx context.Context, chainID uint64, block uint64) (err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return ErrEventsDB.New("invalid chainID 0 specified")
	}
	_, err = eventsDB.db.Delete_TransferEvent_By_ChainId_And_BlockNumber_GreaterOrEqual(ctx, dbx.TransferEvent_ChainId(int64(chainID)), dbx.TransferEvent_BlockNumber(int64(block)))
	return ErrEventsDB.Wrap(err)
}

func (eventsDB eventsDB) DeleteBefore(ctx context.Context, chainID uint64, before uint64) (err error) {
	defer mon.Task()(&ctx)(&err)

	if chainID == 0 {
		return ErrEventsDB.New("invalid chainID 0 specified")
	}
	_, err = eventsDB.db.Delete_TransferEvent_By_ChainId_And_BlockNumber_Less(ctx, dbx.TransferEvent_ChainId(int64(chainID)), dbx.TransferEvent_BlockNumber(int64(before)))
	return ErrEventsDB.Wrap(err)
}

// fromDBXEvent converts dbx log transfer event to blockchain.TransferEvent type.
func fromDBXEvent(dbxEvent *dbx.TransferEvent) events.TransferEvent {
	return events.TransferEvent{
		ChainID:     uint64(dbxEvent.ChainId),
		From:        common.Address(dbxEvent.FromAddress),
		To:          common.Address(dbxEvent.ToAddress),
		BlockHash:   common.Hash(dbxEvent.BlockHash),
		BlockNumber: uint64(dbxEvent.BlockNumber),
		TxHash:      common.Hash(dbxEvent.Transaction),
		LogIndex:    uint(dbxEvent.LogIndex),
		TokenValue:  currency.AmountFromBaseUnits(dbxEvent.TokenValue, currency.StorjToken),
	}
}
