// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/dbutil"
	"storj.io/private/dbutil/pgutil"
	"storj.io/private/migrate"
	"storj.io/private/tagsql"
	"storj.io/storjscan/storjscandb/dbx"
)

var (
	mon = monkit.Package()

	// Error is the default storjscandb errs class.
	Error = errs.Class("storjscandb")
)

// DB is storjscan database.
type DB struct {
	*dbx.DB
	log            *zap.Logger
	driver         string
	source         string
	implementation dbutil.Implementation
	migrationDB    tagsql.DB
}

// Open creates instance of storjscan DB.
func Open(ctx context.Context, log *zap.Logger, databaseURL string) (*DB, error) {
	driver, source, impl, err := dbutil.SplitConnStr(databaseURL)
	if err != nil {
		return nil, err
	}
	if impl != dbutil.Postgres {
		return nil, Error.New("unsupported driver %q", driver)
	}

	source, err = pgutil.CheckApplicationName(source, "storjscan")
	if err != nil {
		return nil, err
	}

	dbxDB, err := dbx.Open(driver, source)
	if err != nil {
		return nil, Error.New("failed opening database via DBX at %q: %v", source, err)
	}
	log.Debug("Connected to:", zap.String("db source", source))

	dbutil.Configure(ctx, dbxDB.DB, "storjscandb", mon)

	db := &DB{
		DB:             dbxDB,
		log:            log,
		driver:         driver,
		source:         source,
		implementation: impl,
	}
	db.migrationDB = db

	return db, nil
}

// MigrateToLatest migrates db to the latest version.
func (db *DB) MigrateToLatest(ctx context.Context) error {
	var migration *migrate.Migration

	switch db.implementation {
	case dbutil.Postgres:
		migration = db.PostgresMigration()
	default:
		return migrate.Create(ctx, "database", db.DB)
	}
	return migration.Run(ctx, db.log)
}

// Wallets creates new WalletsDB with current DB connection.
func (db *DB) Wallets() *WalletsDB {
	return &WalletsDB{db: db.DB}
}

// PostgresMigration returns steps needed for migrating postgres database.
func (db *DB) PostgresMigration() *migrate.Migration {
	return &migrate.Migration{
		Table: "versions",
		Steps: []*migrate.Step{
			{
				DB:          &db.migrationDB,
				Description: "Initial setup",
				Version:     0,
				Action: migrate.SQL{
					`CREATE TABLE block_headers (
						hash bytea NOT NULL,
						number bigint NOT NULL,
						timestamp timestamp with time zone NOT NULL,
						created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
						PRIMARY KEY ( hash )
					);
					CREATE INDEX block_header_timestamp ON block_headers ( timestamp ) ;`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Create wallets table",
				Version:     1,
				Action: migrate.SQL{
					`CREATE TABLE wallets (
						address bytea NOT NULL,
						claimed timestamp with time zone,
						created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
						PRIMARY KEY ( address )
					);`,
				},
			},
		},
	}
}
