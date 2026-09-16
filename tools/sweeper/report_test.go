// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func reportKeys(n int) []KeyPair {
	keys := make([]KeyPair, n)
	for i := range keys {
		var addr common.Address
		addr[19] = byte(i + 1)
		keys[i] = KeyPair{Address: addr, LineNum: i + 1}
	}
	return keys
}

func balancesOf(eth []int64, tokens ...[]int64) []WalletBalances {
	out := make([]WalletBalances, len(eth))
	for i := range eth {
		out[i].ETH = big.NewInt(eth[i])
		out[i].Tokens = make([]*big.Int, len(tokens))
		for ti := range tokens {
			out[i].Tokens[ti] = big.NewInt(tokens[ti][i])
		}
	}
	return out
}

func findReport(t *testing.T, reports []AssetReport, network, asset string) AssetReport {
	t.Helper()
	for _, r := range reports {
		if r.Network == network && r.Asset == asset {
			return r
		}
	}
	t.Fatalf("no report for %s/%s", network, asset)
	return AssetReport{}
}

func TestBuildAssetReports_RanksTopHolders(t *testing.T) {
	keys := reportKeys(5)
	balances := balancesOf([]int64{10, 50, 0, 30, 20})

	reports := BuildAssetReports("ethereum", keys, balances, nil, nil, 3)
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}

	eth := reports[0]
	if eth.Asset != "ETH" {
		t.Errorf("asset = %q, want ETH", eth.Asset)
	}
	if eth.Wallets != 5 {
		t.Errorf("wallets = %d, want 5", eth.Wallets)
	}
	if eth.Holders != 4 {
		t.Errorf("holders = %d, want 4", eth.Holders)
	}
	if eth.Total.Int64() != 110 {
		t.Errorf("total = %s, want 110", eth.Total)
	}
	if len(eth.Top) != 3 {
		t.Fatalf("top = %d entries, want 3 (truncated to topN)", len(eth.Top))
	}
	wantBalances := []int64{50, 30, 20}
	wantLines := []int{2, 4, 5}
	for i, h := range eth.Top {
		if h.Balance.Int64() != wantBalances[i] {
			t.Errorf("top[%d].balance = %s, want %d", i, h.Balance, wantBalances[i])
		}
		if h.LineNum != wantLines[i] {
			t.Errorf("top[%d].lineNum = %d, want %d", i, h.LineNum, wantLines[i])
		}
		if h.Address != keys[wantLines[i]-1].Address {
			t.Errorf("top[%d].address = %s, want %s", i, h.Address, keys[wantLines[i]-1].Address)
		}
	}
}

func TestBuildAssetReports_FewerHoldersThanTopN(t *testing.T) {
	keys := reportKeys(3)
	balances := balancesOf([]int64{0, 7, 0})

	eth := BuildAssetReports("ethereum", keys, balances, nil, nil, 10)[0]
	if len(eth.Top) != 1 {
		t.Fatalf("top = %d entries, want 1", len(eth.Top))
	}
	if eth.Top[0].Balance.Int64() != 7 {
		t.Errorf("top[0].balance = %s, want 7", eth.Top[0].Balance)
	}
}

func TestBuildAssetReports_NoHolders(t *testing.T) {
	keys := reportKeys(3)
	balances := balancesOf([]int64{0, 0, 0})

	eth := BuildAssetReports("ethereum", keys, balances, nil, nil, 10)[0]
	if eth.Holders != 0 {
		t.Errorf("holders = %d, want 0", eth.Holders)
	}
	if eth.Total.Sign() != 0 {
		t.Errorf("total = %s, want 0", eth.Total)
	}
	if len(eth.Top) != 0 {
		t.Errorf("top = %d entries, want 0", len(eth.Top))
	}
}

func TestBuildAssetReports_TokensGetOwnReports(t *testing.T) {
	keys := reportKeys(3)
	balances := balancesOf([]int64{1, 0, 0}, []int64{0, 5, 9}, []int64{0, 0, 0})
	tokens := []common.Address{
		common.HexToAddress("0x1111111111111111111111111111111111111111"),
		common.HexToAddress("0x2222222222222222222222222222222222222222"),
	}

	reports := BuildAssetReports("zksync", keys, balances, tokens, []int{6, 18}, 10)
	if len(reports) != 3 {
		t.Fatalf("expected 3 reports (ETH + 2 tokens), got %d", len(reports))
	}

	first := findReport(t, reports, "zksync", tokens[0].Hex())
	if first.Decimals != 6 {
		t.Errorf("decimals = %d, want 6", first.Decimals)
	}
	if first.Holders != 2 || first.Total.Int64() != 14 {
		t.Errorf("holders = %d, total = %s; want 2 and 14", first.Holders, first.Total)
	}
	if len(first.Top) != 2 || first.Top[0].Balance.Int64() != 9 {
		t.Errorf("top = %+v, want largest balance 9 first", first.Top)
	}

	second := findReport(t, reports, "zksync", tokens[1].Hex())
	if second.Holders != 0 {
		t.Errorf("holders = %d, want 0", second.Holders)
	}
}

