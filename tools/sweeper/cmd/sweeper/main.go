// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"storj.io/sweeper"
)

// CLI is the sweeper command line.
type CLI struct {
	Run    RunCmd    `cmd:"" help:"Sweep ETH and ERC20 balances into the destination address."`
	Report ReportCmd `cmd:"" help:"Print the largest wallet balances and per-asset totals without transacting."`
}

// Common holds the flags every subcommand needs to reach the wallets. Each
// execution works on a single payment type, selected by --type: every type
// settles on its own chain and needs its own transactions, so L1 and L2 are
// handled by separate invocations.
type Common struct {
	Keys       string              `name:"keys" type:"existingfile" required:"" help:"Path to file of private keys (hex, no 0x prefix, one per line)."`
	Type       sweeper.PaymentType `name:"type" required:"" enum:"l1,l2" placeholder:"l1|l2" help:"Payment type to work on: l1 (Ethereum) or l2 (zkSync Era)."`
	Endpoint   string              `name:"endpoint" required:"" help:"JSON-RPC endpoint of the payment type's chain."`
	Tokens     []common.Address    `name:"tokens" help:"Comma-separated list of ERC20 contract addresses on the payment type's chain."`
	FilterKeys []common.Address    `name:"filter-keys" help:"Comma-separated list of public addresses to restrict this command to."`
}

// RunCmd sweeps the deposit wallets into a single destination address.
type RunCmd struct {
	Common

	Destination    common.Address `name:"destination" required:"" help:"Destination address for swept funds."`
	GasSource      string         `name:"gas-source" type:"existingfile" help:"Path to file containing private key (hex, no 0x) of a wallet with ETH for gas funding."`
	RateDelay      time.Duration  `name:"rate-delay" default:"200ms" help:"Delay between RPC calls per key."`
	MaxFailures    int            `name:"max-failures" default:"25" help:"Maximum number of wallet failures before aborting (0 for unlimited)."`
	SkipETH        bool           `name:"skip-eth" help:"Skip sweeping ETH, only sweep ERC20 tokens."`
	DryRun         bool           `name:"dry-run" help:"Print estimated recoverable balances and gas costs without sending any transactions."`
	ReceiptTimeout time.Duration  `name:"receipt-timeout" default:"30m" help:"Maximum time to wait for a transaction to be mined."`
}

// Run executes the sweep.
func (c *RunCmd) Run(ctx context.Context, logger *slog.Logger) error {
	keys, err := c.loadKeys(logger)
	if err != nil {
		return err
	}

	gasSource, err := loadGasSource(c.GasSource, logger)
	if err != nil {
		return err
	}

	client, closeClient, err := c.connect(ctx, logger, c.ReceiptTimeout)
	if err != nil {
		return err
	}
	defer closeClient()

	sw := sweeper.NewSweeper(client, c.Type, c.Destination, c.Tokens, gasSource, c.RateDelay, c.MaxFailures, c.SkipETH, c.DryRun, logger)

	logger.Info("starting sweep", "network", c.Type.Network(), "keys", len(keys), "tokens", len(c.Tokens))

	if err := sw.SweepAll(ctx, keys); err != nil {
		return fmt.Errorf("sweep failed: %w", err)
	}

	logger.Info("sweep completed successfully")
	return nil
}

// ReportCmd summarises the balances held by the deposit wallets.
type ReportCmd struct {
	Common

	Top int `name:"top" default:"10" help:"Number of largest holders to list per asset."`
}

// Run prints the balance report to stdout.
func (c *ReportCmd) Run(ctx context.Context, logger *slog.Logger) error {
	keys, err := c.loadKeys(logger)
	if err != nil {
		return err
	}

	// No transaction is sent, so the receipt timeout is irrelevant here.
	client, closeClient, err := c.connect(ctx, logger, 0)
	if err != nil {
		return err
	}
	defer closeClient()

	reporter := sweeper.NewReporter(client, c.Type, c.Tokens, c.Top, logger)

	reports, err := reporter.Report(ctx, keys)
	if err != nil {
		return fmt.Errorf("report failed: %w", err)
	}

	return sweeper.WriteReport(os.Stdout, reports)
}

// loadKeys reads the key file and applies --filter-keys, if given.
func (c *Common) loadKeys(logger *slog.Logger) ([]sweeper.KeyPair, error) {
	keys, err := sweeper.LoadKeys(c.Keys)
	if err != nil {
		return nil, fmt.Errorf("failed to load keys: %w", err)
	}
	logger.Info("loaded keys", "count", len(keys))

	if len(c.FilterKeys) > 0 {
		keys = sweeper.FilterKeys(keys, c.FilterKeys)
		logger.Info("filtered keys", "count", len(keys))
	}
	return keys, nil
}

// connect dials the endpoint and wraps it in a retrying client. The returned
// function closes the connection.
func (c *Common) connect(ctx context.Context, logger *slog.Logger, receiptTimeout time.Duration) (client *sweeper.RetryClient, closeClient func(), err error) {
	rpc, err := ethclient.DialContext(ctx, c.Endpoint)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to %s endpoint: %w", c.Type.Network(), err)
	}

	retry := sweeper.NewRetryClient(rpc, logger)
	if receiptTimeout > 0 {
		retry.SetReceiptTimeout(receiptTimeout)
	}

	return retry, rpc.Close, nil
}

// loadGasSource reads the gas funding key from path. An empty path means no
// gas source is configured.
func loadGasSource(path string, logger *slog.Logger) (*sweeper.KeyPair, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read gas source file: %w", err)
	}
	pk, err := crypto.HexToECDSA(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid gas source key: %w", err)
	}
	addr := crypto.PubkeyToAddress(pk.PublicKey)
	logger.Info("gas source configured", "address", addr.Hex())
	return &sweeper.KeyPair{PrivateKey: pk, Address: addr}, nil
}

// addressMapper decodes a hex Ethereum address, with or without the 0x prefix.
var addressMapper = kong.MapperFunc(func(ctx *kong.DecodeContext, target reflect.Value) error {
	var value string
	if err := ctx.Scan.PopValueInto("address", &value); err != nil {
		return err
	}
	value = strings.TrimSpace(value)
	if !common.IsHexAddress(value) {
		return fmt.Errorf("%q is not a valid hex address", value)
	}
	target.Set(reflect.ValueOf(common.HexToAddress(value)))
	return nil
})

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("sweeper"),
		kong.Description("Sweep ETH and ERC20 balances from storjscan deposit wallets. Each execution works on a single payment type, selected with --type."),
		kong.UsageOnError(),
		kong.TypeMapper(reflect.TypeOf(common.Address{}), addressMapper),
		kong.BindTo(context.Background(), (*context.Context)(nil)),
		kong.Bind(logger),
	)
	ctx.FatalIfErrorf(ctx.Run())
}
