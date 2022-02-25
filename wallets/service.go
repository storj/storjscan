// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"
	"time"

	acc "github.com/ethereum/go-ethereum/accounts"
	mm "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"storj.io/storjscan/storjscandb"
)

var mon = monkit.Package()

// ErrWalletsService indicates about internal wallets service error.
var ErrWalletsService = errs.Class("Wallets Service")

// Wallet is the interface for storj token deposit addresses.
//
// architecture: Service
type Wallets interface {
	// GetNewDepositAddress returns the next unclaimed deposit address and claims it.
	GetNewDepositAddress(ctx context.Context) ([]byte, error)
	// CountTotal returns the total number of deposit addresses.
	GetCountDepositAddresses(ctx context.Context) (int, error)
	// GetCountClaimedDepositAddresses returns the number of claimed or unclaimed deposit addresses.
	GetCountClaimedDepositAddresses(ctx context.Context, claimed bool) (int, error)
	// GetAccount returns the info related to an address
	GetAccount(ctx context.Context, address []byte) (Account, error)
	// Setup is used to create wallets for test purposes
	Setup(ctx context.Context, size int) ([]byte, error)
}

// Account represents an account within the overarching hd wallet.
type Account struct {
	Address []byte
	Claimed *time.Time
}

// HD implements Wallet interface. Represents hierarchical deterministic wallets. Production Version.
type HD struct {
	log *zap.Logger
	db  *storjscandb.WalletsDB
}

// NewHD creates a new HD struct.
func NewHD(log *zap.Logger, db *storjscandb.WalletsDB) (*HD, error) {
	return &HD{
		log: log,
		db:  db,
	}, nil
}

// GetNewDepositAddress returns the next unclaimed deposit address and claims it.
func (hd *HD) GetNewDepositAddress(ctx context.Context) (address []byte, err error) {
	defer mon.Task()(&ctx)(&err)
	wallet, err := hd.db.GetNextAvailable(ctx)
	if err != nil {
		return address, ErrWalletsService.Wrap(err)
	}
	_, err = hd.db.Claim(ctx, wallet.Address)
	return wallet.Address, ErrWalletsService.Wrap(err)
}

// CountTotal returns the total number of deposit addresses.
func (hd *HD) GetCountDepositAddresses(ctx context.Context) (int, error) {
	total, err := hd.db.TotalCount(ctx)
	return int(total), ErrWalletsService.Wrap(err)
}

// Count returns the number of claimed or unclaimed deposit addresses.
func (hd *HD) GetCountClaimedDepositAddresses(ctx context.Context, claimed bool) (int, error) {
	c, err := hd.db.Count(ctx, claimed)
	return int(c), ErrWalletsService.Wrap(err)
}

// GetAccount returns the info related to an address.
func (hd *HD) GetAccount(ctx context.Context, address []byte) (account Account, err error) {
	a, err := hd.db.Get(ctx, address)
	return Account{Address: a.Address, Claimed: &a.Claimed}, ErrWalletsService.Wrap(err)
}

// Setup is used to create wallets for test purposes.
func (hd *HD) Setup(ctx context.Context, size int) (firstAddr []byte, err error) {
	return firstAddr, err
}

//--- Implementation for testing ---//

// HD_test implements Wallet interface. Represents hierarchical deterministic wallets.
type HD_test struct {
	log    *zap.Logger
	db     *storjscandb.WalletsDB
	wallet *mm.Wallet
}

// NewHD_test creates a new HD_test struct.
func NewHD_test(log *zap.Logger, db *storjscandb.WalletsDB) (*HD_test, error) {
	seed, err := mm.NewSeed()
	if err != nil {
		return &HD_test{}, err
	}
	w, err := mm.NewFromSeed(seed)
	if err != nil {
		return &HD_test{}, err
	}
	return &HD_test{
		log:    log,
		db:     db,
		wallet: w,
	}, nil
}

// GetNewDepositAddress returns the next unclaimed deposit address and claims it.
func (hd *HD_test) GetNewDepositAddress(ctx context.Context) (address []byte, err error) {
	defer mon.Task()(&ctx)(&err)
	wallet, err := hd.db.GetNextAvailable(ctx)
	if err != nil {
		return address, ErrWalletsService.Wrap(err)
	}
	_, err = hd.db.Claim(ctx, wallet.Address)
	return wallet.Address, ErrWalletsService.Wrap(err)
}

// CountTotal returns the total number of deposit addresses.
func (hd *HD_test) GetCountDepositAddresses(ctx context.Context) (int, error) {
	total, err := hd.db.TotalCount(ctx)
	return int(total), ErrWalletsService.Wrap(err)
}

// Count returns the number of claimed or unclaimed deposit addresses.
func (hd *HD_test) GetCountClaimedDepositAddresses(ctx context.Context, claimed bool) (int, error) {
	c, err := hd.db.Count(ctx, claimed)
	return int(c), ErrWalletsService.Wrap(err)
}

// GetAccount returns the info related to an address.
func (hd *HD_test) GetAccount(ctx context.Context, address []byte) (account Account, err error) {
	a, err := hd.db.Get(ctx, address)
	return Account{Address: a.Address, Claimed: &a.Claimed}, ErrWalletsService.Wrap(err)
}

// Setup is used to create wallets for test purposes.
// Is there a better way to do this?
func (hd *HD_test) Setup(ctx context.Context, size int) (firstAddr []byte, err error) {
	firstAddr, err = hd.generateNewAccounts(ctx, size)
	return firstAddr, ErrWalletsService.Wrap(err)
}

//--- helper methods for hd_test. NB: similar functions will be in a command line tool for production. ---/
func (hd *HD_test) generateNewAccounts(ctx context.Context, size int) (firstAddr []byte, err error) {
	var addresses [][]byte
	next := acc.DefaultIterator(mm.DefaultBaseDerivationPath)
	for i := 0; i < size; i++ {
		account, err := hd.wallet.Derive(next(), false) //do we want to pin these accounts to the wallet?
		if err != nil {
			continue
		}
		address, err := hd.wallet.AddressBytes(account)
		if err != nil {
			continue
		}
		addresses = append(addresses, address)
	}
	if len(addresses) < 1 {
		return firstAddr, ErrWalletsService.New("no addresses created")
	}
	err = hd.db.CreateBatch(ctx, addresses)
	firstAddr = addresses[0]
	return firstAddr, ErrWalletsService.Wrap(err)
}
