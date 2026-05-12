// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"

	"storj.io/sweeper"
)

func main() {
	keysPath := flag.String("keys", "", "Path to file of private keys (hex, no 0x prefix, one per line)")
	ethEndpoint := flag.String("eth-endpoint", "", "Ethereum L1 JSON-RPC endpoint")
	zksyncEndpoint := flag.String("zksync-endpoint", "", "zkSync Era JSON-RPC endpoint")
	destination := flag.String("destination", "", "Destination address for swept funds")
	ethTokensFlag := flag.String("eth-tokens", "", "Comma-separated list of ERC20 contract addresses on L1")
	zkTokensFlag := flag.String("zksync-tokens", "", "Comma-separated list of ERC20 contract addresses on zkSync Era")
	gasSourceFlag := flag.String("gas-source", "", "Path to file containing private key (hex, no 0x) of a wallet with ETH for gas funding")
	filterKeysFlag := flag.String("filter-keys", "", "Comma-separated list of public addresses to filter (only sweep these)")
	rateDelay := flag.Duration("rate-delay", 200*time.Millisecond, "Delay between RPC calls per key")
	maxFailures := flag.Int("max-failures", 25, "Maximum number of wallet failures before aborting (0 for unlimited)")
	receiptTimeout := flag.Duration("receipt-timeout", 30*time.Minute, "Maximum time to wait for a transaction to be mined")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Validate required flags.
	var missing []string
	if *keysPath == "" {
		missing = append(missing, "--keys")
	}
	if *ethEndpoint == "" {
		missing = append(missing, "--eth-endpoint")
	}
	if *zksyncEndpoint == "" {
		missing = append(missing, "--zksync-endpoint")
	}
	if *destination == "" {
		missing = append(missing, "--destination")
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "missing required flags: %s\n", strings.Join(missing, ", "))
		flag.Usage()
		os.Exit(1)
	}

	if !common.IsHexAddress(*destination) {
		fmt.Fprintf(os.Stderr, "invalid destination address: %s\n", *destination)
		os.Exit(1)
	}
	dest := common.HexToAddress(*destination)

	// Load keys.
	keys, err := sweeper.LoadKeys(*keysPath)
	if err != nil {
		logger.Error("failed to load keys", "error", err)
		os.Exit(1)
	}
	logger.Info("loaded keys", "count", len(keys))

	// Filter keys if specified.
	if *filterKeysFlag != "" {
		var filterAddrs []common.Address
		for _, addrStr := range strings.Split(*filterKeysFlag, ",") {
			addrStr = strings.TrimSpace(addrStr)
			if addrStr == "" {
				continue
			}
			if !common.IsHexAddress(addrStr) {
				fmt.Fprintf(os.Stderr, "invalid filter address: %s\n", addrStr)
				os.Exit(1)
			}
			filterAddrs = append(filterAddrs, common.HexToAddress(addrStr))
		}
		keys = sweeper.FilterKeys(keys, filterAddrs)
		logger.Info("filtered keys", "count", len(keys))
	}

	// Parse token lists.
	ethTokens := parseAddressList(*ethTokensFlag)
	zkTokens := parseAddressList(*zkTokensFlag)

	// Parse gas source.
	var gasSource *sweeper.KeyPair
	if *gasSourceFlag != "" {
		data, err := os.ReadFile(*gasSourceFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read gas source file: %v\n", err)
			os.Exit(1)
		}
		hexKey := strings.TrimSpace(string(data))
		pk, err := crypto.HexToECDSA(hexKey)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid gas source key: %v\n", err)
			os.Exit(1)
		}
		addr := crypto.PubkeyToAddress(pk.PublicKey)
		gasSource = &sweeper.KeyPair{PrivateKey: pk, Address: addr}
		logger.Info("gas source configured", "address", addr.Hex())
	}

	// Connect to endpoints.
	ctx := context.Background()

	ethClient, err := ethclient.DialContext(ctx, *ethEndpoint)
	if err != nil {
		logger.Error("failed to connect to Ethereum endpoint", "error", err)
		os.Exit(1)
	}
	defer ethClient.Close()

	zkClient, err := ethclient.DialContext(ctx, *zksyncEndpoint)
	if err != nil {
		logger.Error("failed to connect to zkSync endpoint", "error", err)
		os.Exit(1)
	}
	defer zkClient.Close()

	// Create retry-wrapped clients.
	ethRetry := sweeper.NewRetryClient(ethClient, logger)
	ethRetry.SetReceiptTimeout(*receiptTimeout)
	zkRetry := sweeper.NewRetryClient(zkClient, logger)
	zkRetry.SetReceiptTimeout(*receiptTimeout)

	// Create and run sweeper.
	sw := sweeper.NewSweeper(ethRetry, zkRetry, dest, ethTokens, zkTokens, gasSource, *rateDelay, *maxFailures, logger)

	logger.Info("starting sweep", "keys", len(keys), "ethTokens", len(ethTokens), "zkTokens", len(zkTokens))

	if err := sw.SweepAll(ctx, keys); err != nil {
		logger.Error("sweep failed", "error", err)
		os.Exit(1)
	}

	logger.Info("sweep completed successfully")
}

func parseAddressList(s string) []common.Address {
	if s == "" {
		return nil
	}
	var addrs []common.Address
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		addrs = append(addrs, common.HexToAddress(part))
	}
	return addrs
}
