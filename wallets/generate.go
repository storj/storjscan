// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package wallets

import (
	"context"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	mm "github.com/miguelmota/go-ethereum-hdwallet"
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

func generateWithPersistFunc(ctx context.Context, config GenerateConfig, mnemonic string, persist func(context.Context, map[common.Address]string) error) error {

	addr := make(map[common.Address]string)

	seed, err := mm.NewSeedFromMnemonic(mnemonic)
	if err != nil {
		return errs.Wrap(err)
	}

	w, err := mm.NewFromSeed(seed)
	if err != nil {
		return errs.Wrap(err)
	}

	next := accounts.DefaultIterator(mm.DefaultBaseDerivationPath)

	for i := 0; i <= config.Max; i++ {
		path := next()
		if i < config.Min {
			continue
		}
		account, err := w.Derive(path, false)
		if err != nil {
			return errs.Wrap(err)
		}
		addr[account.Address] = config.KeysName + " " + path.String()
	}
	return persist(ctx, addr)
}
