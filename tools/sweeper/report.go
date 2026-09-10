// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"sort"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// UnknownDecimals marks a token whose decimals() call failed. Balances for
// such tokens are reported in raw contract units.
const UnknownDecimals = -1

// ethAsset is the label used for a network's native currency.
const ethAsset = "ETH"

// AddressBalance pairs a wallet with the balance it holds.
type AddressBalance struct {
	Address common.Address
	LineNum int // line the key was read from in the key file
	Balance *big.Int
}

// AssetReport summarises how a single asset (ETH or one ERC20 token) is spread
// across the scanned wallets on one network.
type AssetReport struct {
	Network  string
	Asset    string // "ETH", or the token contract address
	Decimals int    // UnknownDecimals when the token does not expose decimals()
	Wallets  int    // wallets scanned
	Holders  int    // wallets holding a non-zero balance
	Total    *big.Int
	Top      []AddressBalance // largest holders first, at most topN entries
}

// ReportNetwork identifies one chain to report on.
type ReportNetwork struct {
	Name   string
	Client BlockchainClient
	Tokens []common.Address
}

// Reporter reads balances across networks and summarises them. It never sends
// a transaction.
type Reporter struct {
	networks []ReportNetwork
	topN     int
	logger   *slog.Logger
}

// NewReporter creates a Reporter listing the topN largest holders per asset.
func NewReporter(networks []ReportNetwork, topN int, logger *slog.Logger) *Reporter {
	return &Reporter{
		networks: networks,
		topN:     topN,
		logger:   logger,
	}
}

// Report queries every configured network for the balances of keys and returns
// one AssetReport per network asset.
func (r *Reporter) Report(ctx context.Context, keys []KeyPair) ([]AssetReport, error) {
	addrs := make([]common.Address, len(keys))
	for i, kp := range keys {
		addrs[i] = kp.Address
	}

	var reports []AssetReport
	for _, net := range r.networks {
		r.logger.Info("querying balances via multicall", "network", net.Name, "wallets", len(addrs))

		// minETH is nil on purpose: a report should show the balances that are
		// really there, including dust the sweeper would decline to move.
		balances, err := MulticallBalances(ctx, r.logger, net.Client, addrs, net.Tokens, nil, false)
		if err != nil {
			return nil, fmt.Errorf("multicall %s balances: %w", net.Name, err)
		}

		decimals := make([]int, len(net.Tokens))
		for i, token := range net.Tokens {
			d, err := ERC20Decimals(ctx, net.Client, token)
			if err != nil {
				r.logger.Warn("failed to read token decimals, reporting raw units", "network", net.Name, "token", token.Hex(), "error", err)
				d = UnknownDecimals
			}
			decimals[i] = d
		}

		reports = append(reports, BuildAssetReports(net.Name, keys, balances, net.Tokens, decimals, r.topN)...)
	}
	return reports, nil
}

// BuildAssetReports turns raw per-wallet balances into one AssetReport per
// asset, ranking holders by balance. balances is parallel to keys, and decimals
// is parallel to tokens; pass UnknownDecimals for a token whose scale is not
// known.
func BuildAssetReports(network string, keys []KeyPair, balances []WalletBalances, tokens []common.Address, decimals []int, topN int) []AssetReport {
	// balances[i] describes keys[i]; ignore any trailing entries of either.
	n := min(len(keys), len(balances))

	reports := make([]AssetReport, 0, len(tokens)+1)
	reports = append(reports, buildAssetReport(network, ethAsset, 18, keys, balances[:n], topN, func(wb WalletBalances) *big.Int {
		return wb.ETH
	}))
	for ti, token := range tokens {
		dec := UnknownDecimals
		if ti < len(decimals) {
			dec = decimals[ti]
		}
		reports = append(reports, buildAssetReport(network, token.Hex(), dec, keys, balances[:n], topN, func(wb WalletBalances) *big.Int {
			if ti >= len(wb.Tokens) {
				return nil
			}
			return wb.Tokens[ti]
		}))
	}
	return reports
}