func TestBuildAssetReports_MissingDecimalsAreUnknown(t *testing.T) {
	keys := reportKeys(1)
	balances := balancesOf([]int64{0}, []int64{5})
	tokens := []common.Address{common.HexToAddress("0x1111111111111111111111111111111111111111")}

	// decimals slice shorter than tokens: the token falls back to raw units.
	reports := BuildAssetReports("ethereum", keys, balances, tokens, nil, 10)
	token := findReport(t, reports, "ethereum", tokens[0].Hex())
	if token.Decimals != UnknownDecimals {
		t.Errorf("decimals = %d, want UnknownDecimals", token.Decimals)
	}
}

func TestBuildAssetReports_TiesBreakByAddress(t *testing.T) {
	keys := reportKeys(3)
	balances := balancesOf([]int64{5, 5, 5})

	eth := BuildAssetReports("ethereum", keys, balances, nil, nil, 10)[0]
	for i := 1; i < len(eth.Top); i++ {
		prev, cur := eth.Top[i-1].Address, eth.Top[i].Address
		if strings.Compare(prev.Hex(), cur.Hex()) >= 0 {
			t.Errorf("equal balances not ordered by address: %s before %s", prev, cur)
		}
	}
}

func TestBuildAssetReports_TruncatesToShorterSlice(t *testing.T) {
	// A balance slice shorter than the key list must not panic.
	keys := reportKeys(4)
	balances := balancesOf([]int64{3, 4})

	eth := BuildAssetReports("ethereum", keys, balances, nil, nil, 10)[0]
	if eth.Wallets != 2 {
		t.Errorf("wallets = %d, want 2", eth.Wallets)
	}
	if eth.Holders != 2 {
		t.Errorf("holders = %d, want 2", eth.Holders)
	}
}

func TestBuildAssetReports_CopiesBalances(t *testing.T) {
	keys := reportKeys(1)
	balances := balancesOf([]int64{7})

	eth := BuildAssetReports("ethereum", keys, balances, nil, nil, 10)[0]
	balances[0].ETH.SetInt64(99)
	if eth.Top[0].Balance.Int64() != 7 {
		t.Errorf("report balance aliased the input: got %s, want 7", eth.Top[0].Balance)
	}
}

