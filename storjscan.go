// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan

import (
	"context"
	"net"
	"strings"

	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/private/debug"
	"storj.io/storj/private/lifecycle"
	"storj.io/storjscan/api"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/tokenprice"
	"storj.io/storjscan/tokenprice/coinmarketcap"
	"storj.io/storjscan/tokens"
	"storj.io/storjscan/wallets"
)

var mon = monkit.Package()

// Config wraps storjscan configuration.
type Config struct {
	Debug      debug.Config
	Tokens     tokens.Config
	TokenPrice tokenprice.Config
	API        api.Config
}

// DB is a collection of storjscan databases.
type DB interface {
	// Headers creates headers database methods.
	Headers() blockchain.HeadersDB
	// TokenPrice returns database for STORJ token price information.
	TokenPrice() tokenprice.PriceQuoteDB
	// Wallets returns database for deposit address information.
	Wallets() wallets.DB
}

// App is the storjscan process that runs API endpoint.
//
// architecture: Peer
type App struct {
	Log      *zap.Logger
	DB       DB
	Servers  *lifecycle.Group
	Services *lifecycle.Group

	Debug struct {
		Listener net.Listener
		Server   *debug.Server
	}

	Blockchain struct {
		HeadersCache *blockchain.HeadersCache
	}

	Tokens struct {
		Service  *tokens.Service
		Endpoint *tokens.Endpoint
	}

	TokenPrice struct {
		Chore *tokenprice.Chore
	}

	API struct {
		Listener net.Listener
		Server   *api.Server
	}
}

// NewApp creates new storjscan application instance.
func NewApp(log *zap.Logger, config Config, db DB) (*App, error) {
	app := &App{
		Log: log,
		DB:  db,

		Servers:  lifecycle.NewGroup(log.Named("servers")),
		Services: lifecycle.NewGroup(log.Named("services")),
	}

	{ // blockchain
		app.Blockchain.HeadersCache = blockchain.NewHeadersCache(log.Named("blockchain:headers-cache"),
			db.Headers())
	}

	{ // tokens
		token, err := blockchain.AddressFromHex(config.Tokens.Contract)
		if err != nil {
			return nil, err
		}

		app.Tokens.Service = tokens.NewService(log.Named("tokens:service"),
			config.Tokens.Endpoint,
			token,
			app.Blockchain.HeadersCache)

		app.Tokens.Endpoint = tokens.NewEndpoint(log.Named("tokens:endpoint"), app.Tokens.Service)
	}

	{ // token price
		client := coinmarketcap.NewClient(config.TokenPrice.CoinmarketcapConfig)
		app.TokenPrice.Chore = tokenprice.NewChore(log.Named("tokenprice:chore"), db.TokenPrice(), client, config.TokenPrice.Interval)

		app.Services.Add(lifecycle.Item{
			Name:  "tokenprice:chore",
			Run:   app.TokenPrice.Chore.Run,
			Close: app.TokenPrice.Chore.Close,
		})
	}

	{ // API
		var err error

		app.API.Listener, err = net.Listen("tcp", config.API.Address)
		if err != nil {
			return nil, err
		}

		apiKeys, err := getKeyBytes(config.API.Keys)
		if err != nil {
			return nil, err
		}
		app.API.Server = api.NewServer(log.Named("api:server"), app.API.Listener, apiKeys)
		app.API.Server.NewAPI("/tokens", app.Tokens.Endpoint.Register)

		app.Servers.Add(lifecycle.Item{
			Name:  "api",
			Run:   app.API.Server.Run,
			Close: app.API.Server.Close,
		})
	}

	err := app.API.Server.LogRoutes()
	if err != nil {
		return app, err
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

func getKeyBytes(keys []string) (map[string]string, error) {
	apiKeys := make(map[string]string)
	for _, key := range keys {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			return apiKeys, errs.New("Api keys should be defined in user:secret form, but it was %s", key)
		}
		apiKeys[parts[0]] = parts[1]
	}
	return apiKeys, nil
}
