// Copyright (C) 2026 Storj Labs, Inc.
// See LICENSE for copying information.
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
