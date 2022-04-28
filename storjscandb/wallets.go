// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"time"

	"github.com/zeebo/errs"

	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/storjscandb/dbx"
	"storj.io/storjscan/wallets"
)

// ErrWalletsDB indicates about internal wallets DB error.
var ErrWalletsDB = errs.Class("WalletsDB")

// ensures that walletsDB implements wallets.DB.
var _ wallets.DB = (*walletsDB)(nil)

// walletsDB is wallets database dbx postgres implementation that stores deposit address information. Implements wallets.DB.
//
// architecture: Database
type walletsDB struct {
	db *dbx.DB
}

// Insert adds a new entry in the wallets table. Info can be an empty string.
func (wdb *walletsDB) Insert(ctx context.Context, satellite string, address blockchain.Address, info string) (*wallets.Wallet, error) {
	var optional dbx.Wallet_Create_Fields
	if info != "" {
		optional = dbx.Wallet_Create_Fields{Info: dbx.Wallet_Info(info)}
	}
	w, err := wdb.db.Create_Wallet(ctx, dbx.Wallet_Address(address.Bytes()), dbx.Wallet_Satellite(satellite), optional)
	if err != nil {
		return nil, ErrWalletsDB.Wrap(err)
	}
	addr, err := blockchain.AddressFromBytes(w.Address)
	if err != nil {
		return nil, ErrWalletsDB.Wrap(err)
	}
	return &wallets.Wallet{Address: addr}, nil
}

// InsertBatch adds a new db entry for each address. Entries is a string map of address:info. Info can be an empty string.
func (wdb *walletsDB) InsertBatch(ctx context.Context, satellite string, entries map[blockchain.Address]string) error {
	err := wdb.db.WithTx(ctx, func(ctx context.Context, tx *dbx.Tx) error {
		var err error
		var optional dbx.Wallet_Create_Fields
		for address, info := range entries {
			if info != "" {
				optional = dbx.Wallet_Create_Fields{Info: dbx.Wallet_Info(info)}
			}
			_, err := tx.Create_Wallet(ctx, dbx.Wallet_Address(address.Bytes()), dbx.Wallet_Satellite(satellite), optional)
			if err != nil {
				return err
			}
		}
		return err
	})
	return ErrWalletsDB.Wrap(err)
}

// Claim claims and returns the first unclaimed wallet address.
func (wdb *walletsDB) Claim(ctx context.Context, satellite string) (*wallets.Wallet, error) {
	var dbxw *dbx.Wallet
	err := wdb.db.WithTx(ctx, func(ctx context.Context, tx *dbx.Tx) error {
		w1, err := tx.First_Wallet_By_Claimed_Is_Null_And_Satellite(ctx, dbx.Wallet_Satellite(satellite))
		if err != nil {
			return err
		}
		if w1 == nil {
			return wallets.ErrNoAvailableWallets
		}
		w2, err := tx.Update_Wallet_By_Address_And_Satellite(ctx,
			dbx.Wallet_Address(w1.Address),
			dbx.Wallet_Satellite(satellite),
			dbx.Wallet_Update_Fields{
				Claimed: dbx.Wallet_Claimed(time.Now()),
			})
		if err != nil {
			return err
		}
		if w2 == nil {
			return wallets.ErrUpdateWallet
		}
		dbxw = w2
		return nil
	})
	if err != nil {
		return nil, ErrWalletsDB.Wrap(err)
	}
	addr, err := blockchain.AddressFromBytes(dbxw.Address)
	if err != nil {
		return nil, ErrWalletsDB.Wrap(err)
	}
	return &wallets.Wallet{
		Address:   addr,
		Claimed:   *dbxw.Claimed,
		Satellite: dbxw.Satellite,
		Info:      *dbxw.Info,
		CreatedAt: dbxw.CreatedAt,
	}, nil
}

// Get queries the wallets table for the information stored for a given address.
func (wdb *walletsDB) Get(ctx context.Context, satellite string, address blockchain.Address) (*wallets.Wallet, error) {
	w, err := wdb.db.Get_Wallet_By_Address_And_Satellite(ctx, dbx.Wallet_Address(address.Bytes()), dbx.Wallet_Satellite(satellite))
	if err != nil {
		return nil, ErrWalletsDB.Wrap(err)
	}
	return &wallets.Wallet{
		Address:   address,
		Claimed:   *w.Claimed,
		Satellite: w.Satellite,
		Info:      *w.Info,
		CreatedAt: w.CreatedAt,
	}, nil
}

// GetStats returns information about the wallets table.
func (wdb *walletsDB) GetStats(ctx context.Context) (stats *wallets.Stats, err error) {
	err = wdb.db.WithTx(ctx, func(ctx context.Context, tx *dbx.Tx) error {
		total, err := tx.Count_Wallet_Address(ctx)
		if err != nil {
			return err
		}
		claimed, err := tx.Count_Wallet_By_Claimed_IsNot_Null(ctx)
		if err != nil {
			return err
		}
		unclaimed := total - claimed
		stats = &wallets.Stats{
			TotalCount:     int(total),
			ClaimedCount:   int(claimed),
			UnclaimedCount: int(unclaimed),
		}
		return nil
	})
	return stats, ErrWalletsDB.Wrap(err)
}

// ListBySatellite returns addresses claimed by a certain satellite.
func (wdb *walletsDB) ListBySatellite(ctx context.Context, satellite string) (map[blockchain.Address]string, error) {
	var accounts = make(map[blockchain.Address]string)
	rows, err := wdb.db.All_Wallet_By_Satellite_And_Claimed_IsNot_Null(ctx, dbx.Wallet_Satellite(satellite))
	if err != nil {
		return accounts, ErrWalletsDB.Wrap(err)
	}
	var errList error
	for _, r := range rows {
		addr, err := blockchain.AddressFromBytes(r.Address)
		if err != nil {
			errList = errs.Combine(errList, ErrWalletsDB.Wrap(err))
			continue
		}

		info := ""
		if r.Info != nil {
			info = *r.Info
		}
		accounts[addr] = info
	}
	return accounts, errList
}
