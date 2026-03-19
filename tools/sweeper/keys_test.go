package sweeper

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keys.txt")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadKeys_ValidKeys(t *testing.T) {
	// Generate two test keys.
	pk1, _ := crypto.GenerateKey()
	pk2, _ := crypto.GenerateKey()
	hex1 := fmt.Sprintf("%x", crypto.FromECDSA(pk1))
	hex2 := fmt.Sprintf("%x", crypto.FromECDSA(pk2))

	path := writeTestFile(t, hex1+"\n"+hex2+"\n")
	keys, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0].Address != crypto.PubkeyToAddress(pk1.PublicKey) {
		t.Fatalf("first key address mismatch")
	}
	if keys[1].Address != crypto.PubkeyToAddress(pk2.PublicKey) {
		t.Fatalf("second key address mismatch")
	}
}

func TestLoadKeys_EmptyLines(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	hex := fmt.Sprintf("%x", crypto.FromECDSA(pk))

	path := writeTestFile(t, "\n\n"+hex+"\n\n")
	keys, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestLoadKeys_Whitespace(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	hex := fmt.Sprintf("%x", crypto.FromECDSA(pk))

	path := writeTestFile(t, "  "+hex+"  \n")
	keys, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
}

func TestLoadKeys_InvalidHex(t *testing.T) {
	path := writeTestFile(t, "not-a-valid-hex-key\n")
	_, err := LoadKeys(path)
	if err == nil {
		t.Fatal("expected error for invalid hex")
	}
}

func TestLoadKeys_EmptyFile(t *testing.T) {
	path := writeTestFile(t, "")
	keys, err := LoadKeys(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(keys))
	}
}

func TestLoadKeys_FileNotFound(t *testing.T) {
	_, err := LoadKeys("/nonexistent/path/keys.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFilterAddresses_Valid(t *testing.T) {
	pk1, _ := crypto.GenerateKey()
	pk2, _ := crypto.GenerateKey()
	addr1 := crypto.PubkeyToAddress(pk1.PublicKey)
	addr2 := crypto.PubkeyToAddress(pk2.PublicKey)

	path := writeTestFile(t, addr1.Hex()+"\n"+addr2.Hex()+"\n")
	addrs, err := LoadFilterAddresses(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("expected 2 addresses, got %d", len(addrs))
	}
	if addrs[0] != addr1 {
		t.Fatalf("first address mismatch")
	}
	if addrs[1] != addr2 {
		t.Fatalf("second address mismatch")
	}
}

func TestLoadFilterAddresses_LowercaseNoPrefx(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(pk.PublicKey)
	// Write lowercase without 0x prefix.
	hexStr := fmt.Sprintf("%x", addr.Bytes())

	path := writeTestFile(t, hexStr+"\n")
	addrs, err := LoadFilterAddresses(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 address, got %d", len(addrs))
	}
	if addrs[0] != addr {
		t.Fatalf("address mismatch: got %s, want %s", addrs[0].Hex(), addr.Hex())
	}
}

func TestLoadFilterAddresses_EmptyLines(t *testing.T) {
	pk, _ := crypto.GenerateKey()
	addr := crypto.PubkeyToAddress(pk.PublicKey)

	path := writeTestFile(t, "\n\n"+addr.Hex()+"\n\n")
	addrs, err := LoadFilterAddresses(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 1 {
		t.Fatalf("expected 1 address, got %d", len(addrs))
	}
}

func TestLoadFilterAddresses_InvalidAddress(t *testing.T) {
	path := writeTestFile(t, "not-a-valid-address\n")
	_, err := LoadFilterAddresses(path)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestLoadFilterAddresses_EmptyFile(t *testing.T) {
	path := writeTestFile(t, "")
	addrs, err := LoadFilterAddresses(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 0 {
		t.Fatalf("expected 0 addresses, got %d", len(addrs))
	}
}

func TestLoadFilterAddresses_FileNotFound(t *testing.T) {
	_, err := LoadFilterAddresses("/nonexistent/path/filter.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestFilterKeys_MatchSome(t *testing.T) {
	pk1, _ := crypto.GenerateKey()
	pk2, _ := crypto.GenerateKey()
	pk3, _ := crypto.GenerateKey()
	keys := []KeyPair{
		{PrivateKey: pk1, Address: crypto.PubkeyToAddress(pk1.PublicKey)},
		{PrivateKey: pk2, Address: crypto.PubkeyToAddress(pk2.PublicKey)},
		{PrivateKey: pk3, Address: crypto.PubkeyToAddress(pk3.PublicKey)},
	}

	filtered := FilterKeys(keys, []common.Address{keys[0].Address, keys[2].Address})
	if len(filtered) != 2 {
		t.Fatalf("expected 2 filtered keys, got %d", len(filtered))
	}
	if filtered[0].Address != keys[0].Address {
		t.Fatalf("first filtered key mismatch")
	}
	if filtered[1].Address != keys[2].Address {
		t.Fatalf("second filtered key mismatch")
	}
}

func TestFilterKeys_MatchNone(t *testing.T) {
	pk1, _ := crypto.GenerateKey()
	keys := []KeyPair{
		{PrivateKey: pk1, Address: crypto.PubkeyToAddress(pk1.PublicKey)},
	}

	filtered := FilterKeys(keys, []common.Address{{0x01}})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered keys, got %d", len(filtered))
	}
}

func TestFilterKeys_EmptyFilter(t *testing.T) {
	pk1, _ := crypto.GenerateKey()
	keys := []KeyPair{
		{PrivateKey: pk1, Address: crypto.PubkeyToAddress(pk1.PublicKey)},
	}

	filtered := FilterKeys(keys, nil)
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered keys, got %d", len(filtered))
	}
}

func TestFilterKeys_EmptyKeys(t *testing.T) {
	filtered := FilterKeys(nil, []common.Address{{0x01}})
	if len(filtered) != 0 {
		t.Fatalf("expected 0 filtered keys, got %d", len(filtered))
	}
}
