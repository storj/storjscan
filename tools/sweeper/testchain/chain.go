// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.

// Package testchain runs a real go-ethereum node in process, so the sweeper can
// be tested against an EVM instead of a mock. It is a trimmed copy of
// storjscan's private/testeth, kept here because tools/sweeper is its own
// module: the accounts are plain keys funded in genesis rather than a keystore,
// there is no token deployment baked in, and the chain runs with Prague active
// so EIP-7702 works.
package testchain

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/eth/catalyst"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
)

// blockInterval is how often the background producer seals a block. The
// simulated beacon only mines when told to, so something has to tell it: code
// under test sends a transaction and waits for a receipt, exactly as it would
// against a real node, and never calls Commit itself.
const blockInterval = 20 * time.Millisecond

// Chain is a single-node Ethereum network with instant blocks.
type Chain struct {
	stack   *node.Node
	backend *eth.Ethereum
	beacon  *catalyst.SimulatedBeacon
	client  *ethclient.Client

	commitMu sync.Mutex

	// Accounts are funded keys, generated fresh for every chain. The first one
	// is the miner's etherbase.
	Accounts []*ecdsa.PrivateKey
}

// Option adjusts the chain before it starts.
type Option func(alloc map[common.Address]types.Account)

// WithCode preloads runtime bytecode at a fixed address. Contracts that exist
// at the same address on every real chain — Multicall3, say — have to be put
// there by hand on a chain that starts from an empty genesis.
func WithCode(addr common.Address, code []byte) Option {
	return func(alloc map[common.Address]types.Account) {
		alloc[addr] = types.Account{Code: code, Balance: new(big.Int)}
	}
}

// Start brings up a chain with numAccounts funded keys and returns it. The
// chain is closed when the test finishes.
func Start(t *testing.T, numAccounts int, opts ...Option) *Chain {
	t.Helper()

	if numAccounts < 1 {
		numAccounts = 1
	}
	accounts := make([]*ecdsa.PrivateKey, numAccounts)
	alloc := make(map[common.Address]types.Account, numAccounts)
	for i := range accounts {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatalf("generating account: %v", err)
		}
		accounts[i] = key
		alloc[crypto.PubkeyToAddress(key.PublicKey)] = types.Account{
			Balance: new(big.Int).Mul(big.NewInt(1000), big.NewInt(params.Ether)),
		}
	}
	for _, opt := range opts {
		opt(alloc)
	}
	etherbase := crypto.PubkeyToAddress(accounts[0].PublicKey)

	nodeConfig := node.DefaultConfig
	nodeConfig.Name = "sweeper-testchain"
	nodeConfig.DataDir = "" // in memory
	nodeConfig.P2P.MaxPeers = 0
	nodeConfig.P2P.ListenAddr = ""
	nodeConfig.P2P.NoDial = true
	nodeConfig.P2P.NoDiscovery = true
	nodeConfig.P2P.DiscoveryV5 = false

	stack, err := node.New(&nodeConfig)
	if err != nil {
		t.Fatalf("creating node: %v", err)
	}

	ethConfig := ethconfig.Defaults
	ethConfig.NetworkId = 1337
	ethConfig.SyncMode = ethconfig.FullSync
	ethConfig.Miner.GasPrice = big.NewInt(params.GWei)
	ethConfig.Miner.Etherbase = etherbase
	ethConfig.Genesis = genesis(ethConfig.NetworkId, alloc)

	backend, err := eth.New(stack, &ethConfig)
	if err != nil {
		_ = stack.Close()
		t.Fatalf("creating eth service: %v", err)
	}

	// period 0 means the beacon never mines by itself; produceBlocks drives it.
	beacon, err := catalyst.NewSimulatedBeacon(0, etherbase, backend)
	if err != nil {
		_ = stack.Close()
		t.Fatalf("creating simulated beacon: %v", err)
	}

	if err := stack.Start(); err != nil {
		_ = stack.Close()
		t.Fatalf("starting node: %v", err)
	}
	backend.TxPool().SetGasTip(big.NewInt(params.GWei))
	if err := beacon.Start(); err != nil {
		_ = stack.Close()
		t.Fatalf("starting beacon: %v", err)
	}

	chain := &Chain{
		stack:    stack,
		backend:  backend,
		beacon:   beacon,
		client:   ethclient.NewClient(stack.Attach()),
		Accounts: accounts,
	}
	stopProducing := chain.produceBlocks()
	t.Cleanup(func() {
		stopProducing()
		chain.client.Close()
		if err := beacon.Stop(); err != nil {
			t.Logf("stopping beacon: %v", err)
		}
		if err := stack.Close(); err != nil {
			t.Logf("closing node: %v", err)
		}
	})
	return chain
}

