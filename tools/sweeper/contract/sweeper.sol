// SPDX-License-Identifier: MIT
// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
pragma solidity ^0.8.24;

interface IERC20 {
    function balanceOf(address) external view returns (uint256);
    function transfer(address, uint256) external returns (bool);
}

contract Sweeper7702 {
    address payable public immutable DEST;

    constructor(address payable dest) {
        DEST = dest;
    }

    function sweep(address[] calldata tokens) external {
        for (uint256 i; i < tokens.length; ++i) {
            address token = tokens[i];

            (bool ok, bytes memory data) =
                token.staticcall(
                    abi.encodeCall(IERC20.balanceOf, (address(this)))
                );

            if (!ok || data.length < 32) continue;

            uint256 bal = abi.decode(data, (uint256));
            if (bal == 0) continue;

            (bool transferOK, bytes memory result) =
                token.call(
                    abi.encodeCall(IERC20.transfer, (DEST, bal))
                );

            if (!transferOK) continue;
            if (result.length != 0 && !abi.decode(result, (bool))) continue;
        }

        uint256 v = address(this).balance;
        if (v != 0) {
            (bool sent,) = DEST.call{value: v}("");
            if (!sent) revert();
        }
    }

    receive() external payable {}
}
