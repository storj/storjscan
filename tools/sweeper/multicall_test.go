package sweeper

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// encodeAggregate3Response builds a synthetic aggregate3 return value for
// testing. Each value in vals is encoded as a successful (bool success, bytes
// returnData) tuple where returnData is the 32-byte big-endian representation
// of the value.
func encodeAggregate3Response(vals []*big.Int) []byte {
	n := len(vals)

	// Layout (all sizes in bytes):
	//   32  outer offset (= 0x20)
	//   32  array length
	//   n*64  element heads: success(32) + dataOffset(32)
	//   n*64  element tails: bytesLen(32) + 32-byte payload

	// dataOffset for element i is relative to the start of the array content
	// (i.e., right after the outer-offset and length words).
	// Array content starts at byte 64 (0x40) from the start of the return data.
	// Element heads occupy n*64 bytes.
	// Element i's tail starts at n*64 + i*64 bytes from the start of array content.

	buf := make([]byte, 0, 64+n*128)

	// outer offset
	buf = appendUint256(buf, 0x20)
	// array length
	buf = appendUint256(buf, uint64(n))

	// heads
	for i := range n {
		// success = true
		buf = appendUint256(buf, 1)
		// offset to bytes returnData, relative to start of array content (after length word)
		// = n*64 (all heads) + i*64 (preceding tails)
		buf = appendUint256(buf, uint64(n*64+i*64))
	}

	// tails
	for _, v := range vals {
		// bytes length = 32
		buf = appendUint256(buf, 32)
		// 32-byte value
		var word [32]byte
		v.FillBytes(word[:])
		buf = append(buf, word[:]...)
	}

	return buf
}

func TestMulticallBalances_AllZero(t *testing.T) {
	wallet := common.HexToAddress("0x1111")
	token := common.HexToAddress("0xtoken")

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			// 2 calls: ETH balance + 1 token balance, both zero
			return encodeAggregate3Response([]*big.Int{big.NewInt(0), big.NewInt(0)}), nil
		},
	}

	results, err := MulticallBalances(context.Background(), testLogger(), mock, []common.Address{wallet}, []common.Address{token}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].HasFunds() {
		t.Fatal("expected no funds")
	}
}

func TestMulticallBalances_ETHNonZero(t *testing.T) {
	wallet := common.HexToAddress("0x1111")

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return encodeAggregate3Response([]*big.Int{big.NewInt(1e18)}), nil
		},
	}

	results, err := MulticallBalances(context.Background(), testLogger(), mock, []common.Address{wallet}, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !results[0].HasFunds() {
		t.Fatal("expected funds")
	}
	if results[0].ETH.Cmp(big.NewInt(1e18)) != 0 {
		t.Fatalf("unexpected ETH balance: %v", results[0].ETH)
	}
}

func TestMulticallBalances_TokenNonZero(t *testing.T) {
	wallet := common.HexToAddress("0x1111")
	token := common.HexToAddress("0xtoken")

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			// ETH=0, token=500
			return encodeAggregate3Response([]*big.Int{big.NewInt(0), big.NewInt(500)}), nil
		},
	}

	results, err := MulticallBalances(context.Background(), testLogger(), mock, []common.Address{wallet}, []common.Address{token}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !results[0].HasFunds() {
		t.Fatal("expected funds")
	}
	if results[0].Tokens[0].Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("unexpected token balance: %v", results[0].Tokens[0])
	}
}

func TestMulticallBalances_MultipleWallets(t *testing.T) {
	wallets := []common.Address{
		common.HexToAddress("0x1111"),
		common.HexToAddress("0x2222"),
		common.HexToAddress("0x3333"),
	}

	// No tokens — one ETH balance call per wallet, so 3 values total in one batch.
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return encodeAggregate3Response([]*big.Int{
				big.NewInt(0),    // wallet 0: empty
				big.NewInt(1e18), // wallet 1: has ETH
				big.NewInt(0),    // wallet 2: empty
			}), nil
		},
	}

	results, err := MulticallBalances(context.Background(), testLogger(), mock, wallets, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0].HasFunds() {
		t.Fatal("wallet 0 should have no funds")
	}
	if !results[1].HasFunds() {
		t.Fatal("wallet 1 should have funds")
	}
	if results[2].HasFunds() {
		t.Fatal("wallet 2 should have no funds")
	}
}

func TestMulticallBalances_Batching(t *testing.T) {
	// Create enough wallets to require 2 batches (batchSize=500, one call each).
	n := multicallBatchSize + 10
	wallets := make([]common.Address, n)
	for i := range wallets {
		wallets[i] = common.BigToAddress(big.NewInt(int64(i + 1)))
	}

	var batchCount int
	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			batchCount++
			// Figure out how many calls are in this batch from the calldata length
			// and return the right number of zero values.
			// We can't easily decode the calldata here, so return a fixed-size
			// response matching the expected batch size.
			size := multicallBatchSize
			if batchCount == 2 {
				size = 10
			}
			vals := make([]*big.Int, size)
			for i := range vals {
				vals[i] = big.NewInt(0)
			}
			return encodeAggregate3Response(vals), nil
		},
	}

	results, err := MulticallBalances(context.Background(), testLogger(), mock, wallets, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != n {
		t.Fatalf("expected %d results, got %d", n, len(results))
	}
	if batchCount != 2 {
		t.Fatalf("expected 2 batches, got %d", batchCount)
	}
}

func TestMulticallBalances_Empty(t *testing.T) {
	mock := &mockClient{}
	results, err := MulticallBalances(context.Background(), testLogger(), mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got %d", len(results))
	}
}

func TestMulticallBalances_RPCError(t *testing.T) {
	wallet := common.HexToAddress("0x1111")

	mock := &mockClient{
		callContractFn: func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
			return nil, context.DeadlineExceeded
		},
	}

	_, err := MulticallBalances(context.Background(), testLogger(), mock, []common.Address{wallet}, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestSweepAll_SkipsEmptyWalletsViaMulticall verifies that SweepAll does not
// call sweepKey (and thus sends no transactions) for wallets that have zero
// balances according to the multicall pre-check.
func TestSweepAll_SkipsEmptyWalletsViaMulticall(t *testing.T) {
	kp := newTestKey(t)

	var txSent bool
	mock := fullMock()
	// multicall returns all zeros
	mock.callContractFn = func(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
		if *msg.To == multicall3Address {
			// One call: ETH balance = 0
			return encodeAggregate3Response([]*big.Int{big.NewInt(0)}), nil
		}
		return make([]byte, 32), nil
	}
	mock.sendTransactionFn = func(ctx context.Context, tx *types.Transaction) error {
		txSent = true
		return nil
	}

	sw := NewSweeper(mock, mock, common.Address{}, nil, nil, nil, 0, 0, testLogger())
	if err := sw.SweepAll(context.Background(), []KeyPair{kp}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txSent {
		t.Fatal("expected no transactions for empty wallet")
	}
}
