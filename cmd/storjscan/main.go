// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"log"
	"time"

	"github.com/spf13/cobra"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/cfgstruct"
	"storj.io/private/process"
	"storj.io/storjscan"
	"storj.io/storjscan/storjscandb"
)

var (
	rootCmd = &cobra.Command{
		Use:   "storjscan",
		Short: "STORJ token payment management service",
	}
	runCmd = &cobra.Command{
		Use:   "run",
		Short: "Start payment listener daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, _ := process.Ctx(cmd)
			return run(ctx, runCfg)
		},
	}
	setupCmd = &cobra.Command{
		Use:   "migrate",
		Short: "Run database migration",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, _ := process.Ctx(cmd)
			return migrate(ctx, runCfg)
		},
	}
	runCfg runConfig
)

// migrate executes the database migration on an existing database.
func migrate(ctx context.Context, config runConfig) error {
	logger := zap.NewExample()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()

	db, err := openDatabaseWithRetry(ctx, logger, config.Database)
	if err != nil {
		return err
	}

	if config.WithMigration {
		err := db.MigrateToLatest(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

type runConfig struct {
	storjscan.Config
	Database      string `help:"satellite database connection string" releaseDefault:"postgres://" devDefault:"postgres://"`
	WithMigration bool   `help:"run database migration before the start" releaseDefault:"false" devDefault:"true"`
}

func init() {
	defaults := cfgstruct.DefaultsFlag(rootCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(setupCmd)
	process.Bind(runCmd, &runCfg, defaults)
}

func main() {
	process.ExecCustomDebug(rootCmd)
}

func run(ctx context.Context, config runConfig) error {
	logger := zap.NewExample()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()

	db, err := openDatabaseWithRetry(ctx, logger, config.Database)
	if err != nil {
		return err
	}

	if config.WithMigration {
		err := db.MigrateToLatest(ctx)
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
	return errs.Combine(runErr, closeErr)
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
