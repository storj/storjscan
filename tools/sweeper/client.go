// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"errors"
	"log/slog"
	"math/big"
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
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
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
}

// NewRetryClient creates a new RetryClient wrapping the given BlockchainClient.
func NewRetryClient(client BlockchainClient, logger *slog.Logger) *RetryClient {
	return &RetryClient{
		client:         client,
		logger:         logger,
		receiptTimeout: 30 * time.Minute,
	}
}

// SetReceiptTimeout sets the maximum time to wait for a transaction receipt.
func (r *RetryClient) SetReceiptTimeout(d time.Duration) {
	r.receiptTimeout = d
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

func (r *RetryClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return retry(ctx, r, "PendingNonceAt", slog.LevelWarn, func() (uint64, error) {
		return r.client.PendingNonceAt(ctx, account)
	})
}

func (r *RetryClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return retry(ctx, r, "SuggestGasPrice", slog.LevelWarn, func() (*big.Int, error) {
		return r.client.SuggestGasPrice(ctx)
	})
}

func (r *RetryClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	return retry(ctx, r, "EstimateGas", slog.LevelWarn, func() (uint64, error) {
		return r.client.EstimateGas(ctx, msg)
	})
}

func (r *RetryClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	_, err := retry(ctx, r, "SendTransaction", slog.LevelWarn, func() (struct{}, error) {
		return struct{}{}, r.client.SendTransaction(ctx, tx)
	})
	return err
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
			// Contract reverts (code 3) are deterministic — wrap as permanent so
			// the retry loop exits immediately without logging a spurious retry.
			var rpcErr rpc.Error
			if errors.As(err, &rpcErr) && rpcErr.ErrorCode() == 3 {
				return nil, backoff.Permanent(err)
			}
		}
		return result, err
	})
}

func (r *RetryClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	// Polling for a receipt until the tx is mined is normal — use Debug so it
	// doesn't appear as noise in standard output.  Use a longer timeout than
	// regular RPC calls since mining can take much longer than a typical retry
	// window (the default 15m was too short for mainnet congestion).
	return backoff.Retry(ctx, func() (*types.Receipt, error) {
		result, err := r.client.TransactionReceipt(ctx, txHash)
		if err != nil {
			r.logger.Log(ctx, slog.LevelDebug, "retrying RPC call", "op", "TransactionReceipt", "error", err)
			return result, err
		}
		return result, nil
	}, backoff.WithBackOff(r.newBackoff()), backoff.WithMaxElapsedTime(r.receiptTimeout))
}

// WaitMined polls for a transaction receipt until the transaction is confirmed.
func (r *RetryClient) WaitMined(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return r.TransactionReceipt(ctx, txHash)
}
