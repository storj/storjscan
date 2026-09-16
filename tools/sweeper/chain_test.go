// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
package sweeper

import (
	"context"
	"encoding/hex"
	"log/slog"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"storj.io/sweeper/contract"
	"storj.io/sweeper/testchain"
)

// chainFixture is a running chain with the sweeper contract, a token, and a set
// of funded deposit wallets: everything a sweep needs to actually move money.
type chainFixture struct {
	chain *testchain.Chain
	ctx   context.Context

	client      *RetryClient
	contract    common.Address
	token       common.Address
	destination common.Address
	sponsor     *KeyPair
	wallets     []KeyPair

	walletETH   *big.Int // ETH funded into each deposit wallet
	walletToken *big.Int // tokens minted into each deposit wallet
}

// newChainFixture starts a chain and prepares walletCount deposit wallets, each
// holding ETH and tokens. The sponsor is a separate account from the
// destination so that the destination's balance only changes by what is swept,
// which makes the assertions exact.
func newChainFixture(t *testing.T, walletCount int) *chainFixture {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	// 0: deployer and miner, 1: gas sponsor, 2: destination. Multicall3 is
	// preloaded at its canonical address, where the balance queries look for it.
	chain := testchain.Start(t, 3, testchain.WithCode(multicall3Address, contractBytecode(t, contract.Multicall3Runtime)))
	deployer := chain.Accounts[0]
	sponsorKey := chain.Accounts[1]
	destination := chain.Address(2)

	token := chain.Deploy(t, ctx, deployer, contractBytecode(t, contract.TestTokenBin, encodeUint256(18)))
	sweeperContract := chain.Deploy(t, ctx, deployer, contractBytecode(t, contract.Sweeper7702Bin, encodeAddress(destination)))

	f := &chainFixture{
		chain:       chain,
		ctx:         ctx,
		client:      NewRetryClient(chain.Client(), testLogger()),
		contract:    sweeperContract,
		token:       token,
		destination: destination,
		sponsor:     &KeyPair{PrivateKey: sponsorKey, Address: chain.Address(1)},
		walletETH:   big.NewInt(1e17), // 0.1 ETH
		walletToken: big.NewInt(5e18),
	}

	for i := range walletCount {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatalf("generating deposit key: %v", err)
		}
		wallet := KeyPair{PrivateKey: key, Address: crypto.PubkeyToAddress(key.PublicKey), LineNum: i + 1}
		f.wallets = append(f.wallets, wallet)
		f.fund(t, wallet.Address)
	}
	return f
}

// fund gives a wallet ETH and tokens, the state a deposit wallet is in when it
// is due to be swept.
func (f *chainFixture) fund(t *testing.T, wallet common.Address) {
	t.Helper()

	deployer := f.chain.Accounts[0]
	f.chain.Call(t, f.ctx, deployer, wallet, nil, f.walletETH)
	f.chain.Call(t, f.ctx, deployer, f.token, mintCallData(wallet, f.walletToken), nil)
}

// sweeper builds a delegated sweeper wired to the fixture's chain.
func (f *chainFixture) sweeper(t *testing.T, dryRun bool) *Sweeper {
	t.Helper()

	return NewDelegatedSweeper(f.client, PaymentTypeL17702, f.destination, []common.Address{f.token},
		&Delegation{Contract: f.contract, Sponsor: f.sponsor}, 0, 0, dryRun, testLogger())
}

func (f *chainFixture) tokenBalance(t *testing.T, account common.Address) *big.Int {
	t.Helper()

	balance, err := ERC20BalanceOf(f.ctx, f.client, f.token, account)
	if err != nil {
		t.Fatalf("token balance of %s: %v", account.Hex(), err)
	}
	return balance
}

func (f *chainFixture) ethBalance(t *testing.T, account common.Address) *big.Int {
	t.Helper()

	return f.chain.Balance(t, f.ctx, account)
}

