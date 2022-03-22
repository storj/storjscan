// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"github.com/zeebo/errs"

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
