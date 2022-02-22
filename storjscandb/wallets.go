// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"time"

	"github.com/zeebo/errs"
	"storj.io/storjscan/storjscandb/dbx"
)

// ErrWalletsDB indicates about internal wallets DB error.
var ErrWalletsDB = errs.Class("WalletsDB")

// WalletsDB is wallets database dbx postgres implementation.
//
// architecture: Database
type WalletsDB struct {
	db *dbx.DB
}

type Wallet struct {
	Address []byte
	Claimed time.Time
}


func (wallets *WalletsDB) Create(ctx context.Context, address []byte) (Wallet, error){
	w, err := wallets.db.Create_Wallet(ctx, dbx.Wallet_Address(address), dbx.Wallet_Create_Fields{})
	return Wallet{Address: w.Address, Claimed: *w.Claimed}, ErrWalletsDB.Wrap(err)
}

func (wallets *WalletsDB) Claim(ctx context.Context, address []byte) (Wallet, error){
	claimedAt := dbx.Wallet_Claimed(time.Now())
	w, err := wallets.db.Update_Wallet_By_Address(ctx, dbx.Wallet_Address(address), dbx.Wallet_Update_Fields{Claimed: claimedAt})
	return Wallet{Address: w.Address, Claimed: *w.Claimed}, ErrWalletsDB.Wrap(err)
}

func (wallets *WalletsDB) Get(ctx context.Context, address []byte) (Wallet, error){
	w, err := wallets.db.Get_Wallet_By_Address(ctx, dbx.Wallet_Address(address))
	return Wallet{Address: w.Address, Claimed: *w.Claimed}, ErrWalletsDB.Wrap(err)
}

func (wallets *WalletsDB) GetNextAvailable(ctx context.Context)(Wallet, error){
	w, err := wallets.db.First_Wallet_By_Claimed_Equal_False(ctx)
	return Wallet{Address: w.Address, Claimed: *w.Claimed}, ErrWalletsDB.Wrap(err)
}

func (wallets *WalletsDB) CreateBatch(ctx context.Context, addresses [][]byte) (err error) {
	err = wallets.db.WithTx(ctx, func(ctx context.Context, tx *dbx.Tx) error {
		for _, address := range addresses {
			_, err = tx.Create_Wallet(ctx, dbx.Wallet_Address(address), dbx.Wallet_Create_Fields{})
					if err != nil {
						return err
					}
		}
		return err
	})
	return ErrWalletsDB.Wrap(err)
}
