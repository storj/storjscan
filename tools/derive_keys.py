#!/usr/bin/env uv run --with hdwallet==2.2.1 python3
"""Derive Ethereum private keys from a BIP39 mnemonic seed phrase."""

import argparse
import sys

from hdwallet import HDWallet
from hdwallet.symbols import ETH


def derive_keys(mnemonic: str, n: int) -> list[str]:
    """Derive private keys for indices 0 through n (inclusive)."""
    hdwallet = HDWallet(symbol=ETH)
    hdwallet.from_mnemonic(mnemonic=mnemonic.strip())

    private_keys = []
    for i in range(n + 1):
        hdwallet.from_path(f"m/44'/60'/0'/0/{i}")
        private_keys.append(hdwallet.private_key())
        hdwallet.clean_derivation()

    return private_keys


def main():
    parser = argparse.ArgumentParser(
        description="Derive Ethereum private keys from a BIP39 mnemonic"
    )
    parser.add_argument(
        "-m", "--mnemonic-file",
        required=True,
        help="Path to file containing BIP39 mnemonic seed phrase"
    )
    parser.add_argument(
        "-n", "--count",
        type=int,
        required=True,
        help="Derive keys from index 0 to n (inclusive)"
    )
    args = parser.parse_args()

    try:
        with open(args.mnemonic_file, "r") as f:
            mnemonic = f.read().strip()
    except FileNotFoundError:
        print(f"Error: File not found: {args.mnemonic_file}", file=sys.stderr)
        sys.exit(1)
    except IOError as e:
        print(f"Error reading file: {e}", file=sys.stderr)
        sys.exit(1)

    if args.count < 0:
        print("Error: count must be non-negative", file=sys.stderr)
        sys.exit(1)

    try:
        private_keys = derive_keys(mnemonic, args.count)
    except Exception as e:
        print(f"Error deriving keys: {e}", file=sys.stderr)
        sys.exit(1)

    for key in private_keys:
        print(key)


if __name__ == "__main__":
    main()
