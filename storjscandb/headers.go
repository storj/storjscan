// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"database/sql"
	"time"

	"github.com/zeebo/errs"

	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/storjscandb/dbx"
)

// ErrHeadersDB indicates about internal headers DB error.
var ErrHeadersDB = errs.Class("HeadersDB")

// ensures that headersDB implements blockchain.HeadersDB.
var _ blockchain.HeadersDB = (*headersDB)(nil)

// headersDB contains access to the database that stores blockchain headers.
//
// architecture: Database
type headersDB struct {
	db *dbx.DB
}

// Insert inserts new block header into db.
func (headers *headersDB) Insert(ctx context.Context, header blockchain.Header) error {
	if header.ChainID == 0 {
		return ErrHeadersDB.New("invalid chainID 0 specified")
	}
	_, err := headers.db.Create_BlockHeader(ctx,
		dbx.BlockHeader_ChainId(header.ChainID),
		dbx.BlockHeader_Hash(header.Hash.Bytes()),
		dbx.BlockHeader_Number(header.Number),
		dbx.BlockHeader_Timestamp(header.Timestamp.UTC()))

	return ErrHeadersDB.Wrap(err)
}

// Delete deletes block header from the db by block hash.
func (headers *headersDB) Delete(ctx context.Context, chainID int64, hash blockchain.Hash) error {
	if chainID == 0 {
		return ErrHeadersDB.New("invalid chainID 0 specified")
	}
	deleted, err := headers.db.Delete_BlockHeader_By_ChainId_And_Hash(ctx,
		dbx.BlockHeader_ChainId(chainID),
		dbx.BlockHeader_Hash(hash.Bytes()))
	if err != nil {
		return ErrHeadersDB.Wrap(err)
	}
	if !deleted {
		return blockchain.ErrNoHeader
	}
	return nil
}

// DeleteBefore deletes headers before the given time.
func (headers *headersDB) DeleteBefore(ctx context.Context, before time.Time) (err error) {
	_, err = headers.db.Delete_BlockHeader_By_Timestamp_Less(ctx, dbx.BlockHeader_Timestamp(before.UTC()))
	return ErrHeadersDB.Wrap(err)
}

// Get retrieves single block header from the db by block hash.
func (headers *headersDB) Get(ctx context.Context, chainID int64, hash blockchain.Hash) (blockchain.Header, error) {
	if chainID == 0 {
		return blockchain.Header{}, ErrHeadersDB.New("invalid chainID 0 specified")
	}
	dbxHeader, err := headers.db.Get_BlockHeader_By_ChainId_And_Hash(ctx,
		dbx.BlockHeader_ChainId(chainID),
		dbx.BlockHeader_Hash(hash.Bytes()))
	if err != nil {
		if errs.Is(err, sql.ErrNoRows) {
			return blockchain.Header{}, blockchain.ErrNoHeader
		}

		return blockchain.Header{}, ErrHeadersDB.Wrap(err)
	}

	return fromDBXHeader(dbxHeader), nil
}

// GetByNumber retrieves single block header from the db by block number.
func (headers *headersDB) GetByNumber(ctx context.Context, chainID int64, number int64) (blockchain.Header, error) {
	if chainID == 0 {
		return blockchain.Header{}, ErrHeadersDB.New("invalid chainID 0 specified")
	}
	dbxHeader, err := headers.db.Get_BlockHeader_By_ChainId_And_Number(ctx,
		dbx.BlockHeader_ChainId(chainID),
		dbx.BlockHeader_Number(number))
	if err != nil {
		if errs.Is(err, sql.ErrNoRows) {
			return blockchain.Header{}, blockchain.ErrNoHeader
		}

		return blockchain.Header{}, ErrHeadersDB.Wrap(err)
	}

	return fromDBXHeader(dbxHeader), nil
}

// List retrieves all block headers from the db.
func (headers *headersDB) List(ctx context.Context) ([]blockchain.Header, error) {
	dbxHeaders, err := headers.db.All_BlockHeader_OrderBy_Desc_Timestamp(ctx)
	if err != nil {
		return nil, ErrHeadersDB.Wrap(err)
	}

	var list []blockchain.Header
	for _, dbxHeader := range dbxHeaders {
		list = append(list, fromDBXHeader(dbxHeader))
	}

	return list, nil
}

// fromDBXHeader converts dbx block header to blockchain.Header type.
func fromDBXHeader(dbxHeader *dbx.BlockHeader) blockchain.Header {
	return blockchain.Header{
		ChainID:   dbxHeader.ChainId,
		Hash:      blockchain.HashFromBytes(dbxHeader.Hash),
		Number:    dbxHeader.Number,
		Timestamp: dbxHeader.Timestamp.UTC(),
	}
}
