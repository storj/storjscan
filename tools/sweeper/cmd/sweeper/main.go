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
	Type       sweeper.PaymentType `name:"type" required:"" enum:"l1,l2,l1-7702" placeholder:"l1|l2|l1-7702" help:"Payment type to work on: l1 (Ethereum), l2 (zkSync Era), or l1-7702 (Ethereum, swept through an EIP-7702 delegation)."`
	Endpoint   string              `name:"endpoint" required:"" help:"JSON-RPC endpoint of the payment type's chain."`
	Tokens     []common.Address    `name:"tokens" help:"Comma-separated list of ERC20 contract addresses on the payment type's chain."`
	FilterKeys []common.Address    `name:"filter-keys" help:"Comma-separated list of public addresses to restrict this command to."`
}

// RunCmd sweeps the deposit wallets into a single destination address.
type RunCmd struct {
	Common

	Destination     common.Address `name:"destination" required:"" help:"Destination address for swept funds."`
	GasSource       string         `name:"gas-source" type:"existingfile" help:"Path to file containing private key (hex, no 0x) of a wallet with ETH for gas funding."`
	SweeperContract common.Address `name:"sweeper-contract" help:"Address of the deployed Sweeper7702 contract the wallets delegate their code to (--type l1-7702 only)."`
	DestinationKey  string         `name:"destination-key" type:"existingfile" help:"Path to file containing the private key (hex, no 0x) of the destination wallet, which signs and pays for every transaction (--type l1-7702 only)."`
	RateDelay       time.Duration  `name:"rate-delay" default:"200ms" help:"Delay between RPC calls per key."`
	MaxFailures     int            `name:"max-failures" default:"25" help:"Maximum number of wallet failures before aborting (0 for unlimited)."`
	SkipETH         bool           `name:"skip-eth" help:"Skip sweeping ETH, only sweep ERC20 tokens."`
	DryRun          bool           `name:"dry-run" help:"Print estimated recoverable balances and gas costs without sending any transactions."`
	ReceiptTimeout  time.Duration  `name:"receipt-timeout" default:"30m" help:"Maximum time to wait for a transaction to be mined."`
}

// Run executes the sweep.
func (c *RunCmd) Run(ctx context.Context, logger *slog.Logger) error {
	if err := c.validate(); err != nil {
		return err
	}

	keys, err := c.loadKeys(logger)
	if err != nil {
		return err
	}

	client, closeClient, err := c.connect(ctx, logger, c.ReceiptTimeout)
	if err != nil {
		return err
	}
	defer closeClient()

	var sw *sweeper.Sweeper
	if c.Type == sweeper.PaymentTypeL17702 {
		sponsor, err := loadKey(c.DestinationKey)
		if err != nil {
			return fmt.Errorf("destination key: %w", err)
		}
		logger.Info("destination key loaded, it pays for every transaction", "address", sponsor.Address.Hex())
		sw = sweeper.NewDelegatedSweeper(client, c.Type, c.Destination, c.Tokens, &sweeper.Delegation{
			Contract: c.SweeperContract,
			Sponsor:  sponsor,
		}, c.RateDelay, c.MaxFailures, c.DryRun, logger)
	} else {
		gasSource, err := loadGasSource(c.GasSource, logger)
		if err != nil {
			return err
		}
		sw = sweeper.NewSweeper(client, c.Type, c.Destination, c.Tokens, gasSource, c.RateDelay, c.MaxFailures, c.SkipETH, c.DryRun, logger)
	}

	logger.Info("starting sweep", "network", c.Type.Network(), "keys", len(keys), "tokens", len(c.Tokens))

	if err := sw.SweepAll(ctx, keys); err != nil {
		return fmt.Errorf("sweep failed: %w", err)
	}

	logger.Info("sweep completed successfully")
	return nil
}

// validate rejects flag combinations that do not apply to the selected payment
// type, rather than silently ignoring them: each mode pays for gas differently,
// so a flag landing in the wrong mode usually means the sweep would not do what
// was asked.
func (c *RunCmd) validate() error {
	if c.Type != sweeper.PaymentTypeL17702 {
		if c.SweeperContract != (common.Address{}) {
			return fmt.Errorf("--sweeper-contract only applies to --type %s", sweeper.PaymentTypeL17702)
		}
		if c.DestinationKey != "" {
			return fmt.Errorf("--destination-key only applies to --type %s", sweeper.PaymentTypeL17702)
		}
		return nil
	}

	if c.SweeperContract == (common.Address{}) {
		return fmt.Errorf("--sweeper-contract is required for --type %s", sweeper.PaymentTypeL17702)
	}
	if c.DestinationKey == "" {
		return fmt.Errorf("--destination-key is required for --type %s: it signs and pays for every transaction", sweeper.PaymentTypeL17702)
	}
	if c.GasSource != "" {
		return fmt.Errorf("--gas-source does not apply to --type %s: the destination key pays for all gas", sweeper.PaymentTypeL17702)
	}
	if c.SkipETH {
		return fmt.Errorf("--skip-eth does not apply to --type %s: the contract always sweeps the whole ETH balance", sweeper.PaymentTypeL17702)
	}
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
	kp, err := loadKey(path)
	if err != nil {
		return nil, fmt.Errorf("gas source: %w", err)
	}
	logger.Info("gas source configured", "address", kp.Address.Hex())
	return kp, nil
}

// loadKey reads a single private key (hex, no 0x prefix) from a file.
func loadKey(path string) (*sweeper.KeyPair, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read key file: %w", err)
	}
	pk, err := crypto.HexToECDSA(strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("invalid key: %w", err)
	}
	return &sweeper.KeyPair{PrivateKey: pk, Address: crypto.PubkeyToAddress(pk.PublicKey)}, nil
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
