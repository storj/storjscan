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

type GeneratedAddress struct {
	Address common.Address
	Info    string
}

// Generate creates new HD wallet addresses.
func Generate(ctx context.Context, keysname string, min, max int, mnemonic string) ([]GeneratedAddress, error) {
	addrs := make([]GeneratedAddress, 0, max-min)

	if mnemonic == "" {
		return nil, errs.New("mnemonic is required")
	}

	seed, err := bip39.NewSeedWithErrorChecking(mnemonic, "")
	if err != nil {
		return nil, errs.Wrap(err)
	}
	if len(seed) == 0 {
		return nil, errs.New("unexpectedly empty seed")
	}

	masterKey, err := hdkeychain.NewMaster(seed, &chaincfg.MainNetParams)
	if err != nil {
		return nil, errs.Wrap(err)
	}

	next := accounts.DefaultIterator(accounts.DefaultBaseDerivationPath)

	for i := 0; i <= max; i++ {
		path := next()
		if i < min {
			continue
		}
		account, err := derive(masterKey, path)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, GeneratedAddress{
			Address: account.Address,
			Info:    keysname + " " + path.String(),
		})
	}
	return addrs, nil
}
