// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

// EIP-7702 protocol plumbing: reading and signing delegations, talking to the
// Sweeper7702 contract, and building the transaction that carries both. The
// sweep that uses all of this lives in delegate.go.

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"
)

// Gas limits for a delegated sweep. eth_estimateGas is asked first; these are
// the floor applied to its answer, because a node that ignores the
// authorization list estimates the call against an account that still has no
// code and answers with little more than the 21000 intrinsic cost, which would
// strand the transaction out of gas. Bidding a limit that is too high is free —
// only the gas actually burned is paid for — so the floor is deliberately
// generous.
const (
	delegatedSweepBaseGas  = 100_000
	delegatedSweepTokenGas = 50_000
	// delegationAuthGas is EIP-7702's PER_EMPTY_ACCOUNT_COST, added on top of
	// an estimate for every authorization the node may not have accounted for.
	delegationAuthGas = 25_000
	// delegatedSweepGasBufferPercent pads the estimate the same way
	// EstimateSweepGas does.
	delegatedSweepGasBufferPercent = 130
)

var (
	// sweepMethodID is the 4-byte selector of sweep(address[]) on Sweeper7702.
	sweepMethodID = methodID("sweep(address[])")
	// destMethodID is the selector of the contract's immutable DEST() getter.
	destMethodID = methodID("DEST()")
)

func methodID(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

// SweepCallData builds the calldata for Sweeper7702.sweep(address[]).
func SweepCallData(tokens []common.Address) []byte {
	data := make([]byte, 0, 4+64+len(tokens)*32)
	data = append(data, sweepMethodID...)
	data = appendUint256(data, 0x20) // offset to the array content
	data = appendUint256(data, uint64(len(tokens)))
	for _, token := range tokens {
		var word [32]byte
		copy(word[12:], token.Bytes())
		data = append(data, word[:]...)
	}
	return data
}

// ContractDestination reads the immutable destination a deployed Sweeper7702
// sweeps into. The destination is baked into the contract at deployment, so
// this is the only way to check that the contract about to run inside the
// deposit wallets sends their funds where the operator expects.
func ContractDestination(ctx context.Context, client BlockchainClient, contract common.Address) (common.Address, error) {
	code, err := client.CodeAt(ctx, contract, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("code check: %w", err)
	}
	if len(code) == 0 {
		return common.Address{}, fmt.Errorf("no contract deployed at %s", contract.Hex())
	}

	result, err := client.CallContract(ctx, ethereum.CallMsg{
		To:   &contract,
		Data: destMethodID,
	}, nil)
	if err != nil {
		return common.Address{}, fmt.Errorf("DEST call: %w", err)
	}
	if len(result) < 32 {
		return common.Address{}, fmt.Errorf("DEST: unexpected response length %d", len(result))
	}
	return common.BytesToAddress(result[len(result)-32:]), nil
}

// DelegationTarget reports which contract a wallet currently runs as its code
// under EIP-7702. delegated is false when the account has no code at all, the
// normal state of a deposit wallet that has never been swept this way. An
// account holding ordinary contract code is an error: calling sweep() on it
// would execute something other than the sweeper contract.
func DelegationTarget(ctx context.Context, client BlockchainClient, wallet common.Address) (target common.Address, delegated bool, err error) {
	code, err := client.CodeAt(ctx, wallet, nil)
	if err != nil {
		return common.Address{}, false, fmt.Errorf("code check: %w", err)
	}
	if len(code) == 0 {
		return common.Address{}, false, nil
	}
	target, ok := types.ParseDelegation(code)
	if !ok {
		return common.Address{}, false, fmt.Errorf("account holds %d bytes of contract code, not an EIP-7702 delegation", len(code))
	}
	return target, true, nil
}

// SignDelegation signs an EIP-7702 authorization that lets the contract's code
// run in the wallet's own account. It is signed by the wallet but carried by
// somebody else's transaction, so it commits to the wallet's own next nonce.
func SignDelegation(ctx context.Context, client BlockchainClient, kp KeyPair, chainID *big.Int, contract common.Address) (types.SetCodeAuthorization, error) {
	nonce, err := client.PendingNonceAt(ctx, kp.Address)
	if err != nil {
		return types.SetCodeAuthorization{}, fmt.Errorf("nonce: %w", err)
	}
	chain, overflow := uint256.FromBig(chainID)
	if overflow {
		return types.SetCodeAuthorization{}, fmt.Errorf("chain ID %s out of range", chainID)
	}
	auth, err := types.SignSetCode(kp.PrivateKey, types.SetCodeAuthorization{
		ChainID: *chain,
		Address: contract,
		Nonce:   nonce,
	})
	if err != nil {
		return types.SetCodeAuthorization{}, fmt.Errorf("sign authorization: %w", err)
	}
	return auth, nil
}

// EstimateDelegatedSweepGas returns the gas limit to bid for a sweep call sent
// to wallet, carrying auths. See the gas constants for why the estimate is only
// used as a lower bound on a floor derived from the number of tokens.
func EstimateDelegatedSweepGas(ctx context.Context, client BlockchainClient, sponsor common.Address, wallet common.Address, data []byte, auths []types.SetCodeAuthorization, tokens int) (uint64, error) {
	estimate, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:              sponsor,
		To:                &wallet,
		Data:              data,
		AuthorizationList: auths,
	})
	if err != nil {
		return 0, fmt.Errorf("estimate gas: %w", err)
	}

	limit := estimate * delegatedSweepGasBufferPercent / 100
	limit += uint64(len(auths)) * delegationAuthGas

	floor := uint64(delegatedSweepBaseGas + tokens*delegatedSweepTokenGas)
	return max(limit, floor), nil
}

// NewDelegatedSweepTx builds the sponsor's transaction that runs the sweep in
// the wallet's account. When auths is non-empty it is an EIP-7702 set-code
// transaction, which installs the delegation and makes the call in one go;
// once a wallet is already delegated a plain dynamic-fee call is enough.
func NewDelegatedSweepTx(chainID *big.Int, nonce uint64, wallet common.Address, data []byte, gasLimit uint64, gasFeeCap, gasTipCap *big.Int, auths []types.SetCodeAuthorization) (*types.Transaction, error) {
	if len(auths) == 0 {
		return types.NewTx(&types.DynamicFeeTx{
			ChainID:   chainID,
			Nonce:     nonce,
			To:        &wallet,
			Gas:       gasLimit,
			GasFeeCap: gasFeeCap,
			GasTipCap: gasTipCap,
			Data:      data,
		}), nil
	}

	chain, overflow := uint256.FromBig(chainID)
	if overflow {
		return nil, fmt.Errorf("chain ID %s out of range", chainID)
	}
	feeCap, overflow := uint256.FromBig(gasFeeCap)
	if overflow {
		return nil, fmt.Errorf("gas fee cap %s out of range", gasFeeCap)
	}
	tipCap, overflow := uint256.FromBig(gasTipCap)
	if overflow {
		return nil, fmt.Errorf("gas tip cap %s out of range", gasTipCap)
	}
	return types.NewTx(&types.SetCodeTx{
		ChainID:   chain,
		Nonce:     nonce,
		To:        wallet,
		Value:     uint256.NewInt(0),
		Gas:       gasLimit,
		GasFeeCap: feeCap,
		GasTipCap: tipCap,
		Data:      data,
		AuthList:  auths,
	}), nil
}
