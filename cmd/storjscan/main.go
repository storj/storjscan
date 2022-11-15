// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip39"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/cfgstruct"
	"storj.io/private/process"
	"storj.io/storjscan"
	"storj.io/storjscan/storjscandb"
	"storj.io/storjscan/wallets"
)

var (
	rootCmd = &cobra.Command{
		Use:   "storjscan",
		Short: "STORJ token payment management service",
	}

	runCfg runConfig
	runCmd = &cobra.Command{
		Use:   "run",
		Short: "Start payment listener daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, _ := process.Ctx(cmd)
			return run(ctx, runCfg)
		},
	}
	migrateCmd = &cobra.Command{
		Use:   "migrate",
		Short: "Execute database migration.",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, _ := process.Ctx(cmd)
			return migrate(ctx, runCfg)
		},
	}

	generateCfg struct {
		MnemonicFile string `help:"File which contains the mnemonic to be used for HD generation." default:".mnemonic"`
		OutputFile   string `help:"File to write CSV output to. If unset, uses stdout."`
		Min          int    `help:"Index of the first derived address." default:"0"`
		Max          int    `help:"Index of the last derived address." default:"1000"`
		KeysName     string `help:"Name of the hd chain/mnemonic which was used/" default:"default"`
	}
	generateCmd = &cobra.Command{
		Use:   "generate",
		Short: "Generated deterministic wallet addresses and output them to a CSV",
		RunE:  generate,
	}

	importCfg struct {
		Address   string `help:"public address to connect to" default:"http://127.0.0.1:12000"`
		APIKey    string `help:"Secrets to connect to service endpoints."`
		APISecret string `help:"Secrets to connect to service endpoints."`
		InputFile string `help:"CSV input path"`
	}
	importCmd = &cobra.Command{
		Use:   "import",
		Short: "Read generated wallet addresses and register them with the db",
		RunE:  importCSV,
	}

	mnemonicCmd = &cobra.Command{
		Use:   "mnemonic",
		Short: "Print out a random mnemonic to be used.",
		RunE: func(cmd *cobra.Command, args []string) error {
			entropy, err := bip39.NewEntropy(256)
			if err != nil {
				return errs.Wrap(err)
			}
			m, err := bip39.NewMnemonic(entropy)
			if err != nil {
				return errs.Wrap(err)
			}
			fmt.Println(m)
			return nil
		},
	}
)

type runConfig struct {
	storjscan.Config
	Database      string `help:"satellite database connection string" releaseDefault:"cockroach://" devDefault:"postgres://"`
	WithMigration bool   `help:"automatically run database migration before the start" releaseDefault:"false" devDefault:"true"`
}

func init() {
	defaults := cfgstruct.DefaultsFlag(rootCmd)
	rootCmd.AddCommand(runCmd)
	process.Bind(runCmd, &runCfg, defaults)

	rootCmd.AddCommand(migrateCmd)
	process.Bind(migrateCmd, &runCfg, defaults)

	rootCmd.AddCommand(generateCmd)
	process.Bind(generateCmd, &generateCfg, defaults)

	rootCmd.AddCommand(importCmd)
	process.Bind(importCmd, &importCfg, defaults)

	rootCmd.AddCommand(mnemonicCmd)

}

func main() {
	process.Exec(rootCmd)
}

func run(ctx context.Context, config runConfig) error {
	logger := zap.L()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()
	db, err := openDatabaseWithRetry(ctx, logger, config.Database)
	if err != nil {
		return err
	}

	defer func() {
		err = errs.Combine(err, db.Close())
	}()

	if config.WithMigration {
		err = migrate(ctx, config)
		if err != nil {
			return err
		}
	}

	app, err := storjscan.NewApp(logger.Named("storjscan"), config.Config, db)
	if err != nil {
		return err
	}

	runErr := app.Run(ctx)
	closeErr := app.Close()
	err = errs.Combine(runErr, closeErr)
	return err
}

func openDatabaseWithRetry(ctx context.Context, logger *zap.Logger, databaseURL string) (db *storjscandb.DB, err error) {
	for i := 0; i < 120; i++ {
		db, err = storjscandb.Open(ctx, logger.Named("storjscandb"), databaseURL)
		if err == nil {
			break
		}

		logger.Warn("Database connection is not yet available", zap.Error(err))
		time.Sleep(3 * time.Second)

	}
	return db, err
}

// migrate executes the database migration on an existing database.
func migrate(ctx context.Context, config runConfig) (err error) {
	logger := zap.L()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()
	db, err := openDatabaseWithRetry(ctx, logger, config.Database)
	if err != nil {
		return err
	}

	defer func() {
		err = errs.Combine(err, db.Close())
	}()

	err = db.MigrateToLatest(ctx)
	return err
}

func generate(cmd *cobra.Command, args []string) (err error) {
	ctx, _ := process.Ctx(cmd)
	mnemonic, err := ioutil.ReadFile(generateCfg.MnemonicFile)
	if err != nil {
		return errs.New("Couldn't read mnemonic from %s: %v", generateCfg.MnemonicFile, err)
	}

	addresses, err := wallets.Generate(ctx, generateCfg.KeysName, generateCfg.Min, generateCfg.Max, strings.TrimSpace(string(mnemonic)))
	if err != nil {
		return err
	}

	var out io.Writer = os.Stdout
	if generateCfg.OutputFile != "" {
		fh, err := os.Create(generateCfg.OutputFile)
		if err != nil {
			return err
		}
		defer func() {
			closeErr := fh.Close()
			err = errs.Combine(err, errs.Wrap(closeErr))
		}()
		out = fh
	}

	w := csv.NewWriter(out)
	err = w.Write([]string{"address", "info"})
	if err != nil {
		return errs.Wrap(err)
	}

	for addr, info := range addresses {
		err = w.Write([]string{addr.String(), info})
		if err != nil {
			return errs.Wrap(err)
		}
	}

	w.Flush()
	return errs.Wrap(w.Error())
}

func safeHexToAddress(s string) (common.Address, error) {
	if !common.IsHexAddress(s) {
		return common.Address{}, errs.New("malformed hex address: %q", s)
	}
	return common.HexToAddress(s), nil
}

func importCSV(cmd *cobra.Command, args []string) (err error) {
	ctx, _ := process.Ctx(cmd)

	fh, err := os.Open(importCfg.InputFile)
	if err != nil {
		return errs.Wrap(err)
	}
	defer func() {
		err = errs.Combine(err, fh.Close())
	}()

	records, err := csv.NewReader(fh).ReadAll()
	if err != nil {
		return errs.Wrap(err)
	}
	if len(records) < 1 || len(records[0]) != 2 || records[0][0] != "address" || records[0][1] != "info" {
		return errs.New("malformed csv")
	}

	addresses := map[common.Address]string{}
	for _, record := range records[1:] {
		address, err := safeHexToAddress(record[0])
		if err != nil {
			return err
		}

		addresses[address] = record[1]
	}

	client := wallets.NewClient(importCfg.Address, importCfg.APIKey, importCfg.APISecret)
	return client.AddWallets(ctx, addresses)
}
