// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

// BlockchainClient defines the interface for interacting with an Ethereum-compatible blockchain.
type BlockchainClient interface {
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error)
	SendTransaction(ctx context.Context, tx *types.Transaction) error
	ChainID(ctx context.Context) (*big.Int, error)
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error)
}

// RetryClient wraps a BlockchainClient with exponential backoff retry logic.
type RetryClient struct {
	client         BlockchainClient
	logger         *slog.Logger
	receiptTimeout time.Duration
	minGasTipCap   *big.Int

	// A load-balanced endpoint (Infura) can answer from a backend that has not
	// seen the latest block yet, and report a pending nonce the sender already
	// used. sent remembers who sent which transaction, so once it is mined
	// minedNonce can hold the sender's next nonce up against such stale answers.
	mu         sync.Mutex
	sent       map[common.Hash]sentTx
	minedNonce map[common.Address]uint64
}

// sentTx is the sender and nonce of a transaction sent through RetryClient.
type sentTx struct {
	from  common.Address
	nonce uint64
}

// NewRetryClient creates a new RetryClient wrapping the given BlockchainClient.
func NewRetryClient(client BlockchainClient, logger *slog.Logger) *RetryClient {
	return &RetryClient{
		client:         client,
		logger:         logger,
		receiptTimeout: 30 * time.Minute,
		sent:           map[common.Hash]sentTx{},
		minedNonce:     map[common.Address]uint64{},
	}
}

// SetReceiptTimeout sets the maximum time to wait for a transaction receipt.
func (r *RetryClient) SetReceiptTimeout(d time.Duration) {
	r.receiptTimeout = d
}

// SetMinGasTipCap sets the lowest priority fee, in wei, the sweep bids with,
// whatever the node suggests. nil means no floor.
func (r *RetryClient) SetMinGasTipCap(tip *big.Int) {
	r.minGasTipCap = tip
}

// MinGasTipCap returns the floor set by SetMinGasTipCap.
func (r *RetryClient) MinGasTipCap() *big.Int {
	return r.minGasTipCap
}

func (r *RetryClient) newBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = 1 * time.Second
	b.Multiplier = 1.5
	b.MaxInterval = 30 * time.Second
	return b
}

func retry[T any](ctx context.Context, r *RetryClient, op string, level slog.Level, fn func() (T, error)) (T, error) {
	return backoff.Retry(ctx, func() (T, error) {
		result, err := fn()
		if err != nil {
			var permErr *backoff.PermanentError
			if !errors.As(err, &permErr) {
				r.logger.Log(ctx, level, "retrying RPC call", "op", op, "error", err)
			}
			return result, err
		}
		return result, nil
	}, backoff.WithBackOff(r.newBackoff()))
}

func (r *RetryClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return retry(ctx, r, "BalanceAt", slog.LevelWarn, func() (*big.Int, error) {
		return r.client.BalanceAt(ctx, account, blockNumber)
	})
}

func (r *RetryClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	return retry(ctx, r, "CodeAt", slog.LevelWarn, func() ([]byte, error) {
		return r.client.CodeAt(ctx, account, blockNumber)
	})
}

// PendingNonceAt returns the account's next nonce, never one below what a
// transaction mined through this client already used.
func (r *RetryClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	nonce, err := retry(ctx, r, "PendingNonceAt", slog.LevelWarn, func() (uint64, error) {
		return r.client.PendingNonceAt(ctx, account)
	})
	if err != nil {
		return 0, err
	}

	r.mu.Lock()
	next, ok := r.minedNonce[account]
	r.mu.Unlock()
	if ok && nonce < next {
		r.logger.Warn("node reported a stale nonce, using the next one after the last mined transaction",
			"address", account.Hex(), "reported", nonce, "using", next)
		return next, nil
	}
	return nonce, nil
}

func (r *RetryClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return retry(ctx, r, "SuggestGasPrice", slog.LevelWarn, func() (*big.Int, error) {
		return r.client.SuggestGasPrice(ctx)
	})
}

func (r *RetryClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return retry(ctx, r, "SuggestGasTipCap", slog.LevelWarn, func() (*big.Int, error) {
		return r.client.SuggestGasTipCap(ctx)
	})
}

func (r *RetryClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return retry(ctx, r, "EstimateGas", slog.LevelWarn, func() (uint64, error) {
		gas, err := r.client.EstimateGas(ctx, msg)
		if err != nil {
			// Estimation runs the call, so it fails the same way CallContract
			// does when the call reverts — and just as deterministically.
			// Retrying only stalls the sweep on this wallet until the context
			// is cancelled, when it should be recorded as a failure and the
			// next wallet tried.
			if isRevert(err) {
				return 0, backoff.Permanent(err)
			}
		}
		return gas, err
	})
}

