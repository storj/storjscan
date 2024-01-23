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
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/eth/downloader"
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
}

func minerTestGenesisBlock(period uint64, gasLimit uint64, faucet common.Address) *core.Genesis {
	config := *params.AllCliqueProtocolChanges
	config.Clique = &params.CliqueConfig{
		Period: period,
		Epoch:  config.Clique.Epoch,
	}

	// Assemble and return the genesis with the precompiles and faucet pre-funded
	return &core.Genesis{
		Config:     &config,
		ExtraData:  append(append(make([]byte, 32), faucet[:]...), make([]byte, crypto.SignatureLength)...),
		GasLimit:   gasLimit,
		BaseFee:    big.NewInt(params.InitialBaseFee),
		Difficulty: big.NewInt(1),
		Alloc: map[common.Address]core.GenesisAccount{
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
func NewNetwork() (*Network, error) {
	config := node.DefaultConfig
	config.Name = "testeth"
	config.DataDir = ""
	config.HTTPHost = "127.0.0.1"
	config.HTTPPort = 0
	config.AuthPort = 0
	config.HTTPModules = append(config.HTTPModules, "eth")
	config.P2P.MaxPeers = 0
	config.P2P.ListenAddr = ""
	config.P2P.NoDial = true
	config.P2P.NoDiscovery = true
	config.P2P.DiscoveryV5 = false

	stack, err := node.New(&config)
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
	for i := 0; i < 9; i++ {
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

	genesis := minerTestGenesisBlock(0, 11500000, base.Address)
	for _, acc := range preFund {
		genesis.Alloc[acc.Address] = core.GenesisAccount{
			Balance: new(big.Int).Mul(big.NewInt(100), big.NewInt(params.Ether)),
		}
	}

	// eth config
	ethConfig := ethconfig.Defaults
	ethConfig.NetworkId = 1337
	ethConfig.SyncMode = downloader.FullSync
	ethConfig.Miner.GasPrice = big.NewInt(1)
	ethConfig.Miner.Etherbase = base.Address
	ethConfig.Genesis = genesis
	ethConfig.FilterLogCacheSize = 100
	backend, ethereum := utils.RegisterEthService(stack, &ethConfig)

	utils.RegisterFilterAPI(stack, backend, &ethConfig)
	return &Network{
		ethereum:  ethereum,
		stack:     stack,
		keystore:  ks,
		developer: base,
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

// TransactOptions creates new key store transaction opts for given account with provided nonce and context.
func (network *Network) TransactOptions(ctx context.Context, account accounts.Account, nonce int64) *bind.TransactOpts {
	opts, _ := bind.NewKeyStoreTransactorWithChainID(network.keystore, account, network.ChainID())
	opts.Context = ctx
	opts.Nonce = big.NewInt(nonce)
	return opts
}

// WaitForTx block execution until transaction receipt is received or context is cancelled.
func (network *Network) WaitForTx(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	client := network.Dial()
	defer client.Close()

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
	if !errors.Is(err, ethereum.NotFound) {
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
		if !errors.Is(err, ethereum.NotFound) {
			return rcpt, err
		}
	}
}

// Start starts all registered lifecycles, RPC services, p2p networking and starts mining.
func (network *Network) Start() error {
	if err := network.stack.Start(); err != nil {
		return err
	}

	network.ethereum.TxPool().SetGasTip(big.NewInt(params.GWei))
	return network.ethereum.StartMining()
}

// Close stops the node and releases resources acquired in node constructor.
func (network *Network) Close() error {
	return network.stack.Close()
}
