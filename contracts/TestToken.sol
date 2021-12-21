// SPDX-License-Identifier: MIT
// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

pragma solidity ^0.8.0;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";

contract TestToken is ERC20 {
    constructor(uint256 _supply) ERC20("TestToken", "TT") {
        _mint(msg.sender, _supply * 10 ** decimals());
    }
}
