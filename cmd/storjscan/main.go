// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package main

import (
	"context"
	"log"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/storjscan"
)

func main() {
	var config storjscan.Config

	config.API.Address = "127.0.0.1:14002"
	config.Tokens.Endpoint = "http://127.0.0.1:7545"
	config.Tokens.TokenAddress = "0x65B38B8fc2a8d8fc2798a002DfD8e257aB6b0382"

	if err := run(context.Background(), config); err != nil {
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

	app, err := storjscan.NewApp(logger.Named("storjscan"), config)
	if err != nil {
		return err
	}

	runErr := app.Run(ctx)
	closeErr := app.Close()
	return errs.Combine(runErr, closeErr)
}
