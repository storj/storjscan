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
// Kept at 100 to stay within Infura's eth_call sub-call limits on standard tiers.
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
// ABI layout (all offsets in 32-byte words):
//
//	[0]  selector (4 bytes) + offset to array (32 bytes) = 36 bytes head
//	     offset = 0x20 (points past the 32-byte length word)
//	[1]  array length
//	[2+] array elements, each tuple encoded inline:
//	       word 0: target address (right-padded to 32 bytes)
//	       word 1: allowFailure bool (0)
//	       word 2: offset to callData bytes (relative to start of this tuple)
//	       ...callData bytes...
func encodeAggregate3(calls []callDesc) ([]byte, error) {
	n := len(calls)

	// Each call has fixed head: target(32) + allowFailure(32) + dataOffset(32) = 96 bytes
	// plus the bytes payload: length(32) + data padded to 32-byte boundary.
	// We also need the outer ABI head: selector(4) + arrayOffset(32) + arrayLen(32).

	// First pass: compute per-element data sizes and total size.
	type encodedCall struct {
		target [32]byte
		data   []byte
	}
	encoded := make([]encodedCall, n)
	for i, c := range calls {
		copy(encoded[i].target[12:], c.target.Bytes())
		encoded[i].data = c.data
	}

	// Build the buffer.
	// Outer header: 4 (selector) + 32 (array offset = 0x20) + 32 (array length)
	// Per element head (3 words each): 96 bytes
	// Per element tail: 32 (data length) + len(data) rounded up to 32
	buf := make([]byte, 0, 4+32+32+n*96)

	// selector
	buf = append(buf, aggregate3MethodID[:]...)

	// offset to array (0x20 = 32, pointing right after this word)
	buf = appendUint256(buf, 0x20)

	// array length
	buf = appendUint256(buf, uint64(n))

	// For each call we need to know the offset of its `bytes callData` relative
	// to the start of the tuple. Each tuple head is 3 words (96 bytes). The
	// bytes data for call i starts after all the tuple heads of all n calls,
	// then after all the bytes payloads of calls 0..i-1.
	//
	// offset_i = (n-i-1)*96 (remaining heads after this tuple's head)
	//           + sum(32 + roundUp32(len(data[j])) for j < i)
	//
	// We build the heads first, then append the tails.

	tailBuf := make([]byte, 0, n*64)
	dataOffset := uint64((n) * 96) // offset from start of first tuple to first tail

	// We need to track the running offset into tails for each element.
	offsets := make([]uint64, n)
	runningOffset := uint64(0)
	for i, ec := range encoded {
		offsets[i] = dataOffset - uint64(i)*96 + runningOffset
		_ = ec
		dataLen := uint64(len(encoded[i].data))
		paddedLen := (dataLen + 31) &^ 31
		runningOffset += 32 + paddedLen
	}

	for i, ec := range encoded {
		// target
		buf = append(buf, ec.target[:]...)
		// allowFailure = false
		buf = appendUint256(buf, 0)
		// offset to callData bytes, relative to start of this tuple
		buf = appendUint256(buf, offsets[i])

		// tail: length + padded data
		dataLen := uint64(len(ec.data))
		paddedLen := (dataLen + 31) &^ 31
		tailBuf = appendUint256(tailBuf, dataLen)
		tailBuf = append(tailBuf, ec.data...)
		for pad := dataLen; pad < paddedLen; pad++ {
			tailBuf = append(tailBuf, 0)
		}
	}

	buf = append(buf, tailBuf...)
	return buf, nil
}

// decodeAggregate3Results decodes the ABI-encoded return value of aggregate3.
//
// Return type: (bool success, bytes returnData)[]
//
// Layout:
//
//	[0]  offset to array (0x20)
//	[1]  array length
//	[2+] per-element: success(32) + dataOffset(32) ... bytes data
func decodeAggregate3Results(data []byte, n int) ([][]byte, error) {
	// Minimum: 32 (outer offset) + 32 (length) = 64 bytes
	if len(data) < 64 {
		return nil, fmt.Errorf("response too short: %d bytes", len(data))
	}

	// outer offset (we trust it's 0x20)
	arrayStart := int(new(big.Int).SetBytes(safeSlice(data, 0, 32)).Uint64())
	if arrayStart+32 > len(data) {
		return nil, fmt.Errorf("array start out of bounds")
	}
	arrayLen := int(new(big.Int).SetBytes(safeSlice(data, arrayStart, arrayStart+32)).Uint64())
	if arrayLen != n {
		return nil, fmt.Errorf("unexpected result count: got %d, want %d", arrayLen, n)
	}

	// Each element head is at arrayStart+32 + i*64 (success word + data-offset word).
	results := make([][]byte, n)
	elemBase := arrayStart + 32
	for i := range n {
		elemHeadOff := elemBase + i*64
		if elemHeadOff+64 > len(data) {
			return nil, fmt.Errorf("element %d head out of bounds", i)
		}

		// success word
		success := data[elemHeadOff+31] != 0

		// offset to bytes returnData, relative to start of array content (elemBase)
		dataRelOff := int(new(big.Int).SetBytes(safeSlice(data, elemHeadOff+32, elemHeadOff+64)).Uint64())
		dataAbsOff := elemBase + dataRelOff
		if dataAbsOff+32 > len(data) {
			return nil, fmt.Errorf("element %d data offset out of bounds", i)
		}

		bytesLen := int(new(big.Int).SetBytes(safeSlice(data, dataAbsOff, dataAbsOff+32)).Uint64())
		if dataAbsOff+32+bytesLen > len(data) {
			return nil, fmt.Errorf("element %d data out of bounds", i)
		}
		payload := data[dataAbsOff+32 : dataAbsOff+32+bytesLen]

		if !success || len(payload) < 32 {
			// Treat failed or empty calls as zero balance.
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
