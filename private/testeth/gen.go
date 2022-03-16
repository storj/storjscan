// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

//go:generate mkdir -p "testtoken"
//go:generate abigen --bin=../../contracts/build/TestToken.bin --abi=../../contracts/build/TestToken.abi --type=TestToken --pkg=testtoken --out=testtoken/token.go
