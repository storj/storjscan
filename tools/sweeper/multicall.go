package sweeper

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// multicall3Address is the canonical Multicall3 deployment address, the same
// on Ethereum mainnet, zkSync Era, and virtually every other EVM chain.
// See https://github.com/mds1/multicall
var multicall3Address = common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11")

// aggregate3MethodID is the 4-byte selector for aggregate3((address,bool,bytes)[]).
var aggregate3MethodID = [4]byte{0x82, 0xad, 0x56, 0xcb}

// multicallBatchSize is the number of individual calls packed into one eth_call.
// Kept at 100 to stay within RPC provider eth_call sub-call limits.
const multicallBatchSize = 100

// WalletBalances holds the ETH and ERC20 balances for a single wallet returned
// by a multicall query.
type WalletBalances struct {
	ETH    *big.Int
	Tokens []*big.Int // parallel to the tokens slice passed to MulticallBalances
}

// HasFunds returns true if any balance is non-zero.
func (wb WalletBalances) HasFunds() bool {
	if wb.ETH != nil && wb.ETH.Sign() > 0 {
		return true
	}
	for _, t := range wb.Tokens {
		if t != nil && t.Sign() > 0 {
			return true
		}
	}
	return false
}

// callDesc describes a single sub-call within an aggregate3 batch.
type callDesc struct {
	walletIdx int
	tokenIdx  int // -1 means ETH balance
	target    common.Address
	data      []byte
}

// MulticallBalances queries ETH and ERC20 balances for all wallets in batches
// using Multicall3. Returns one WalletBalances per entry in wallets, in the
// same order. ETH balances at or below minETH are treated as dust and zeroed
// out; pass nil to keep all positive ETH balances.
func MulticallBalances(ctx context.Context, logger *slog.Logger, client BlockchainClient, wallets []common.Address, tokens []common.Address, minETH *big.Int) ([]WalletBalances, error) {
	if len(wallets) == 0 {
		return nil, nil
	}

	// Number of calls per wallet: 1 ETH balance + 1 per token.
	callsPerWallet := 1 + len(tokens)
	totalCalls := len(wallets) * callsPerWallet

	allCalls := make([]callDesc, 0, totalCalls)
	for wi, wallet := range wallets {
		// ETH balance via Multicall3.getEthBalance(address)
		allCalls = append(allCalls, callDesc{
			walletIdx: wi,
			tokenIdx:  -1,
			target:    multicall3Address,
			data:      encodeGetEthBalance(wallet),
		})
		// ERC20 balanceOf(wallet) for each token
		for ti, token := range tokens {
			allCalls = append(allCalls, callDesc{
				walletIdx: wi,
				tokenIdx:  ti,
				target:    token,
				data:      encodeBalanceOf(wallet),
			})
		}
	}

	results := make([]WalletBalances, len(wallets))
	for i := range results {
		results[i].ETH = new(big.Int)
		results[i].Tokens = make([]*big.Int, len(tokens))
		for j := range results[i].Tokens {
			results[i].Tokens[j] = new(big.Int)
		}
	}

	// Process in batches.
	totalBatches := (len(allCalls) + multicallBatchSize - 1) / multicallBatchSize
	for start := 0; start < len(allCalls); start += multicallBatchSize {
		end := start + multicallBatchSize
		if end > len(allCalls) {
			end = len(allCalls)
		}
		batch := allCalls[start:end]
		batchNum := start/multicallBatchSize + 1
		logger.Info("executing multicall batch", "batch", batchNum, "of", totalBatches, "calls", len(batch))

		raw, err := callAggregate3(ctx, client, batch)
		if err != nil {
			return nil, fmt.Errorf("multicall batch %d-%d: %w", start, end, err)
		}

		for i, cd := range batch {
			val := new(big.Int).SetBytes(raw[i])
			if cd.tokenIdx == -1 {
				if minETH != nil && val.Cmp(minETH) <= 0 {
					val = new(big.Int) // treat as dust
				}
				results[cd.walletIdx].ETH = val
			} else {
				results[cd.walletIdx].Tokens[cd.tokenIdx] = val
			}
		}
	}

	return results, nil
}

// callAggregate3 encodes and executes a single aggregate3 call, returning the
// raw 32-byte return data for each sub-call (only successful ones are expected;
// allowFailure is false for all).
func callAggregate3(ctx context.Context, client BlockchainClient, calls []callDesc) ([][]byte, error) {
	calldata, err := encodeAggregate3(calls)
	if err != nil {
		return nil, err
	}

	mc := multicall3Address
	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &mc,
		Data: calldata,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("eth_call: %w", err)
	}

	return decodeAggregate3Results(result, len(calls))
}

