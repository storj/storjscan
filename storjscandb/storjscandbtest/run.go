// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscandbtest

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storj/shared/dbutil"
	"storj.io/storj/shared/dbutil/dbtest"
	"storj.io/storj/shared/dbutil/pgutil"
	"storj.io/storj/shared/dbutil/tempdb"
	"storj.io/storjscan"
	"storj.io/storjscan/storjscandb"
	"storj.io/storjscan/wallets"
)

// Checks that test db implements storjscan.DB.
var _ storjscan.DB = (*DB)(nil)

// Run creates new storjscan test database, create tables and execute test function against that db.
func Run(t *testing.T, test func(ctx *testcontext.Context, t *testing.T, db *DB)) {
	t.Run("Postgres", func(t *testing.T) {
		ctx := testcontext.New(t)
		defer ctx.Cleanup()

		connStr := dbtest.PickPostgres(t)

		db, err := OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		test(ctx, t, db)
	})

	t.Run("Cockroach", func(t *testing.T) {
		ctx := testcontext.New(t)
		defer ctx.Cleanup()

		connStr := dbtest.PickCockroach(t)

		db, err := OpenDB(ctx, zaptest.NewLogger(t), connStr, t.Name(), "T")
		if err != nil {
			t.Fatal(err)
		}
		defer ctx.Check(db.Close)

		err = db.MigrateToLatest(ctx)
		if err != nil {
			t.Fatal(err)
		}

		test(ctx, t, db)
	})
}

// DB is test storjscan database with unique schema which performs cleanup on close.
type DB struct {
	*storjscandb.DB
	tempDB *dbutil.TempDatabase
}

// OpenDB opens new unique temp storjscan test database.
func OpenDB(ctx context.Context, log *zap.Logger, connStr, testName, category string) (*DB, error) {
	schemaSuffix := pgutil.CreateRandomTestingSchemaName(6)
	schemaName := schemaName(testName, schemaSuffix, category)

	tempDB, err := tempdb.OpenUnique(ctx, log.Named("tempdb"), connStr, schemaName, nil)
	if err != nil {
		return nil, err
	}
	storjscanDB, err := storjscandb.Open(ctx, log, tempDB.ConnStr)
	if err != nil {
		return nil, errs.Combine(err, tempDB.Close())
	}

	return &DB{
		DB:     storjscanDB,
		tempDB: tempDB,
	}, nil
}

// Close closes test database and performs cleanup.
func (db *DB) Close() error {
	return errs.Combine(db.DB.Close(), db.tempDB.Close())
}

// schemaName create new postgres db schema name for testing.
func schemaName(testName, suffix, category string) string {
	maxTestNameLength := 64 - len(suffix) - len(category)
	if len(testName) > maxTestNameLength {
		testName = testName[:maxTestNameLength]
	}
	return strings.ToLower(testName + "/" + suffix + "/" + category)
}

// GenerateTestAddresses create test addresses for the satellite and add them to the wallets service.
func GenerateTestAddresses(ctx context.Context, service *wallets.Service, satellite string, count int) error {
	seed := make([]byte, 64)
	_, err := rand.Read(seed)
	if err != nil {
		return err
	}

	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return err
	}

	var inserts []wallets.InsertWallet
	next := accounts.DefaultIterator(accounts.DefaultBaseDerivationPath)
	for i := 0; i < count; i++ {
		account, err := Derive(masterKey, next())
		if err != nil {
			return err
		}
		inserts = append(inserts, wallets.InsertWallet{
			Address: account.Address,
			Info:    "test-info",
		})
	}

	if len(inserts) < 1 {
		return errors.New("no addresses created")
	}

	err = service.Register(ctx, satellite, inserts)
	return err
}

// Derive derives an account from the master key using the provided path.
func Derive(masterKey *hdkeychain.ExtendedKey, path accounts.DerivationPath) (accounts.Account, error) {
	var err error
	key := masterKey
	for _, n := range path {
		key, err = key.Derive(n)
		if err != nil {
			return accounts.Account{}, errs.Wrap(err)
		}
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		return accounts.Account{}, errs.Wrap(err)
	}
	privateKeyECDSA := privateKey.ToECDSA()
	publicKey := privateKeyECDSA.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return accounts.Account{}, errs.New("failed to get public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return accounts.Account{
		Address: address,
		URL: accounts.URL{
			Scheme: "",
			Path:   path.String(),
		},
	}, nil
}
