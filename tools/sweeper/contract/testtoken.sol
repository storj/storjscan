// SPDX-License-Identifier: MIT
// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
pragma solidity ^0.8.24;

/// TestToken is a minimal ERC20, only used by the sweeper's integration tests
/// to give deposit wallets something to sweep. Anyone can mint: it is not meant
/// to be deployed anywhere real.
contract TestToken {
    string public constant name = "Test Token";
    string public constant symbol = "TEST";
    uint8 public immutable decimals;

    mapping(address => uint256) public balanceOf;

    event Transfer(address indexed from, address indexed to, uint256 value);

    constructor(uint8 decimals_) {
        decimals = decimals_;
    }

    function mint(address to, uint256 amount) external {
        balanceOf[to] += amount;
        emit Transfer(address(0), to, amount);
    }

    function transfer(address to, uint256 amount) external returns (bool) {
        require(balanceOf[msg.sender] >= amount, "insufficient balance");
        balanceOf[msg.sender] -= amount;
        balanceOf[to] += amount;
        emit Transfer(msg.sender, to, amount);
        return true;
    }
}
