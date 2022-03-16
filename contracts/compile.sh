# Copyright (C) 2022 Storj Labs, Inc.
# See LICENSE for copying information.
#!/bin/bash

solc --bin --abi --overwrite -o build/ TestToken.sol \
    --base-path . \
    --include-path node_modules/
