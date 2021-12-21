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
	logger := zap.NewExample()
	defer func() {
		if err := logger.Sync(); err != nil {
			log.Println(err)
		}
	}()

	var config storjscan.Config

	config.API.Address = "127.0.0.1:14002"
	config.Tokens.Endpoint = "http://127.0.0.1:7545"
	config.Tokens.TokenAddress = "0x65B38B8fc2a8d8fc2798a002DfD8e257aB6b0382"

	app, err := storjscan.NewApp(logger.Named("storjscan"), config)
	if err != nil {
		log.Fatal(err)
	}

	runErr := app.Run(context.Background())
	closeErr := app.Close()

	if err = errs.Combine(runErr, closeErr); err != nil {
		log.Fatal(err)
	}
}