// encodeAggregate3 ABI-encodes the aggregate3 call for the given set of calls.
//
// aggregate3((address target, bool allowFailure, bytes callData)[])
//
// The array element type is a dynamic tuple (it contains a `bytes` field), so
// the ABI encoding requires two layers of indirection:
//
//	selector (4 bytes)
//	offset to array content (0x20)          ← points past this word
//	array length (n)
//	n × per-element offset words            ← each points to that element's encoding
//	n × element encodings, each:
//	  target       (32 bytes)
//	  allowFailure (32 bytes, 1 = true)
//	  offset to callData bytes (relative to start of this element's encoding, always 0x60)
//	  callData length (32 bytes)
//	  callData padded to 32-byte boundary
func encodeAggregate3(calls []callDesc) ([]byte, error) {
	n := len(calls)

	// Compute per-element encoded sizes for building the element-offset table.
	// Each element encodes as: 3×32 (head) + 32 (bytes length) + roundUp32(data).
	elemSizes := make([]uint64, n)
	for i, c := range calls {
		dataLen := uint64(len(c.data))
		paddedLen := (dataLen + 31) &^ 31
		elemSizes[i] = 96 + 32 + paddedLen
	}

	// Per-element offset table: offsets are relative to the start of array content
	// (right after the length word). The table itself is n×32 bytes; element i
	// starts after the table and all preceding elements.
	elemOffsets := make([]uint64, n)
	base := uint64(n * 32) // table size
	for i := range n {
		elemOffsets[i] = base
		base += elemSizes[i]
	}

	var buf []byte
	buf = append(buf, aggregate3MethodID[:]...)
	buf = appendUint256(buf, 0x20) // offset to array content
	buf = appendUint256(buf, uint64(n))

	// Per-element offset table.
	for _, off := range elemOffsets {
		buf = appendUint256(buf, off)
	}

	// Element encodings.
	for _, c := range calls {
		var target [32]byte
		copy(target[12:], c.target.Bytes())
		buf = append(buf, target[:]...)
		buf = appendUint256(buf, 1)    // allowFailure = true
		buf = appendUint256(buf, 0x60) // offset to callData bytes relative to this element (always 3×32)

		dataLen := uint64(len(c.data))
		paddedLen := (dataLen + 31) &^ 31
		buf = appendUint256(buf, dataLen)
		buf = append(buf, c.data...)
		for pad := dataLen; pad < paddedLen; pad++ {
			buf = append(buf, 0)
		}
	}

	return buf, nil
}

// decodeAggregate3Results decodes the ABI-encoded return value of aggregate3.
//
// Return type: (bool success, bytes returnData)[]
//
// Because the tuple contains a bytes field it is dynamic, so the array uses a
// per-element offset table (same two-level indirection as the encoding):
//
//	[0]   outer offset (0x20) → array content starts at [32]
//	[32]  array length n
//	[64]  n × per-element offset words (relative to start of array content, i.e. [32])
//	      each offset points to the element encoding:
//	        success (32 bytes)
//	        offset to returnData bytes relative to start of element (32 bytes, always 0x40)
//	        bytes length (32 bytes)
//	        bytes data (padded to 32)
func decodeAggregate3Results(data []byte, n int) ([][]byte, error) {
	if len(data) < 64 {
		return nil, fmt.Errorf("response too short: %d bytes", len(data))
	}

	arrayStart := int(new(big.Int).SetBytes(safeSlice(data, 0, 32)).Uint64())
	if arrayStart+32 > len(data) {
		return nil, fmt.Errorf("array start out of bounds")
	}
	// arrayContent is where the length word + offset table live; offsets are relative to it.
	arrayContent := arrayStart
	arrayLen := int(new(big.Int).SetBytes(safeSlice(data, arrayContent, arrayContent+32)).Uint64())
	if arrayLen != n {
		return nil, fmt.Errorf("unexpected result count: got %d, want %d", arrayLen, n)
	}

	// Per-element offset table starts right after the length word.
	// Each offset is relative to the start of the offset table (not arrayContent).
	offsetTableBase := arrayContent + 32

	results := make([][]byte, n)
	for i := range n {
		// Read the per-element offset (relative to offsetTableBase).
		offWord := offsetTableBase + i*32
		if offWord+32 > len(data) {
			return nil, fmt.Errorf("element %d offset word out of bounds", i)
		}
		elemRelOff := int(new(big.Int).SetBytes(safeSlice(data, offWord, offWord+32)).Uint64())
		elemStart := offsetTableBase + elemRelOff

		// Element layout: success(32) + bytesOffset(32) + bytesLen(32) + bytes
		if elemStart+64 > len(data) {
			return nil, fmt.Errorf("element %d head out of bounds", i)
		}
		success := data[elemStart+31] != 0

		// offset to returnData bytes, relative to start of this element
		bytesRelOff := int(new(big.Int).SetBytes(safeSlice(data, elemStart+32, elemStart+64)).Uint64())
		bytesLenOff := elemStart + bytesRelOff
		if bytesLenOff+32 > len(data) {
			return nil, fmt.Errorf("element %d data offset out of bounds", i)
		}

		bytesLen := int(new(big.Int).SetBytes(safeSlice(data, bytesLenOff, bytesLenOff+32)).Uint64())
		if bytesLenOff+32+bytesLen > len(data) {
			return nil, fmt.Errorf("element %d data out of bounds", i)
		}
		payload := data[bytesLenOff+32 : bytesLenOff+32+bytesLen]

		if !success || len(payload) < 32 {
			results[i] = make([]byte, 32)
		} else {
			results[i] = payload[len(payload)-32:]
		}
	}
	return results, nil
}

// encodeGetEthBalance encodes Multicall3.getEthBalance(address).
// selector: 0x4d2301cc
func encodeGetEthBalance(addr common.Address) []byte {
	data := make([]byte, 4+32)
	data[0], data[1], data[2], data[3] = 0x4d, 0x23, 0x01, 0xcc
	copy(data[4+12:], addr.Bytes())
	return data
}

// encodeBalanceOf encodes ERC20 balanceOf(address).
func encodeBalanceOf(addr common.Address) []byte {
	data := make([]byte, 4+32)
	copy(data[0:4], balanceOfMethodID[:])
	copy(data[4+12:], addr.Bytes())
	return data
}

func appendUint256(buf []byte, v uint64) []byte {
	var word [32]byte
	binary.BigEndian.PutUint64(word[24:], v)
	return append(buf, word[:]...)
}

func safeSlice(data []byte, from, to int) []byte {
	if to > len(data) {
		return make([]byte, to-from)
	}
	return data[from:to]
}