// produceBlocks starts sealing blocks in the background and returns a function
// that stops it and waits for it to finish.
func (c *Chain) produceBlocks() (stop func()) {
	done := make(chan struct{})
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		ticker := time.NewTicker(blockInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				c.Commit()
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}

// genesis builds a dev-chain genesis with every fork active from block zero.
// AllDevChainProtocolChanges sets PragueTime to 0, which is what makes EIP-7702
// set-code transactions valid on this chain.
func genesis(chainID uint64, alloc map[common.Address]types.Account) *core.Genesis {
	config := *params.AllDevChainProtocolChanges
	config.ChainID = new(big.Int).SetUint64(chainID)
	return &core.Genesis{
		Config:     &config,
		GasLimit:   11500000,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(0),
		Alloc:      alloc,
	}
}

// Client returns an ethclient talking to the in-process node.
func (c *Chain) Client() *ethclient.Client { return c.client }

// ChainID returns the chain's ID.
func (c *Chain) ChainID() *big.Int { return c.backend.BlockChain().Config().ChainID }

// Address returns the address of the account at index i.
func (c *Chain) Address(i int) common.Address {
	return crypto.PubkeyToAddress(c.Accounts[i].PublicKey)
}

// Commit seals a block with whatever is pending. It is safe to call while the
// background producer is running.
func (c *Chain) Commit() {
	c.commitMu.Lock()
	defer c.commitMu.Unlock()
	c.beacon.Commit()
}

// WaitMined commits a block and waits for the transaction's receipt. It fails
// the test if the transaction reverted.
func (c *Chain) WaitMined(t *testing.T, ctx context.Context, tx *types.Transaction) *types.Receipt {
	t.Helper()

	receipt, err := c.waitReceipt(ctx, tx.Hash())
	if err != nil {
		t.Fatalf("waiting for transaction %s: %v", tx.Hash().Hex(), err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("transaction %s reverted", tx.Hash().Hex())
	}
	return receipt
}

func (c *Chain) waitReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	c.Commit()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		receipt, err := c.client.TransactionReceipt(ctx, hash)
		if err == nil {
			return receipt, nil
		}
		// Not mined yet, or mined but not indexed yet: both resolve on their
		// own, anything else is a real failure.
		if !errors.Is(err, ethereum.NotFound) && err.Error() != "transaction indexing is in progress" {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Send signs a transaction with the given key, sends it, and waits for it to be
// mined successfully.
func (c *Chain) Send(t *testing.T, ctx context.Context, key *ecdsa.PrivateKey, data types.TxData) *types.Receipt {
	t.Helper()

	signed, err := types.SignTx(types.NewTx(data), types.LatestSignerForChainID(c.ChainID()), key)
	if err != nil {
		t.Fatalf("signing transaction: %v", err)
	}
	if err := c.client.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("sending transaction: %v", err)
	}
	return c.WaitMined(t, ctx, signed)
}

// Deploy deploys a contract and returns its address. bytecode must already have
// the constructor arguments appended.
func (c *Chain) Deploy(t *testing.T, ctx context.Context, key *ecdsa.PrivateKey, bytecode []byte) common.Address {
	t.Helper()

	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := c.client.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	gasFeeCap, gasTipCap := c.gasFees(t, ctx)

	receipt := c.Send(t, ctx, key, &types.DynamicFeeTx{
		ChainID:   c.ChainID(),
		Nonce:     nonce,
		Gas:       3_000_000,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
		Data:      bytecode,
	})
	if receipt.ContractAddress == (common.Address{}) {
		t.Fatal("deployment produced no contract address")
	}
	return receipt.ContractAddress
}

// Call signs and sends a contract call, waiting for it to be mined.
func (c *Chain) Call(t *testing.T, ctx context.Context, key *ecdsa.PrivateKey, to common.Address, data []byte, value *big.Int) *types.Receipt {
	t.Helper()

	from := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := c.client.PendingNonceAt(ctx, from)
	if err != nil {
		t.Fatalf("nonce: %v", err)
	}
	gasFeeCap, gasTipCap := c.gasFees(t, ctx)

	return c.Send(t, ctx, key, &types.DynamicFeeTx{
		ChainID:   c.ChainID(),
		Nonce:     nonce,
		To:        &to,
		Value:     value,
		Gas:       1_000_000,
		GasFeeCap: gasFeeCap,
		GasTipCap: gasTipCap,
		Data:      data,
	})
}

// Balance returns an account's ETH balance.
func (c *Chain) Balance(t *testing.T, ctx context.Context, account common.Address) *big.Int {
	t.Helper()

	balance, err := c.client.BalanceAt(ctx, account, nil)
	if err != nil {
		t.Fatalf("balance of %s: %v", account.Hex(), err)
	}
	return balance
}

func (c *Chain) gasFees(t *testing.T, ctx context.Context) (gasFeeCap, gasTipCap *big.Int) {
	t.Helper()

	tip, err := c.client.SuggestGasTipCap(ctx)
	if err != nil {
		t.Fatalf("gas tip: %v", err)
	}
	head, err := c.client.HeaderByNumber(ctx, nil)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	baseFee := new(big.Int)
	if head.BaseFee != nil {
		baseFee.Set(head.BaseFee)
	}
	return new(big.Int).Add(tip, new(big.Int).Mul(baseFee, big.NewInt(2))), tip
}
