// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandb

import (
	"context"
	"fmt"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storj/private/migrate"
	"storj.io/storj/shared/dbutil"
	"storj.io/storj/shared/dbutil/pgutil"
	"storj.io/storj/shared/tagsql"
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

	source, err = pgutil.EnsureApplicationName(source, "storjscan")
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
		if err := db.QueryRowContext(ctx, `SELECT current_database();`).Scan(&dbName); err != nil {
			return errs.New("error querying current database: %+v", err)
		}

		_, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE IF NOT EXISTS %s;`,
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
	return db.DB.PingContext(ctx)
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
						price double precision NOT NULL,
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
			{
				DB:          &db.migrationDB,
				Description: "Delete existing price records and drop price column",
				Version:     4,
				Action: migrate.SQL{
					`TRUNCATE TABLE token_prices;`,
					`ALTER TABLE token_prices DROP COLUMN price;`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Add price column as int64",
				Version:     5,
				Action: migrate.SQL{
					`ALTER TABLE token_prices ADD COLUMN price bigint NOT NULL;`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Delete existing block headers and add chain ID column",
				Version:     6,
				Action: migrate.SQL{
					`TRUNCATE TABLE block_headers;`,
					`ALTER TABLE block_headers ADD COLUMN chain_id bigint NOT NULL;`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Add chain ID column to primary key for block headers table",
				Version:     7,
				Action: migrate.SQL{
					`ALTER TABLE block_headers DROP CONSTRAINT block_headers_pkey;`,
					`ALTER TABLE block_headers ADD CONSTRAINT block_headers_pkey PRIMARY KEY ( chain_id, hash );`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Add transfer event logs cache table",
				Version:     8,
				Action: migrate.SQL{
					`CREATE TABLE transfer_events (
						chain_id bigint NOT NULL,
						block_hash bytea NOT NULL,
						block_number bigint NOT NULL,
						transaction bytea NOT NULL,
						log_index integer NOT NULL,
						from_address bytea NOT NULL,
						to_address bytea NOT NULL,
						token_value bigint NOT NULL,
						created_at timestamp with time zone NOT NULL,
						PRIMARY KEY ( chain_id, block_hash, log_index )
					);`,
				},
			},
			{
				DB:          &db.migrationDB,
				Description: "Drop transfer event logs cache table",
				Version:     9,
				Action: migrate.SQL{
					`DROP TABLE transfer_events;`,
				},
			},
		},
	}
}
