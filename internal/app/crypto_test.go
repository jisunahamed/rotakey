package app

import (
	"bytes"
	"testing"
)

func TestVaultRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	vault, err := newVault(key)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("provider-secret")
	ciphertext, err := vault.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, plain) {
		t.Fatal("ciphertext contains plaintext")
	}
	decoded, err := vault.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, plain) {
		t.Fatalf("got %q, want %q", decoded, plain)
	}
}

func TestPasswordHash(t *testing.T) {
	encoded, err := hashPassword("a-strong-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if !verifyPassword(encoded, "a-strong-passphrase") {
		t.Fatal("correct password did not verify")
	}
	if verifyPassword(encoded, "wrong-passphrase") {
		t.Fatal("incorrect password verified")
	}
}
