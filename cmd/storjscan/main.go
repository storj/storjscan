// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"log"

	"github.com/spf13/pflag"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/private/cfgstruct"
	"storj.io/storjscan"
	"storj.io/storjscan/storjscandb"
)

// Flags contains storjscan app configuration.
var Flags struct {
	Database string
	storjscan.Config
}

func init() {
	cfgstruct.Bind(pflag.CommandLine, &Flags)
}

func main() {
	pflag.Parse()

	if err := run(context.Background(), Flags.Config); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, config storjscan.Config) error {
	logger := zap.NewExample()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()

	db, err := storjscandb.Open(ctx, logger.Named("storjscandb"), Flags.Database)
	if err != nil {
		return err
	}

	app, err := storjscan.NewApp(logger.Named("storjscan"), config, db)
	if err != nil {
		return err
	}

	runErr := app.Run(ctx)
	closeErr := app.Close()
	return errs.Combine(runErr, closeErr)
}
