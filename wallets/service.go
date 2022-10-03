// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan/blockchain"
)

var mon = monkit.Package()

// ErrWalletsService indicates about internal wallets service error.
var ErrWalletsService = errs.Class("Wallets Service")

// Stats represents the high level information about the wallets table.
type Stats struct {
	TotalCount     int
	ClaimedCount   int
	UnclaimedCount int
}

// Service for querying and updating wallets information.
//
// architecture: Service
type Service struct {
	log *zap.Logger
	db  DB
}

// NewService initializes a wallets service instance.
func NewService(log *zap.Logger, db DB) (*Service, error) {
	return &Service{
		log: log,
		db:  db,
	}, nil
}

// Claim claims the next unclaimed deposit address.
func (service *Service) Claim(ctx context.Context, satellite string) (_ blockchain.Address, err error) {
	defer mon.Task()(&ctx)(&err)
	wallet, err := service.db.Claim(ctx, satellite)
	if err != nil {
		return blockchain.Address{}, ErrWalletsService.Wrap(err)
	}
	service.log.Debug("new wallet claimed")
	return wallet.Address, nil
}

// Get returns information related to an address.
func (service *Service) Get(ctx context.Context, satellite string, address blockchain.Address) (*Wallet, error) {
	var err error
	defer mon.Task()(&ctx)(&err)
	a, err := service.db.Get(ctx, satellite, address)
	if err != nil {
		return nil, ErrWalletsService.Wrap(err)
	}
	return a, nil
}

// GetStats returns information about the wallets table.
func (service *Service) GetStats(ctx context.Context) (*Stats, error) {
	var err error
	defer mon.Task()(&ctx)(&err)
	stats, err := service.db.GetStats(ctx)
	return stats, ErrWalletsService.Wrap(err)
}

// ListBySatellite returns accounts claimed by a certain satellite. Returns map[address]info.
func (service *Service) ListBySatellite(ctx context.Context, satellite string) (map[blockchain.Address]string, error) {
	var err error
	defer mon.Task()(&ctx)(&err)
	accounts, err := service.db.ListBySatellite(ctx, satellite)
	return accounts, ErrWalletsService.Wrap(err)
}

// Register inserts the addresses (key) and any associated info (value) to the persistent storage.
func (service *Service) Register(ctx context.Context, satellite string, addresses map[blockchain.Address]string) error {
	var err error
	defer mon.Task()(&ctx)(&err)
	err = service.db.InsertBatch(ctx, satellite, addresses)
	service.log.Debug("new wallets added to DB", zap.String("satellite", satellite), zap.Int("number of new wallets", len(addresses)))
	return ErrWalletsService.Wrap(err)
}
