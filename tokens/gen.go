// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package tokens

//go:generate mkdir -p "erc20"
//go:generate abigen --abi=../contracts/build/ERC20.abi --type=ERC20 --pkg=erc20 --out=erc20/erc20.go
