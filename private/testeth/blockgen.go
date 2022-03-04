// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package testeth

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/clique"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/params"
	"github.com/zeebo/errs"
)

var (
	// BlockGenError is chain generator error class.
	BlockGenError = errs.Class("BlockGen")

	// Ensure chainHeaderReader implements ChainHeaderReader.
	_ consensus.ChainHeaderReader = (*chainHeaderReader)(nil)
)

// BlockGen generates signed blocks for Ethereum POS chain.
type BlockGen struct {
	config  *params.ChainConfig
	signer  accounts.Wallet
	headers consensus.ChainHeaderReader
	engine  consensus.Engine
	db      ethdb.Database
}

// NewBlockGen creates new block generator instance.
func NewBlockGen(config *params.ChainConfig, signer accounts.Wallet, headers consensus.ChainHeaderReader, engine consensus.Engine, db ethdb.Database) *BlockGen {
	return &BlockGen{
		config:  config,
		signer:  signer,
		headers: headers,
		engine:  engine,
		db:      db,
	}
}

// GenerateChain generates new chain after a given parent with specified duration between blocks.
func (blockGen *BlockGen) GenerateChain(ctx context.Context, parent *types.Header, count int, delay time.Duration) (types.Blocks, error) {
	var blocks types.Blocks

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if count <= 0 {
		return nil, nil
	}

	block, err := blockGen.CreateBlock(parent, delay)
	if err != nil {
		return nil, err
	}
	blocks = append(blocks, block)

	for i := 1; i < count; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		h := blocks[i-1].Header()
		block, err := blockGen.CreateBlock(h, delay)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}

	return blocks, nil
}

// CreateBlock creates new signed block after a given parent with specified delay.
func (blockGen *BlockGen) CreateBlock(parent *types.Header, delay time.Duration) (*types.Block, error) {
	header := &types.Header{
		Number:     big.NewInt(parent.Number.Int64() + 1),
		ParentHash: parent.Hash(),
		GasLimit:   parent.GasLimit,
		BaseFee:    misc.CalcBaseFee(blockGen.config, parent),
	}

	chainReader := &chainHeaderReader{
		ChainHeaderReader: blockGen.headers,
		header:            parent,
	}
	err := blockGen.engine.Prepare(chainReader, header)
	if err != nil {
		return nil, BlockGenError.Wrap(err)
	}
	header.Time = uint64(time.Unix(int64(parent.Time), 0).Add(delay).Unix())

	statedb, err := state.New(parent.Root, state.NewDatabase(blockGen.db), nil)
	if err != nil {
		return nil, BlockGenError.Wrap(err)
	}
	block, err := blockGen.engine.FinalizeAndAssemble(blockGen.headers, header, statedb, nil, nil, nil)
	if err != nil {
		return nil, BlockGenError.Wrap(err)
	}

	header = block.Header()
	sig, err := blockGen.signer.SignData(blockGen.signer.Accounts()[0], accounts.MimetypeClique, clique.CliqueRLP(header))
	if err != nil {
		return nil, BlockGenError.Wrap(err)
	}
	copy(header.Extra[len(header.Extra)-crypto.SignatureLength:], sig)

	return block.WithSeal(header), nil
}

// chainHeaderReader wraps consensus chain header reader to return parent header that is not added to the chain db.
type chainHeaderReader struct {
	consensus.ChainHeaderReader
	header *types.Header
}

// GetHeader returns particular non existing parent header or fallback to provided chain header reader.
func (chainReader *chainHeaderReader) GetHeader(hash common.Hash, number uint64) *types.Header {
	if hash == chainReader.header.Hash() && number == chainReader.header.Number.Uint64() {
		h := new(types.Header)
		*h = *chainReader.header
		return h
	}
	return chainReader.ChainHeaderReader.GetHeader(hash, number)
}

// GetHeaderByNumber returns particular non existing parent header by number or fallback to provided chain header reader.
func (chainReader *chainHeaderReader) GetHeaderByNumber(number uint64) *types.Header {
	if number == chainReader.header.Number.Uint64() {
		h := new(types.Header)
		*h = *chainReader.header
		return h
	}
	return chainReader.ChainHeaderReader.GetHeaderByNumber(number)
}
