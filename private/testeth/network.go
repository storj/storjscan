// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"context"
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/eth/catalyst"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/params"
	"github.com/zeebo/errs"
)

// Network - test Ethererum network.
type Network struct {
	ethereum  *eth.Ethereum
	stack     *node.Node
	keystore  *keystore.KeyStore
	developer accounts.Account
	token     common.Address
	beacon    *catalyst.SimulatedBeacon
}

func minerTestGenesisBlock(chainID uint64, gasLimit uint64, faucet common.Address) *core.Genesis {
	// Use AllDevChainProtocolChanges with SimulatedBeacon for automatic block production
	config := *params.AllDevChainProtocolChanges
	config.ChainID = big.NewInt(int64(chainID))
	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &core.Genesis{
		Config:     &config,
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(0),
		Alloc: map[common.Address]types.Account{
			common.BytesToAddress([]byte{1}): {Balance: big.NewInt(1)}, // ECRecover
			common.BytesToAddress([]byte{2}): {Balance: big.NewInt(1)}, // SHA256
			common.BytesToAddress([]byte{3}): {Balance: big.NewInt(1)}, // RIPEMD
			common.BytesToAddress([]byte{4}): {Balance: big.NewInt(1)}, // Identity
			common.BytesToAddress([]byte{5}): {Balance: big.NewInt(1)}, // ModExp
			common.BytesToAddress([]byte{6}): {Balance: big.NewInt(1)}, // ECAdd
			common.BytesToAddress([]byte{7}): {Balance: big.NewInt(1)}, // ECScalarMul
			common.BytesToAddress([]byte{8}): {Balance: big.NewInt(1)}, // ECPairing
			common.BytesToAddress([]byte{9}): {Balance: big.NewInt(1)}, // BLAKE2b
			faucet:                           {Balance: new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(9))},
		},
	}
}

// NewNetwork creates new test Ethereum network with PoS and inmemory DBs.
func NewNetwork(nodeConfig node.Config, ethConfig ethconfig.Config, numAccounts int) (*Network, error) {
	stack, err := node.New(&nodeConfig)
	if err != nil {
		return nil, err
	}

	// setup keystore backend
	ks := keystore.NewKeyStore(stack.KeyStoreDir(), keystore.LightScryptN, keystore.LightScryptP)
	stack.AccountManager().AddBackend(ks)

	var preFund []accounts.Account

	base, err := ks.NewAccount("")
	if err != nil {
		return nil, err
	}
	for i := 1; i < numAccounts; i++ {
		acc, err := ks.NewAccount("")
		if err != nil {
			return nil, err
		}
		preFund = append(preFund, acc)
	}
	for _, acc := range ks.Accounts() {
		if err = ks.Unlock(acc, ""); err != nil {
			return nil, err
		}
	}

	ethConfig.Genesis = minerTestGenesisBlock(ethConfig.NetworkId, 11500000, base.Address)
	for _, acc := range preFund {
		ethConfig.Genesis.Alloc[acc.Address] = types.Account{
			Balance: new(big.Int).Mul(big.NewInt(100), big.NewInt(params.Ether)),
		}
	}
	ethConfig.Miner.Etherbase = base.Address
	backend, ethereum := utils.RegisterEthService(stack, &ethConfig)

	utils.RegisterFilterAPI(stack, backend, &ethConfig)

	// Create a simulated beacon with period=0 to auto-produce blocks on every transactiony
	beacon, err := catalyst.NewSimulatedBeacon(0, base.Address, ethereum)
	if err != nil {
		return nil, err
	}

	return &Network{
		ethereum:  ethereum,
		stack:     stack,
		keystore:  ks,
		developer: base,
		beacon:    beacon,
	}, nil
}

// Ethereum returns Ethereum full service.
func (network *Network) Ethereum() *eth.Ethereum {
	return network.ethereum
}

// Accounts returns available accounts, with first one being coinbase account.
func (network *Network) Accounts() []accounts.Account {
	return network.keystore.Accounts()
}

// Dial creates new Ethereum client connected to in-process API handler.
func (network *Network) Dial() *ethclient.Client {
	rpcClient := network.stack.Attach()
	return ethclient.NewClient(rpcClient)
}

// ChainID returns chaind id of the network.
func (network *Network) ChainID() *big.Int {
	return network.ethereum.BlockChain().Config().ChainID
}

// HTTPEndpoint returns HTTP RPC API endpoint address.
func (network *Network) HTTPEndpoint() string {
	return network.stack.HTTPEndpoint()
}

// TokenAddress returns address of deployed test token.
func (network *Network) TokenAddress() common.Address {
	return network.token
}

// TransactOptions creates new key store transaction opts for given account with provided nonce and context.
func (network *Network) TransactOptions(ctx context.Context, account accounts.Account, nonce int64) *bind.TransactOpts {
	opts, _ := bind.NewKeyStoreTransactorWithChainID(network.keystore, account, network.ChainID())
	opts.Context = ctx
	opts.Nonce = big.NewInt(nonce)
	return opts
}

// WaitForTx block execution until transaction receipt is received or context is cancelled.
// It manually commits a block to ensure the transaction is included.
func (network *Network) WaitForTx(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	client := network.Dial()
	defer client.Close()

	// Manually commit a block to include pending transactions
	network.Commit()

	c := make(chan core.ChainHeadEvent)
	defer close(c)

	sub := network.ethereum.BlockChain().SubscribeChainHeadEvent(c)
	defer sub.Unsubscribe()

	rcpt, err := client.TransactionReceipt(ctx, hash)
	if err == nil {
		if rcpt.Status == 1 {
			return rcpt, nil
		}
		return rcpt, errs.New("transaction failed")
	}
	// keep waiting if not yet found or still in progress.
	if !errors.Is(err, ethereum.NotFound) && !isIndexingInProgress(err) {
		return rcpt, err
	}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c:
		}

		rcpt, err := client.TransactionReceipt(ctx, hash)
		if err == nil {
			if rcpt.Status == 1 {
				return rcpt, nil
			}
			return rcpt, errs.New("transaction failed")
		}
		// Treat indexing errors as temporary - keep waiting
		if !errors.Is(err, ethereum.NotFound) && !isIndexingInProgress(err) {
			return rcpt, err
		}
	}
}

// isIndexingInProgress checks if the error is the "transaction indexing is in progress" error
func isIndexingInProgress(err error) bool {
	return err != nil && (err.Error() == "transaction indexing is in progress")
}

// Start starts all registered lifecycles, RPC services, p2p networking and the simulated beacon.
// The simulated beacon automatically produces blocks on every transaction (period=0).
func (network *Network) Start() error {
	if err := network.stack.Start(); err != nil {
		return err
	}

	network.ethereum.TxPool().SetGasTip(big.NewInt(params.GWei))

	return network.beacon.Start()
}

// Commit produces a new block with pending transactions.
// This is useful for ensuring transactions are included in a block immediately.
func (network *Network) Commit() common.Hash {
	return network.beacon.Commit()
}

// Close stops the beacon and node, releasing all resources.
func (network *Network) Close() error {
	if network.beacon != nil {
		if err := network.beacon.Stop(); err != nil {
			return err
		}
	}
	return network.stack.Close()
}
