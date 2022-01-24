// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

package dbx

import (
	// make sure we load our cockroach driver so dbx.Open can find it.
	_ "storj.io/private/dbutil/cockroachutil"
)
