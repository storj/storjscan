// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.

package storjscan

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"storj.io/common/currency"
	"storj.io/common/testcontext"
	"storj.io/storjscan/common"
	"storj.io/storjscan/tokenprice"
)

func TestCheckFixedPriceEndpoints(t *testing.T) {
	require.NoError(t, checkFixedPriceEndpoints([]common.EthEndpoint{
		{Name: "mainnet", Currency: "USDC"},
		{Name: "l2", Currency: "USDC"},
	}))

	require.Error(t, checkFixedPriceEndpoints([]common.EthEndpoint{
		{Name: "mainnet", Currency: "STORJ"},
	}))

	require.Error(t, checkFixedPriceEndpoints([]common.EthEndpoint{
		{Name: "mainnet"},
	}))

	require.Error(t, checkFixedPriceEndpoints([]common.EthEndpoint{
		{Name: "mainnet", Currency: "USDC"},
		{Name: "l2", Currency: "STORJ"},
	}))
}

func TestNewTokenPrice(t *testing.T) {
	ctx := testcontext.New(t)
	log := zaptest.NewLogger(t)
	usdc := []common.EthEndpoint{{Name: "mainnet", Currency: "USDC"}}
	storj := []common.EthEndpoint{{Name: "mainnet", Currency: "STORJ"}}

	t.Run("nothing configured", func(t *testing.T) {
		_, _, err := newTokenPrice(log, tokenprice.Config{}, nil, usdc)
		require.Error(t, err)
	})

	t.Run("both configured", func(t *testing.T) {
		config := tokenprice.Config{FixedPrice: "1"}
		config.CoinmarketcapConfig.APIKey = "key"
		_, _, err := newTokenPrice(log, config, nil, usdc)
		require.Error(t, err)

		_, _, err = newTokenPrice(log, tokenprice.Config{FixedPrice: "1", UseTestPrices: true}, nil, usdc)
		require.Error(t, err)
	})

	t.Run("fixed", func(t *testing.T) {
		price, chore, err := newTokenPrice(log, tokenprice.Config{FixedPrice: "1.5"}, nil, usdc)
		require.NoError(t, err)
		require.Nil(t, chore)
		p, err := price.PriceAt(ctx, time.Now())
		require.NoError(t, err)
		require.Equal(t, currency.AmountFromBaseUnits(1500000, currency.USDollarsMicro), p)
	})

	t.Run("invalid fixed", func(t *testing.T) {
		for _, fixed := range []string{"abc", "0", "-1"} {
			_, _, err := newTokenPrice(log, tokenprice.Config{FixedPrice: fixed}, nil, usdc)
			require.Error(t, err, fixed)
		}
	})

	t.Run("fixed with STORJ endpoint", func(t *testing.T) {
		_, _, err := newTokenPrice(log, tokenprice.Config{FixedPrice: "1"}, nil, storj)
		require.Error(t, err)
	})

	t.Run("coinmarketcap without token ID", func(t *testing.T) {
		config := tokenprice.Config{}
		config.CoinmarketcapConfig.APIKey = "key"
		_, _, err := newTokenPrice(log, config, nil, storj)
		require.Error(t, err)
	})

	t.Run("coinmarketcap", func(t *testing.T) {
		config := tokenprice.Config{}
		config.CoinmarketcapConfig.APIKey = "key"
		config.CoinmarketcapConfig.TokenID = "1772"
		price, chore, err := newTokenPrice(log, config, nil, storj)
		require.NoError(t, err)
		require.NotNil(t, chore)
		require.IsType(t, &tokenprice.CoinmarketcapPrice{}, price)
	})
}