func TestFormatUnits(t *testing.T) {
	tests := []struct {
		value    string
		decimals int
		want     string
	}{
		{"0", 18, "0"},
		{"1000000000000000000", 18, "1"},
		{"1500000000000000000", 18, "1.5"},
		{"1", 18, "0.000000000000000001"},
		{"123456", 6, "0.123456"},
		{"1000001", 6, "1.000001"},
		{"42", UnknownDecimals, "42"},
		{"42", 0, "42"},
		{"-1500000000000000000", 18, "-1.5"},
	}
	for _, tt := range tests {
		v, ok := new(big.Int).SetString(tt.value, 10)
		if !ok {
			t.Fatalf("bad test value %q", tt.value)
		}
		if got := formatUnits(v, tt.decimals); got != tt.want {
			t.Errorf("formatUnits(%s, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
		}
	}
	if got := formatUnits(nil, 18); got != "0" {
		t.Errorf("formatUnits(nil, 18) = %q, want %q", got, "0")
	}
}

func TestWriteReport(t *testing.T) {
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	keys := reportKeys(2)
	reports := []AssetReport{
		{
			Network: "ethereum", Asset: "ETH", Decimals: 18, Wallets: 2, Holders: 1,
			Total: big.NewInt(1500000000000000000),
			Top:   []AddressBalance{{Address: keys[0].Address, LineNum: 1, Balance: big.NewInt(1500000000000000000)}},
		},
		{
			Network: "ethereum", Asset: token.Hex(), Decimals: UnknownDecimals, Wallets: 2, Holders: 0,
			Total: new(big.Int),
		},
		{
			Network: "zksync", Asset: "ETH", Decimals: 18, Wallets: 2, Holders: 0,
			Total: new(big.Int),
		},
	}

	var out strings.Builder
	if err := WriteReport(&out, reports); err != nil {
		t.Fatal(err)
	}
	got := out.String()

	for _, want := range []string{
		"=== ethereum ===",
		"=== zksync ===",
		"=== summary ===",
		"holders: 1 of 2 wallets",
		"total:   1.5",
		keys[0].Address.Hex(),
		"raw units, decimals() unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}

	// Each network heading is printed once, no matter how many assets it has.
	if n := strings.Count(got, "=== ethereum ==="); n != 1 {
		t.Errorf("ethereum heading printed %d times, want 1", n)
	}
	// Assets with no holders print no ranking table.
	if n := strings.Count(got, "rank"); n != 1 {
		t.Errorf("rank header printed %d times, want 1", n)
	}
}

func TestReporter_QueriesConfiguredPaymentType(t *testing.T) {
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")
	keys := reportKeys(2)

	// wallet 1 holds 5 wei of ETH, wallet 2 holds 7 units of the token.
	newClient := func(ethOne, tokenTwo int64) *mockClient {
		return &mockClient{
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				if *msg.To == token && len(msg.Data) == 4 {
					// decimals() — this token does not implement it.
					return nil, errors.New("execution reverted")
				}
				// aggregate3: ETH+token per wallet, in wallet order.
				return encodeAggregate3Response([]*big.Int{
					big.NewInt(ethOne), new(big.Int),
					new(big.Int), big.NewInt(tokenTwo),
				}), nil
			},
		}
	}

	l1 := NewReporter(newClient(5, 7), PaymentTypeL1, []common.Address{token}, 10, testLogger())
	reports, err := l1.Report(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports (ETH + 1 token), got %d", len(reports))
	}

	eth := findReport(t, reports, "ethereum", "ETH")
	if eth.Holders != 1 || eth.Total.Int64() != 5 {
		t.Errorf("ethereum ETH: holders = %d, total = %s; want 1 and 5", eth.Holders, eth.Total)
	}
	// The dust filter must not apply: 5 wei is far below any sweep threshold
	// but is still a real balance worth reporting.
	if len(eth.Top) != 1 || eth.Top[0].LineNum != 1 {
		t.Errorf("ethereum ETH top = %+v, want the wallet on line 1", eth.Top)
	}

	tok := findReport(t, reports, "ethereum", token.Hex())
	if tok.Decimals != UnknownDecimals {
		t.Errorf("decimals = %d, want UnknownDecimals after a failed decimals() call", tok.Decimals)
	}
	if tok.Holders != 1 || tok.Total.Int64() != 7 {
		t.Errorf("ethereum token: holders = %d, total = %s; want 1 and 7", tok.Holders, tok.Total)
	}

	// The same keys under the L2 payment type are reported with the L2 network name.
	l2 := NewReporter(newClient(0, 0), PaymentTypeL2, []common.Address{token}, 10, testLogger())
	zkReports, err := l2.Report(context.Background(), keys)
	if err != nil {
		t.Fatal(err)
	}
	zk := findReport(t, zkReports, "zksync", "ETH")
	if zk.Holders != 0 {
		t.Errorf("zksync ETH holders = %d, want 0", zk.Holders)
	}
}

func TestReporter_MulticallError(t *testing.T) {
	client := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return nil, errors.New("rpc down")
		},
	}
	reporter := NewReporter(client, PaymentTypeL1, nil, 10, testLogger())

	_, err := reporter.Report(context.Background(), reportKeys(1))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "ethereum") {
		t.Errorf("error %q should name the failing network", err)
	}
}

func TestERC20Decimals(t *testing.T) {
	token := common.HexToAddress("0x1111111111111111111111111111111111111111")

	word := func(v int64) []byte {
		b := make([]byte, 32)
		big.NewInt(v).FillBytes(b)
		return b
	}

	t.Run("valid", func(t *testing.T) {
		client := &mockClient{
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				if len(msg.Data) != 4 || msg.Data[0] != 0x31 {
					t.Errorf("unexpected calldata %x", msg.Data)
				}
				return word(6), nil
			},
		}
		got, err := ERC20Decimals(context.Background(), client, token)
		if err != nil {
			t.Fatal(err)
		}
		if got != 6 {
			t.Errorf("decimals = %d, want 6", got)
		}
	})

	t.Run("call error", func(t *testing.T) {
		client := &mockClient{
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				return nil, errors.New("execution reverted")
			},
		}
		if _, err := ERC20Decimals(context.Background(), client, token); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("short response", func(t *testing.T) {
		client := &mockClient{
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				return []byte{0x06}, nil
			},
		}
		if _, err := ERC20Decimals(context.Background(), client, token); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("out of uint8 range", func(t *testing.T) {
		client := &mockClient{
			callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
				return word(1000), nil
			},
		}
		if _, err := ERC20Decimals(context.Background(), client, token); err == nil {
			t.Fatal("expected an error")
		}
	})
}
