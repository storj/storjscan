// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan

import (
	"context"
	"net"

	"github.com/spacemonkeygo/monkit/v3"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"storj.io/storjscan/storjscandb"
	"storj.io/storjscan/wallets"

	"storj.io/private/debug"
	"storj.io/storj/private/lifecycle"
	"storj.io/storjscan/api"
	"storj.io/storjscan/tokens"
)

var mon = monkit.Package()

// Config wraps storjscan configuration.
type Config struct {
	Debug  debug.Config
	Tokens tokens.Config
	API    api.Config
}

// DB is a collection of storjscan databases.
type DB interface {
	Wallets() *storjscandb.WalletsDB
}

// App is the storjscan process that runs API endpoint.
//
// architecture: Peer
type App struct {
	Log     *zap.Logger
	DB      DB
	Servers *lifecycle.Group

	Debug struct {
		Listener net.Listener
		Server   *debug.Server
	}

	Tokens struct {
		Service  *tokens.Service
		Endpoint *tokens.Endpoint
	}

	API struct {
		Listener net.Listener
		Server   *api.Server
	}

	Wallets struct {
		Service  *wallets.Wallets
		Endpoint *wallets.Endpoint
	}

}

// NewApp creates new storjscan application instance.
func NewApp(log *zap.Logger, config Config, db DB) (*App, error) {
	app := &App{
		Log: log,
		DB:  db,

		Servers: lifecycle.NewGroup(log.Named("servers")),
	}

	{ // tokens
		token, err := tokens.AddressFromHex(config.Tokens.TokenAddress)
		if err != nil {
			return nil, err
		}

		app.Tokens.Service = tokens.NewService(log.Named("tokens:service"),
			config.Tokens.Endpoint,
			token)

		app.Tokens.Endpoint = tokens.NewEndpoint(log.Named("tokens:endpoint"), app.Tokens.Service)
	}

	{ // API
		var err error

		app.API.Listener, err = net.Listen("tcp", config.API.Address)
		if err != nil {
			return nil, err
		}

		app.API.Server = api.NewServer(log.Named("api:server"), app.API.Listener)
		app.API.Server.NewAPI("/tokens", app.Tokens.Endpoint.Register)

		app.Servers.Add(lifecycle.Item{
			Name:  "api",
			Run:   app.API.Server.Run,
			Close: app.API.Server.Close,
		})
	}
	{ // wallets
		//TODO
	}

	return app, nil
}

// Run runs storjscan until it's either closed or it errors.
func (app *App) Run(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)

	group, ctx := errgroup.WithContext(ctx)

	app.Servers.Run(ctx, group)

	return group.Wait()
}

// Close closes all the resources.
func (app *App) Close() error {
	return app.Servers.Close()
}