// TestChain_Delegated_SweepsEverything is the test the mocks cannot do: a real
// node has to accept the set-code transaction, apply the authorization, and run
// the contract inside the deposit wallet.
func TestChain_Delegated_SweepsEverything(t *testing.T) {
	f := newChainFixture(t, 2)

	destETHBefore := f.ethBalance(t, f.destination)

	if err := f.sweeper(t, false).SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("sweep failed: %v", err)
	}

	for _, wallet := range f.wallets {
		// The sponsor paid the fees, so the wallet keeps nothing back for gas.
		if got := f.ethBalance(t, wallet.Address); got.Sign() != 0 {
			t.Errorf("wallet %s still holds %s wei", wallet.Address.Hex(), got)
		}
		if got := f.tokenBalance(t, wallet.Address); got.Sign() != 0 {
			t.Errorf("wallet %s still holds %s tokens", wallet.Address.Hex(), got)
		}

		// The delegation is what let the contract run in the wallet's account.
		target, delegated, err := DelegationTarget(f.ctx, f.client, wallet.Address)
		if err != nil {
			t.Fatalf("reading delegation: %v", err)
		}
		if !delegated || target != f.contract {
			t.Errorf("wallet %s delegation: got (%s, %v), want %s", wallet.Address.Hex(), target.Hex(), delegated, f.contract.Hex())
		}
	}

	wantETH := new(big.Int).Mul(f.walletETH, big.NewInt(int64(len(f.wallets))))
	gotETH := new(big.Int).Sub(f.ethBalance(t, f.destination), destETHBefore)
	if gotETH.Cmp(wantETH) != 0 {
		t.Errorf("destination received %s wei, want %s", gotETH, wantETH)
	}

	wantTokens := new(big.Int).Mul(f.walletToken, big.NewInt(int64(len(f.wallets))))
	if got := f.tokenBalance(t, f.destination); got.Cmp(wantTokens) != 0 {
		t.Errorf("destination received %s tokens, want %s", got, wantTokens)
	}
}

// TestChain_Delegated_SecondSweepNeedsNoAuthorization covers the cheaper path a
// wallet takes once it is already delegated: no authorization, a plain
// dynamic-fee call, and the funds still move.
func TestChain_Delegated_SecondSweepNeedsNoAuthorization(t *testing.T) {
	f := newChainFixture(t, 1)
	wallet := f.wallets[0]

	if err := f.sweeper(t, false).SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("first sweep failed: %v", err)
	}

	// A new deposit lands in the wallet, which now already runs the contract.
	f.fund(t, wallet.Address)

	chainID, err := f.client.ChainID(f.ctx)
	if err != nil {
		t.Fatalf("chain ID: %v", err)
	}
	strategy := newDelegatedStrategy(sweepConfig{
		client:      f.client,
		paymentType: PaymentTypeL17702,
		destination: f.destination,
		tokens:      []common.Address{f.token},
		logger:      testLogger(),
	}, &Delegation{Contract: f.contract, Sponsor: f.sponsor})

	auths, err := strategy.authorizations(f.ctx, wallet, chainID)
	if err != nil {
		t.Fatalf("checking authorizations: %v", err)
	}
	if len(auths) != 0 {
		t.Fatalf("expected no authorization for an already delegated wallet, got %d", len(auths))
	}

	destTokensBefore := f.tokenBalance(t, f.destination)
	if err := f.sweeper(t, false).SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("second sweep failed: %v", err)
	}

	if got := f.ethBalance(t, wallet.Address); got.Sign() != 0 {
		t.Errorf("wallet still holds %s wei after the second sweep", got)
	}
	gotTokens := new(big.Int).Sub(f.tokenBalance(t, f.destination), destTokensBefore)
	if gotTokens.Cmp(f.walletToken) != 0 {
		t.Errorf("second sweep moved %s tokens, want %s", gotTokens, f.walletToken)
	}
}

