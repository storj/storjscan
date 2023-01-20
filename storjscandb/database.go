// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"fmt"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/dbutil"
	"storj.io/private/dbutil/pgutil"
	"storj.io/private/migrate"
	"storj.io/private/tagsql"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/storjscandb/dbx"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/wallets"
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
	if impl != dbutil.Postgres && impl != dbutil.Cockroach {
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
	switch db.implementation {
	case dbutil.Postgres:
		schema, err := pgutil.ParseSchemaFromConnstr(db.source)
		if err != nil {
			return errs.New("error parsing schema: %+v", err)
		}

		if schema != "" {
			err = pgutil.CreateSchema(ctx, db, schema)
			if err != nil {
				return errs.New("error creating schema: %+v", err)
			}
		}

	case dbutil.Cockroach:
		var dbName string
		if err := db.QueryRow(ctx, `SELECT current_database();`).Scan(&dbName); err != nil {
			return errs.New("error querying current database: %+v", err)
		}

		_, err := db.Exec(ctx, fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s;`,
			pgutil.QuoteIdentifier(dbName)))
		if err != nil {
			return errs.Wrap(err)
		}
	}

	var migration *migrate.Migration
	switch db.implementation {
	case dbutil.Postgres, dbutil.Cockroach:
		migration = db.PostgresMigration()
	default:
		return migrate.Create(ctx, "database", db.DB)
	}
	return migration.Run(ctx, db.log)
}

// Headers creates new headersDB with current DB connection.
func (db *DB) Headers() blockchain.HeadersDB {
	return &headersDB{db: db.DB}
}

// TokenPrice creates new PriceQuoteDB with current DB connection.
func (db *DB) TokenPrice() tokenprice.PriceQuoteDB {
	return &priceQuoteDB{db: db.DB}
}

// Wallets creates new WalletsDB with current DB connection.
func (db *DB) Wallets() wallets.DB {
	return &walletsDB{db: db.DB}
}

// Ping checks if the database connection is available.
func (db *DB) Ping(ctx context.Context) error {
	return db.DB.Ping(ctx)
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
					CREATE INDEX block_header_timestamp ON block_headers ( timestamp ) ;
					CREATE TABLE token_prices (
						interval_start timestamp with time zone NOT NULL,
						price bigint NOT NULL,
						PRIMARY KEY ( interval_start )
					);
					CREATE TABLE wallets (
						address bytea NOT NULL,
						claimed timestamp with time zone,
						satellite text NOT NULL,
						info text,
						created_at timestamp with time zone NOT NULL DEFAULT current_timestamp,
						PRIMARY KEY ( address )
					);
					CREATE INDEX wallets_satellite_index ON wallets ( satellite );`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Add id column for new primary key for wallets table",
				Version:     1,
				Action: migrate.SQL{
					`ALTER TABLE wallets ADD COLUMN id bigserial NOT NULL;`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Make id column new primary key for wallets table",
				Version:     2,
				Action: migrate.SQL{
					`ALTER TABLE wallets DROP CONSTRAINT wallets_pkey;`,
					`ALTER TABLE wallets ADD CONSTRAINT wallets_pkey PRIMARY KEY ( id );`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Create unique index for address column for wallets table",
				Version:     3,
				Action: migrate.SQL{
					`CREATE UNIQUE INDEX wallets_address_index ON wallets ( address );`,
				},
			},
		},
	}
}
