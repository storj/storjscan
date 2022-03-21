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

// headersDB is headers database cache dbx postgres implementation.
//
// architecture: Database
type headersDB struct {
	db *dbx.DB
}

// Insert inserts new block header into db.
func (headers *headersDB) Insert(ctx context.Context, hash blockchain.Hash, number int64, timestamp time.Time) error {
	_, err := headers.db.Create_BlockHeader(ctx,
		dbx.BlockHeader_Hash(hash.Bytes()),
		dbx.BlockHeader_Number(number),
		dbx.BlockHeader_Timestamp(timestamp.UTC()))

	return ErrHeadersDB.Wrap(err)
}

// Delete deletes block header from the db by block hash.
func (headers *headersDB) Delete(ctx context.Context, hash blockchain.Hash) error {
	deleted, err := headers.db.Delete_BlockHeader_By_Hash(ctx, dbx.BlockHeader_Hash(hash.Bytes()))
	if err != nil {
		return ErrHeadersDB.Wrap(err)
	}
	if !deleted {
		return blockchain.ErrNoHeader
	}
	return nil
}

// Get retrieves single block header from the db by block hash.
func (headers *headersDB) Get(ctx context.Context, hash blockchain.Hash) (blockchain.Header, error) {
	dbxHeader, err := headers.db.Get_BlockHeader_By_Hash(ctx, dbx.BlockHeader_Hash(hash.Bytes()))
	if err != nil {
		if errs.Is(err, sql.ErrNoRows) {
			return blockchain.Header{}, blockchain.ErrNoHeader
		}

		return blockchain.Header{}, ErrHeadersDB.Wrap(err)
	}

	return fromDBXHeader(dbxHeader), nil
}

// GetByNumber retrieves single block header from the db by block number.
func (headers *headersDB) GetByNumber(ctx context.Context, number int64) (blockchain.Header, error) {
	dbxHeader, err := headers.db.Get_BlockHeader_By_Number(ctx, dbx.BlockHeader_Number(number))
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
		Hash:      blockchain.HashFromBytes(dbxHeader.Hash),
		Number:    dbxHeader.Number,
		Timestamp: dbxHeader.Timestamp.UTC(),
	}
}