// TestChain_Delegated_GasEstimateCoversTheSweep checks the gas bid against a
// real node: eth_estimateGas is asked with an authorization list, and whatever
// comes back has to be enough for the transaction that follows.
func TestChain_Delegated_GasEstimateCoversTheSweep(t *testing.T) {
	f := newChainFixture(t, 1)
	wallet := f.wallets[0]

	chainID, err := f.client.ChainID(f.ctx)
	if err != nil {
		t.Fatalf("chain ID: %v", err)
	}
	auth, err := SignDelegation(f.ctx, f.client, wallet, chainID, f.contract)
	if err != nil {
		t.Fatalf("signing delegation: %v", err)
	}
	auths := []types.SetCodeAuthorization{auth}
	data := SweepCallData([]common.Address{f.token})

	gasLimit, err := EstimateDelegatedSweepGas(f.ctx, f.client, f.sponsor.Address, wallet.Address, data, auths, 1)
	if err != nil {
		t.Fatalf("estimating gas: %v", err)
	}

	// Run the sweep and compare the bid against what it actually burned.
	if err := f.sweeper(t, false).SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("sweep failed: %v", err)
	}
	receipt := f.lastSweepReceipt(t, wallet.Address)
	if receipt.GasUsed > gasLimit {
		t.Fatalf("bid %d gas but the sweep used %d", gasLimit, receipt.GasUsed)
	}
	// A node that ignored the authorization list would have answered with
	// roughly the 21000 intrinsic cost, which the floor has to lift clear of
	// the real cost of installing a delegation and moving two assets.
	if receipt.GasUsed < 50_000 {
		t.Fatalf("sweep only used %d gas, the transaction cannot have done the work", receipt.GasUsed)
	}
}

// lastSweepReceipt finds the receipt of the transaction that swept wallet, by
// walking back from the head block.
func (f *chainFixture) lastSweepReceipt(t *testing.T, wallet common.Address) *types.Receipt {
	t.Helper()

	client := f.chain.Client()
	head, err := client.BlockNumber(f.ctx)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	for number := head; number > 0; number-- {
		block, err := client.BlockByNumber(f.ctx, new(big.Int).SetUint64(number))
		if err != nil {
			t.Fatalf("block %d: %v", number, err)
		}
		for _, tx := range block.Transactions() {
			if tx.To() == nil || *tx.To() != wallet {
				continue
			}
			receipt, err := client.TransactionReceipt(f.ctx, tx.Hash())
			if err != nil {
				t.Fatalf("receipt of %s: %v", tx.Hash().Hex(), err)
			}
			return receipt
		}
	}
	t.Fatalf("no transaction to %s found", wallet.Hex())
	return nil
}

// TestChain_Delegated_ReadsContractDestination checks DEST() against a really
// deployed contract, which is what the pre-flight check relies on.
func TestChain_Delegated_ReadsContractDestination(t *testing.T) {
	f := newChainFixture(t, 0)

	got, err := ContractDestination(f.ctx, f.client, f.contract)
	if err != nil {
		t.Fatalf("reading destination: %v", err)
	}
	if got != f.destination {
		t.Fatalf("destination: got %s, want %s", got.Hex(), f.destination.Hex())
	}
}

// TestChain_Delegated_WrongDestinationAborts makes sure the pre-flight check
// stops a sweep whose contract would send the funds elsewhere, against a real
// contract rather than a mocked answer.
func TestChain_Delegated_WrongDestinationAborts(t *testing.T) {
	f := newChainFixture(t, 1)

	elsewhere := common.HexToAddress("0x000000000000000000000000000000000000dEaD")
	sw := NewDelegatedSweeper(f.client, PaymentTypeL17702, elsewhere, []common.Address{f.token},
		&Delegation{Contract: f.contract, Sponsor: f.sponsor}, 0, 0, false, testLogger())

	err := sw.SweepAll(f.ctx, f.wallets)
	if err == nil || !strings.Contains(err.Error(), "not the configured destination") {
		t.Fatalf("expected the sweep to refuse the contract, got %v", err)
	}
	if got := f.ethBalance(t, f.wallets[0].Address); got.Cmp(f.walletETH) != 0 {
		t.Fatalf("wallet balance changed to %s, nothing should have moved", got)
	}
}

