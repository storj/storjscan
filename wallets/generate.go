// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"
	"crypto/ecdsa"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil/hdkeychain"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
	"github.com/zeebo/errs"
)

// GenerateConfig for wallet address generation.
type GenerateConfig struct {
	Address      string `help:"public address to listen on" default:"http://127.0.0.1:12000"`
	APIKey       string `help:"Secrets to connect to service endpoints."`
	APISecret    string `help:"Secrets to connect to service endpoints."`
	MnemonicFile string `help:"File which contains the mnemonic to be used for HD generation." default:".mnemonic"`
	Min          int    `help:"Index of the first derived address." default:"0"`
	Max          int    `help:"Index of the last derived address." default:"1000"`
	KeysName     string `help:"Name of the hd chain/mnemonic which was used/" default:"default"`
}

// Generate creates and registers new HD wallet addresses.
func Generate(ctx context.Context, config GenerateConfig, mnemonic string) error {
	client := NewClient(config.Address, config.APIKey, config.APISecret)
	return generateWithPersistFunc(ctx, config, mnemonic, client.AddWallets)
}

func derive(masterKey *hdkeychain.ExtendedKey, path accounts.DerivationPath) (accounts.Account, error) {
	var err error
	key := masterKey
	for _, n := range path {
		key, err = key.Derive(n)
		if err != nil {
			return accounts.Account{}, errs.Wrap(err)
		}
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		return accounts.Account{}, errs.Wrap(err)
	}
	privateKeyECDSA := privateKey.ToECDSA()
	publicKey := privateKeyECDSA.Public()
	publicKeyECDSA, ok := publicKey.(*ecdsa.PublicKey)
	if !ok {
		return accounts.Account{}, errs.New("failed to get public key")
	}

	address := crypto.PubkeyToAddress(*publicKeyECDSA)

	return accounts.Account{
		Address: address,
		URL: accounts.URL{
			Scheme: "",
			Path:   path.String(),
		},
	}, nil
}

func generateWithPersistFunc(ctx context.Context, config GenerateConfig, mnemonic string, persist func(context.Context, map[common.Address]string) error) error {

	addr := make(map[common.Address]string)

	if mnemonic == "" {
		return errs.New("mnemonic is required")
	}

	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return errs.Wrap(err)
	}
	if len(seed) == 0 {
		return errs.New("unexpectedly empty seed")
	}

	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return errs.Wrap(err)
	}

	next := accounts.DefaultIterator(accounts.DefaultBaseDerivationPath)

	for i := 0; i <= config.Max; i++ {
		path := next()
		if i < config.Min {
			continue
		}
		account, err := derive(masterKey, path)
		if err != nil {
			return err
		}
		addr[account.Address] = config.KeysName + " " + path.String()
	}
	return persist(ctx, addr)
}
