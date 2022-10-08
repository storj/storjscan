// Copyright (C) 2022 Storj Labs, Inc.
// See LICENSE for copying information.

package cleanup_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/testcontext"
	"storj.io/storjscan/blockchain"
	"storj.io/storjscan/blockchain/cleanup"
	"storj.io/storjscan/storjscandb/storjscandbtest"
)

func TestChore(t *testing.T) {
	storjscandbtest.Run(t, func(ctx *testcontext.Context, t *testing.T, db *storjscandbtest.DB) {

		currentTime := time.Now().Truncate(time.Millisecond)
		var headers []blockchain.Header
		b := make([]byte, common.HashLength)
		_, err := rand.Read(b)
		require.NoError(t, err)
		headers = append(headers, blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    0,
			Timestamp: currentTime,
		})
		_, err = rand.Read(b)
		require.NoError(t, err)
		headers = append(headers, blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    1,
			Timestamp: currentTime.AddDate(0, 0, -29),
		})
		_, err = rand.Read(b)
		require.NoError(t, err)
		headers = append(headers, blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    2,
			Timestamp: currentTime.AddDate(0, 0, -31),
		})
		_, err = rand.Read(b)
		require.NoError(t, err)
		headers = append(headers, blockchain.Header{
			Hash:      blockchain.HashFromBytes(b),
			Number:    3,
			Timestamp: currentTime.AddDate(-1, 0, 0),
		})

		for _, header := range headers {
			err := db.Headers().Insert(ctx, header.Hash, header.Number, header.Timestamp)
			require.NoError(t, err)
		}

		// initially, all 4 headers in the cache
		for _, header := range headers {
			dbHeader, err := db.Headers().Get(ctx, header.Hash)
			require.NoError(t, err)
			require.Equal(t, header.Hash, dbHeader.Hash)
			require.Equal(t, header.Timestamp, dbHeader.Timestamp.Local())
		}

		chore := cleanup.NewChore(zaptest.NewLogger(t), db.Headers(), cleanup.Config{
			Interval:   336 * time.Hour,
			RetainDays: 30,
		})

		defer ctx.Check(chore.Close)
		ctx.Go(func() error {
			return chore.Run(ctx)
		})

		chore.Loop.Pause()
		chore.Loop.TriggerWait()

		// after the chore, the entries newer than 30 days should be returned
		dbHeader, err := db.Headers().Get(ctx, headers[0].Hash)
		require.NoError(t, err)
		require.Equal(t, headers[0].Hash, dbHeader.Hash)
		require.Equal(t, headers[0].Timestamp, dbHeader.Timestamp.Local())
		dbHeader, err = db.Headers().Get(ctx, headers[1].Hash)
		require.NoError(t, err)
		require.Equal(t, headers[1].Hash, dbHeader.Hash)
		require.Equal(t, headers[1].Timestamp, dbHeader.Timestamp.Local())
		// the entries older than 30 days should be gone
		dbHeader, err = db.Headers().Get(ctx, headers[2].Hash)
		require.Error(t, err)
		require.Equal(t, blockchain.ErrNoHeader, err)
		require.Equal(t, blockchain.Header{}, dbHeader)
		dbHeader, err = db.Headers().Get(ctx, headers[3].Hash)
		require.Error(t, err)
		require.Equal(t, blockchain.ErrNoHeader, err)
		require.Equal(t, blockchain.Header{}, dbHeader)
	})
}
