package app

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func TestExpandRotakeyCompaction(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	vault, err := newVault(key)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{vault: vault}
	ciphertext, err := vault.Encrypt([]byte(`rotakey-compaction-v1:{"prior":"context"}`))
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"input": []any{
		map[string]any{"type": "compaction", "encrypted_content": base64.RawURLEncoding.EncodeToString(ciphertext)},
		map[string]any{"type": "message", "role": "user", "content": "continue"},
	}}
	if err := server.expandRotakeyCompaction(request); err != nil {
		t.Fatal(err)
	}
	input := request["input"].([]any)
	first := input[0].(map[string]any)
	if first["type"] != "message" || first["role"] != "developer" {
		t.Fatalf("unexpected expanded item: %#v", first)
	}
}

func TestExpandRotakeyCompactionRejectsForeignCiphertext(t *testing.T) {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	vault, _ := newVault(key)
	server := &Server{vault: vault}
	request := map[string]any{"input": []any{map[string]any{
		"type": "compaction", "encrypted_content": base64.RawURLEncoding.EncodeToString([]byte("foreign")),
	}}}
	if err := server.expandRotakeyCompaction(request); err == nil {
		t.Fatal("expected foreign compaction to be rejected")
	}
}
