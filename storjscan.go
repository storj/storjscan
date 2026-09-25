// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan

import (
	"context"
	"encoding/json"
	"net"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"storj.io/common/currency"
	"storj.io/common/debug"
	"storj.io/storj/private/lifecycle"
	"storj.io/storjscan/api"
	"storj.io/storjscan/blockchain"
	headerCleanup "storj.io/storjscan/blockchain/cleanup"
	"storj.io/storjscan/blockchain/events"
	"storj.io/storjscan/common"
	"storj.io/storjscan/health"
	"storj.io/storjscan/tokenprice"
	tokenPriceCleanup "storj.io/storjscan/tokenprice/cleanup"
	"storj.io/storjscan/tokenprice/coinmarketcap"
	"storj.io/storjscan/tokens"
	"storj.io/storjscan/wallets"
)

var mon = monkit.Package()

// Config wraps storjscan configuration.
type Config struct {
	Debug             debug.Config
	Events            events.Config
	Tokens            tokens.Config
	TokenPrice        tokenprice.Config
	TokenPriceCleanup tokenPriceCleanup.Config
	HeaderCleanup     headerCleanup.Config
	API               api.Config
}

// DB is a collection of storjscan databases.
type DB interface {
	// Headers creates headers database methods.
	Headers() blockchain.HeadersDB
	// TokenPrice returns database for STORJ token price information.
	TokenPrice() tokenprice.PriceQuoteDB
	// Wallets returns database for deposit address information.
	Wallets() wallets.DB
	// Ping checks if the database connection is available.
	Ping(context.Context) error
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
		Events       *events.Service
		CleanupChore *headerCleanup.Chore
	}

	Tokens struct {
		Service  *tokens.Service
		Endpoint *tokens.Endpoint
	}

	TokenPrice struct {
		Chore        *tokenprice.Chore
		CleanupChore *tokenPriceCleanup.Chore
		Service      tokenprice.Price
	}

	API struct {
		Listener net.Listener
		Server   *api.Server
	}

	Wallets struct {
		Service  *wallets.Service
		Endpoint *wallets.Endpoint
	}

	Health struct {
		Endpoint *health.Endpoint
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
		app.Blockchain.Events = events.NewEventsService(log.Named("blockchain:events-service"),
			db.Wallets(), config.Events)
	}

	var endpoints []common.EthEndpoint
	{ // endpoints
		err := json.Unmarshal([]byte(config.Tokens.Endpoints), &endpoints)
		if err != nil {
			return nil, err
		}
		if err := common.ValidateEndpoints(endpoints); err != nil {
			return nil, err
		}
	}

	{ // token price
		var err error
		app.TokenPrice.Service, app.TokenPrice.Chore, err = newTokenPrice(log, config.TokenPrice, db.TokenPrice(), endpoints)
		if err != nil {
			return nil, err
		}
		if app.TokenPrice.Chore != nil {
			app.Services.Add(lifecycle.Item{
				Name:  "tokenprice:chore",
				Run:   app.TokenPrice.Chore.Run,
				Close: app.TokenPrice.Chore.Close,
			})
		}
	}

	{ // tokens
		app.Tokens.Service = tokens.NewService(log.Named("tokens:service"),
			endpoints,
			app.Blockchain.HeadersCache,
			app.Blockchain.Events,
			app.TokenPrice.Service)

		app.Tokens.Endpoint = tokens.NewEndpoint(log.Named("tokens:endpoint"), app.Tokens.Service)
	}

	{ // wallets
		var err error
		app.Wallets.Service, err = wallets.NewService(log.Named("wallets:service"), db.Wallets())
		if err != nil {
			return nil, err
		}
		app.Wallets.Endpoint = wallets.NewEndpoint(log.Named("wallets:endpoint"), app.Wallets.Service)
	}

	{ // health check
		app.Health.Endpoint = health.NewEndpoint(log.Named("health:endpoint"), db, app.TokenPrice.Service, app.Tokens.Service)
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
		app.API.Server.NewAPI("/wallets", app.Wallets.Endpoint.Register)
		app.API.Server.NewAPI("/health", app.Health.Endpoint.Register)

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

	err = app.Tokens.Service.VerifyDecimals(ctx)
	if err != nil {
		return err
	}

	group, ctx := errgroup.WithContext(ctx)

	app.Servers.Run(ctx, group)
	app.Services.Run(ctx, group)

	return group.Wait()
}

// Close closes all the resources.
func (app *App) Close() error {
	var errList errs.Group
	errList.Add(app.Servers.Close())
	errList.Add(app.Services.Close())
	return errList.Err()
}

// newTokenPrice creates the configured token price service. Either fixed price or
// coinmarketcap should be configured. The chore is nil when it's not required.
func newTokenPrice(log *zap.Logger, config tokenprice.Config, db tokenprice.PriceQuoteDB, endpoints []common.EthEndpoint) (tokenprice.Price, *tokenprice.Chore, error) {
	fixedConfigured := config.FixedPrice != ""
	coinmarketcapConfigured := config.UseTestPrices || config.CoinmarketcapConfig.APIKey != ""

	switch {
	case fixedConfigured && coinmarketcapConfigured:
		return nil, nil, errs.New("both fixed token price and coinmarketcap are configured, only one of them can be used")
	case fixedConfigured:
		price, err := decimal.NewFromString(config.FixedPrice)
		if err != nil {
			return nil, nil, errs.New("invalid fixed token price %q: %v", config.FixedPrice, err)
		}
		if !price.IsPositive() {
			return nil, nil, errs.New("fixed token price must be positive: %q", config.FixedPrice)
		}
		if err := checkFixedPriceEndpoints(endpoints); err != nil {
			return nil, nil, err
		}
		log.Info("using fixed token price", zap.String("USD", price.String()))
		return tokenprice.NewFixedPrice(currency.AmountFromDecimal(price, currency.USDollarsMicro)), nil, nil
	case coinmarketcapConfigured:
		var client tokenprice.Client
		if config.UseTestPrices {
			log.Info("using coinmarketcap test token prices")
			client = coinmarketcap.NewTestClient()
		} else {
			if config.CoinmarketcapConfig.TokenID == "" {
				return nil, nil, errs.New("coinmarketcap token ID is not configured (e.g. 1772 for STORJ)")
			}
			log.Info("using coinmarketcap token price", zap.String("URL", config.CoinmarketcapConfig.BaseURL), zap.String("TokenID", config.CoinmarketcapConfig.TokenID))
			client = coinmarketcap.NewClient(config.CoinmarketcapConfig)
		}
		service := tokenprice.NewCoinmarketcapPrice(log.Named("tokenprice:service"), db, client, config.PriceWindow)
		return service, tokenprice.NewChore(log.Named("tokenprice:chore"), service, config.Interval), nil
	default:
		return nil, nil, errs.New("token price is not configured: either fixed price or coinmarketcap API key should be set")
	}
}

// checkFixedPriceEndpoints returns an error if any of the endpoints uses a token
// which can't be valued with a fixed price.
func checkFixedPriceEndpoints(endpoints []common.EthEndpoint) error {
	for _, endpoint := range endpoints {
		tokenCurrency, err := endpoint.TokenCurrency()
		if err != nil {
			return err
		}
		if tokenCurrency != currency.USDC {
			return errs.New("coinmarketcap is required for %s token (endpoint %q), fixed price is only allowed for USDC", tokenCurrency.Symbol(), endpoint.Name)
		}
	}
	return nil
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
