// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.

package common_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"storj.io/common/currency"
	"storj.io/storjscan/common"
)

func TestEthEndpointTokenCurrency(t *testing.T) {
	_, err := common.EthEndpoint{}.TokenCurrency()
	require.Error(t, err)

	c, err := common.EthEndpoint{Currency: "STORJ"}.TokenCurrency()
	require.NoError(t, err)
	require.Equal(t, currency.StorjToken, c)

	c, err = common.EthEndpoint{Currency: "USDC"}.TokenCurrency()
	require.NoError(t, err)
	require.Equal(t, currency.USDC, c)

	_, err = common.EthEndpoint{Currency: "DOGE"}.TokenCurrency()
	require.Error(t, err)
}