func (r *RetryClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	_, err := retry(ctx, r, "SendTransaction", slog.LevelWarn, func() (struct{}, error) {
		err := r.client.SendTransaction(ctx, tx)
		switch {
		case err == nil:
			return struct{}{}, nil
		case isErr(err, "already known"):
			// An earlier attempt reached the node after all.
			return struct{}{}, nil
		case isErr(err, "nonce too low"):
			// Resending the same signed transaction can never fix its nonce.
			// The only way it is fine is that it is this very transaction
			// that used the nonce, on an earlier attempt.
			if receipt, rerr := r.client.TransactionReceipt(ctx, tx.Hash()); rerr == nil && receipt != nil {
				return struct{}{}, nil
			}
			return struct{}{}, backoff.Permanent(err)
		default:
			return struct{}{}, err
		}
	})
	if err != nil {
		return err
	}
	r.trackSent(tx)
	// Waiting for the receipt is quiet, so this is what tells a pending
	// transaction apart from a stuck sweep, and what to look it up by.
	to := "contract creation"
	if tx.To() != nil {
		to = tx.To().Hex()
	}
	r.logger.Info("transaction sent, waiting for it to be mined",
		"tx", tx.Hash().Hex(), "nonce", tx.Nonce(), "to", to,
		"gasTipCap", tx.GasTipCap().String(), "gasFeeCap", tx.GasFeeCap().String(), "gas", tx.Gas())
	return nil
}

// trackSent remembers the sender and nonce of tx, for TransactionReceipt to
// record once it is mined.
func (r *RetryClient) trackSent(tx *types.Transaction) {
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent[tx.Hash()] = sentTx{from: from, nonce: tx.Nonce()}
}

// trackMined records that a transaction sent through this client is mined, so
// its sender's nonce is used up.
func (r *RetryClient) trackMined(txHash common.Hash) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sent[txHash]
	if !ok {
		return
	}
	delete(r.sent, txHash)
	if s.nonce+1 > r.minedNonce[s.from] {
		r.minedNonce[s.from] = s.nonce + 1
	}
}

// isErr reports whether an RPC error carries the given node error message.
// Nodes and providers wrap these messages differently, so only the text is
// reliable.
func isErr(err error, msg string) bool {
	return strings.Contains(strings.ToLower(err.Error()), msg)
}

func (r *RetryClient) ChainID(ctx context.Context) (*big.Int, error) {
	return retry(ctx, r, "ChainID", slog.LevelWarn, func() (*big.Int, error) {
		return r.client.ChainID(ctx)
	})
}

func (r *RetryClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return retry(ctx, r, "CallContract", slog.LevelWarn, func() ([]byte, error) {
		result, err := r.client.CallContract(ctx, msg, blockNumber)
		if err != nil {
			// Contract reverts are deterministic — wrap as permanent so the
			// retry loop exits immediately without logging a spurious retry.
			if isRevert(err) {
				return nil, backoff.Permanent(err)
			}
		}
		return result, err
	})
}

// isRevert reports whether an RPC error is the node saying the call reverted,
// which no amount of retrying will change. Nodes answer with JSON-RPC error
// code 3 ("execution reverted"); some report it in the message instead.
func isRevert(err error) bool {
	var rpcErr rpc.Error
	if errors.As(err, &rpcErr) && rpcErr.ErrorCode() == 3 {
		return true
	}
	return strings.Contains(err.Error(), "execution reverted")
}

func (r *RetryClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	// Polling for a receipt until the tx is mined is normal — use Debug so it
	// doesn't appear as noise in standard output.  Use a longer timeout than
	// regular RPC calls since mining can take much longer than a typical retry
	// window (the default 15m was too short for mainnet congestion).
	receipt, err := backoff.Retry(ctx, func() (*types.Receipt, error) {
		result, err := r.client.TransactionReceipt(ctx, txHash)
		if err != nil {
			r.logger.Log(ctx, slog.LevelDebug, "retrying RPC call", "op", "TransactionReceipt", "error", err)
			return result, err
		}
		return result, nil
	}, backoff.WithBackOff(r.newBackoff()), backoff.WithMaxElapsedTime(r.receiptTimeout))
	if errors.Is(err, ethereum.NotFound) {
		// A bare "not found" reads like a lookup failure. It actually means the
		// transaction was accepted by the node but never mined in the whole
		// wait window — it is either still pending or was dropped from the
		// mempool. Name the transaction so it can be looked up.
		return nil, fmt.Errorf("transaction %s not mined within %s, still pending or dropped: %w", txHash.Hex(), r.receiptTimeout, err)
	}
	if err == nil {
		r.trackMined(txHash)
	}
	return receipt, err
}

// WaitMined polls for a transaction receipt until the transaction is confirmed.
func (r *RetryClient) WaitMined(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return r.TransactionReceipt(ctx, txHash)
}
