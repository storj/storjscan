// SPDX-License-Identifier: MIT
// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
pragma solidity ^0.8.24;

/// Multicall3 is the part of mds1/multicall the sweeper uses: aggregate3 to
/// batch balance queries, and getEthBalance to read native balances inside a
/// batch. The real contract is deployed at the same address on every chain the
/// sweeper runs against; a test chain starts empty, so this is preloaded at that
/// address to give the batch queries something to call.
contract Multicall3 {
    struct Call3 {
        address target;
        bool allowFailure;
        bytes callData;
    }

    struct Result {
        bool success;
        bytes returnData;
    }

    function aggregate3(Call3[] calldata calls) public payable returns (Result[] memory returnData) {
        uint256 length = calls.length;
        returnData = new Result[](length);
        for (uint256 i = 0; i < length; i++) {
            Result memory result = returnData[i];
            Call3 calldata calli = calls[i];
            (result.success, result.returnData) = calli.target.call(calli.callData);
            if (!calli.allowFailure && !result.success) {
                revert("Multicall3: call failed");
            }
        }
    }

    function getEthBalance(address addr) public view returns (uint256 balance) {
        balance = addr.balance;
    }
}
