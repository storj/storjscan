package sweeper

import (
	"bufio"
	"crypto/ecdsa"
	"fmt"
	"os"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// KeyPair holds a private key and its derived Ethereum address.
type KeyPair struct {
	PrivateKey *ecdsa.PrivateKey
	Address    common.Address
	LineNum    int
}

// LoadKeys reads private keys from a file (hex without 0x prefix, one per line)
// and returns the corresponding KeyPairs. Empty lines and whitespace are ignored.
func LoadKeys(path string) ([]KeyPair, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening key file: %w", err)
	}
	defer f.Close()

	var keys []KeyPair
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		pk, err := crypto.HexToECDSA(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid private key: %w", lineNum, err)
		}
		addr := crypto.PubkeyToAddress(pk.PublicKey)
		keys = append(keys, KeyPair{PrivateKey: pk, Address: addr, LineNum: lineNum})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	return keys, nil
}

// LoadFilterAddresses reads public addresses from a file (hex, one per line)
// and returns them. Empty lines and whitespace are ignored. Lines may optionally
// include a 0x prefix. All addresses are normalized to lowercase for comparison.
func LoadFilterAddresses(path string) ([]common.Address, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening filter file: %w", err)
	}
	defer f.Close()

	var addrs []common.Address
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !common.IsHexAddress(line) {
			return nil, fmt.Errorf("line %d: invalid address: %s", lineNum, line)
		}
		addrs = append(addrs, common.HexToAddress(line))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading filter file: %w", err)
	}
	return addrs, nil
}

// FilterKeys returns only the KeyPairs whose addresses are in the given list.
func FilterKeys(keys []KeyPair, addresses []common.Address) []KeyPair {
	allowed := make(map[common.Address]struct{}, len(addresses))
	for _, addr := range addresses {
		allowed[addr] = struct{}{}
	}
	var filtered []KeyPair
	for _, kp := range keys {
		if _, ok := allowed[kp.Address]; ok {
			filtered = append(filtered, kp)
		}
	}
	return filtered
}
