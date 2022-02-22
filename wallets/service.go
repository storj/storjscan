// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"
	"time"

	mm "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"storj.io/storjscan/storjscandb"
)

var mon = monkit.Package()

// ErrWalletsService indicates about internal wallets service error.
var ErrWalletsService = errs.Class("WalletsService")

// Wallet is ...
//
// architecture: ...
type Wallets interface {
	// Get returns an unclaimed erc20 token deposit address
	GetDepositAddress() string
}

type Wallet struct {
	address []byte
	claimed time.Time
}

// HD implements Wallet interface
type HD struct {
	log  *zap.Logger
	db   *storjscandb.WalletsDB
	seed []byte //test only, seed will be stored offline in prod
}

func NewHD(log *zap.Logger, db *storjscandb.WalletsDB) (*HD, error) {
	seed, err := mm.NewSeed()
	if err != nil {
		return &HD{}, err
	}
	return &HD{
		log:  log,
		db:   db,
		seed: seed,
	}, nil

}
func (hd *HD) GetNewDepositAddress(ctx context.Context) (address []byte, err error) {
	defer mon.Task()(&ctx)(&err)
	wallet, err := hd.db.GetNextAvailable(ctx)
	//claim here?
	return wallet.Address, ErrWalletsService.Wrap(err)
}

func (hd *HD) newWallet(ctx context.Context) (address []byte, err error) {
	address, err = hd.newWalletHelper()
	if err != nil {
		return address, ErrWalletsService.Wrap(err)
	}
	wallet, err := hd.db.Create(ctx, address)
	return wallet.Address, ErrWalletsService.Wrap(err)
}

func (hd *HD) generateNewBatch(ctx context.Context, size int) error {
	var addresses [][]byte
	for i := 0; i < size; i++ {
		address, err := hd.newWalletHelper()
		if err != nil {
			continue
		}
		addresses = append(addresses, address)
	}
	err := hd.db.CreateBatch(ctx, addresses)
	return ErrWalletsService.Wrap(err)
}

func (hd *HD) newWalletHelper() (address []byte, err error) {
	w, err := mm.NewFromSeed(hd.seed)
	if err != nil {
		return address, ErrWalletsService.Wrap(err)
	}
	accounts := w.Accounts()
	address, err = w.AddressBytes(accounts[0])
	return address, ErrWalletsService.Wrap(err)
}

func (hd *HD) getWallet(ctx context.Context, address []byte) (wallet Wallet, err error) {
	w, err := hd.db.Get(ctx, address)
	return Wallet{address: w.Address, claimed: w.Claimed}, ErrWalletsService.Wrap(err)
}

func (hd *HD) claim(ctx context.Context, address []byte) (err error) {
	_, err = hd.db.Claim(ctx, address)
	return ErrWalletsService.Wrap(err)
}