// TestChain_Delegated_DryRunMovesNothing runs the dry run against the chain: it
// has to describe the sweep without leaving a trace of it.
func TestChain_Delegated_DryRunMovesNothing(t *testing.T) {
	f := newChainFixture(t, 1)
	wallet := f.wallets[0]

	if err := f.sweeper(t, true).SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}

	if got := f.ethBalance(t, wallet.Address); got.Cmp(f.walletETH) != 0 {
		t.Errorf("wallet ETH changed to %s, want %s", got, f.walletETH)
	}
	if got := f.tokenBalance(t, wallet.Address); got.Cmp(f.walletToken) != 0 {
		t.Errorf("wallet tokens changed to %s, want %s", got, f.walletToken)
	}
	if _, delegated, err := DelegationTarget(f.ctx, f.client, wallet.Address); err != nil || delegated {
		t.Errorf("a dry run must not delegate the wallet (delegated=%v, err=%v)", delegated, err)
	}
}

// TestChain_Direct_SweepsEverything runs the wallet-pays-its-own-gas mechanism
// against the same chain, so the two are covered by the same kind of test.
func TestChain_Direct_SweepsEverything(t *testing.T) {
	f := newChainFixture(t, 1)
	wallet := f.wallets[0]

	gasSource := &KeyPair{PrivateKey: f.chain.Accounts[1], Address: f.chain.Address(1)}
	sw := NewSweeper(f.client, PaymentTypeL1, f.destination, []common.Address{f.token},
		gasSource, 0, 0, false, false, testLogger())

	if err := sw.SweepAll(f.ctx, f.wallets); err != nil {
		t.Fatalf("sweep failed: %v", err)
	}

	if got := f.tokenBalance(t, wallet.Address); got.Sign() != 0 {
		t.Errorf("wallet still holds %s tokens", got)
	}
	if got := f.tokenBalance(t, f.destination); got.Cmp(f.walletToken) != 0 {
		t.Errorf("destination holds %s tokens, want %s", got, f.walletToken)
	}
	// The wallet pays its own fees here, so it keeps whatever the last transfer
	// did not cost — but the bulk of the balance has to have moved.
	left := f.ethBalance(t, wallet.Address)
	if left.Cmp(new(big.Int).Div(f.walletETH, big.NewInt(100))) > 0 {
		t.Errorf("wallet kept %s wei, more than 1%% of its balance", left)
	}
}

// contractBytecode returns deployment bytecode with the constructor arguments
// appended, from a solc --bin artifact.
func contractBytecode(t *testing.T, binHex string, args ...[]byte) []byte {
	t.Helper()

	code, err := hex.DecodeString(strings.TrimSpace(binHex))
	if err != nil {
		t.Fatalf("decoding contract bytecode: %v", err)
	}
	for _, arg := range args {
		code = append(code, arg...)
	}
	return code
}

func encodeAddress(addr common.Address) []byte {
	var word [32]byte
	copy(word[12:], addr.Bytes())
	return word[:]
}

func encodeUint256(v uint64) []byte {
	var word [32]byte
	new(big.Int).SetUint64(v).FillBytes(word[:])
	return word[:]
}

// mintCallData builds calldata for TestToken.mint(address,uint256).
func mintCallData(to common.Address, amount *big.Int) []byte {
	data := make([]byte, 0, 4+64)
	data = append(data, methodID("mint(address,uint256)")...)
	data = append(data, encodeAddress(to)...)
	var word [32]byte
	amount.FillBytes(word[:])
	return append(data, word[:]...)
}

// testLoggerFor returns a logger that writes to the test log, useful when a
// chain test needs to be debugged.
func testLoggerFor(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(p)))
	return len(p), nil
}
