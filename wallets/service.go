// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"
	"time"

	mm "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	acc "github.com/ethereum/go-ethereum/accounts"
	"go.uber.org/zap"
	"storj.io/storjscan/storjscandb"
)

var mon = monkit.Package()

// ErrWalletsService indicates about internal wallets service error.
var ErrWalletsService = errs.Class("WalletsService")

// Wallet is the interface for storj token deposit addresses.
//
// architecture: Service
type Wallets interface {
	// Get returns an unclaimed erc20 token deposit address.
	GetDepositAddress() string
}

// Account ...
type Account struct {
	address []byte
	claimed time.Time
}

// HD implements Wallet interface. Represents hierarchical deterministic wallets.
type HD struct {
	log  *zap.Logger
	db   *storjscandb.WalletsDB
	wallet *mm.Wallet //test only, will be stored offline in prod
}

// NewHD creates a new HD struct
// non-prod only
func NewHD(log *zap.Logger, db *storjscandb.WalletsDB) (*HD, error) {
	seed, err := mm.NewSeed()
	if err != nil {
		return &HD{}, err
	}
	w, err := mm.NewFromSeed(seed)
	if err != nil {
		return &HD{}, err
	}
	return &HD{
		log:  log,
		db:   db,
		wallet: w,
	}, nil

}

// GetNewDepositAddress ...
func (hd *HD) GetNewDepositAddress(ctx context.Context) (address []byte, err error) {
	defer mon.Task()(&ctx)(&err)
	wallet, err := hd.db.GetNextAvailable(ctx)
	//claim here?
	return wallet.Address, ErrWalletsService.Wrap(err)
}

func (hd *HD) newAccount(ctx context.Context) (address []byte, err error) {
	account, err := hd.wallet.Derive(mm.DefaultBaseDerivationPath, false) //pin?
	if err != nil {
		return address, ErrWalletsService.Wrap(err)
	}
	address, err = hd.wallet.AddressBytes(account)
	wallet, err := hd.db.Create(ctx, address)
	return wallet.Address, ErrWalletsService.Wrap(err)
}

func (hd *HD) generateNewBatch(ctx context.Context, size int) error {
	var addresses [][]byte
	next := acc.DefaultIterator(mm.DefaultBaseDerivationPath)
	for i := 0; i < size; i++ {
		account, err := hd.wallet.Derive(next(),false)
		if err != nil {
			continue
		}
		address, err := hd.wallet.AddressBytes(account)
		if err != nil {
			continue
		}
		addresses = append(addresses, address)
	}
	err := hd.db.CreateBatch(ctx, addresses)
	return ErrWalletsService.Wrap(err)
}

func (hd *HD) getAccount(ctx context.Context, address []byte) (account Account, err error) {
	a, err := hd.db.Get(ctx, address)
	return Account{address: a.Address, claimed: a.Claimed}, ErrWalletsService.Wrap(err)
}

func (hd *HD) claim(ctx context.Context, address []byte) (err error) {
	_, err = hd.db.Claim(ctx, address)
	return ErrWalletsService.Wrap(err)
}

func (hd *HD) countTotal(ctx context.Context)  (int, error) {
	total, err := hd.db.TotalCount(ctx)
	return int(total), ErrWalletsService.Wrap(err)
}

func (hd *HD) count(ctx context.Context, claimed bool) (int, error) {
	c, err := hd.db.Count(ctx, claimed)
	return int(c), ErrWalletsService.Wrap(err)
}