func buildAssetReport(network, asset string, decimals int, keys []KeyPair, balances []WalletBalances, topN int, balanceOf func(WalletBalances) *big.Int) AssetReport {
	report := AssetReport{
		Network:  network,
		Asset:    asset,
		Decimals: decimals,
		Wallets:  len(balances),
		Total:    new(big.Int),
	}

	var holders []AddressBalance
	for i, wb := range balances {
		balance := balanceOf(wb)
		if balance == nil || balance.Sign() <= 0 {
			continue
		}
		report.Holders++
		report.Total.Add(report.Total, balance)
		holders = append(holders, AddressBalance{
			Address: keys[i].Address,
			LineNum: keys[i].LineNum,
			Balance: new(big.Int).Set(balance),
		})
	}

	// Largest first; equal balances fall back to address order so the output is
	// stable across runs.
	sort.Slice(holders, func(i, j int) bool {
		if c := holders[i].Balance.Cmp(holders[j].Balance); c != 0 {
			return c > 0
		}
		return bytes.Compare(holders[i].Address.Bytes(), holders[j].Address.Bytes()) < 0
	})
	if topN >= 0 && len(holders) > topN {
		holders = holders[:topN]
	}
	report.Top = holders

	return report
}

// WriteReport renders reports as plain text: the largest holders per asset,
// followed by a combined totals table.
func WriteReport(w io.Writer, reports []AssetReport) error {
	buf := &strings.Builder{}

	network := ""
	for _, r := range reports {
		if r.Network != network {
			network = r.Network
			fmt.Fprintf(buf, "\n=== %s ===\n", network)
		}

		unit := ""
		if r.Decimals == UnknownDecimals {
			unit = " (raw units, decimals() unavailable)"
		}
		fmt.Fprintf(buf, "\n%s%s\n", r.Asset, unit)
		fmt.Fprintf(buf, "  holders: %d of %d wallets\n", r.Holders, r.Wallets)
		fmt.Fprintf(buf, "  total:   %s\n", formatUnits(r.Total, r.Decimals))

		if len(r.Top) == 0 {
			continue
		}
		fmt.Fprintf(buf, "\n  %4s  %-42s  %8s  %s\n", "rank", "address", "key line", "balance")
		for i, h := range r.Top {
			fmt.Fprintf(buf, "  %4d  %-42s  %8d  %s\n", i+1, h.Address.Hex(), h.LineNum, formatUnits(h.Balance, r.Decimals))
		}
	}

	fmt.Fprintf(buf, "\n=== summary ===\n\n")
	fmt.Fprintf(buf, "  %-10s  %-42s  %8s  %s\n", "network", "asset", "holders", "total")
	for _, r := range reports {
		fmt.Fprintf(buf, "  %-10s  %-42s  %8d  %s\n", r.Network, r.Asset, r.Holders, formatUnits(r.Total, r.Decimals))
	}

	_, err := io.WriteString(w, buf.String())
	return err
}

// formatUnits renders a raw integer balance scaled by decimals, trimming
// trailing fractional zeros. Balances with UnknownDecimals (or a token that
// genuinely uses zero decimals) are returned as-is.
func formatUnits(v *big.Int, decimals int) string {
	if v == nil {
		return "0"
	}
	if decimals <= 0 {
		return v.String()
	}
	denom := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	whole, frac := new(big.Int).QuoRem(new(big.Int).Abs(v), denom, new(big.Int))

	digits := frac.String()
	if len(digits) < decimals {
		digits = strings.Repeat("0", decimals-len(digits)) + digits
	}
	digits = strings.TrimRight(digits, "0")

	sign := ""
	if v.Sign() < 0 {
		sign = "-"
	}
	if digits == "" {
		return sign + whole.String()
	}
	return sign + whole.String() + "." + digits
}
