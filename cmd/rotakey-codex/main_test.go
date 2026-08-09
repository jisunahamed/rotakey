package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRemoveManagedBlockPreservesUserConfig(t *testing.T) {
	input := "model = \"native\"\n\n" + beginMarker + "\n[profiles.rotakey]\nmodel = \"demo\"\n" + endMarker + "\n[features]\nfoo = true\n"
	got, found, err := removeManagedBlock(input)
	if err != nil || !found {
		t.Fatalf("removeManagedBlock() found=%v err=%v", found, err)
	}
	if strings.Contains(got, "rotakey") || !strings.Contains(got, `model = "native"`) || !strings.Contains(got, "foo = true") {
		t.Fatalf("user config was not preserved:\n%s", got)
	}
}

func TestInstallConfigIsScopedAndReversible(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("ROTAKEY_CODEX_DATA_DIR", filepath.Join(root, "data"))
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	path, _ := codexConfigPath()
	if err := os.WriteFile(path, []byte("model = \"native\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	current := state{GatewayURL: "https://ai.example.com", DefaultModel: "rotakey/test"}
	if err := installConfig(current); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "[profiles.rotakey]") || !strings.Contains(string(body), `model = "native"`) {
		t.Fatalf("unexpected config:\n%s", body)
	}
	cleaned, found, err := removeManagedBlock(string(body))
	if err != nil || !found || strings.TrimSpace(cleaned) != `model = "native"` {
		t.Fatalf("managed block was not reversible: found=%v err=%v body=%q", found, err, cleaned)
	}
}

func TestWindowsDPAPISecretRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows DPAPI test")
	}
	t.Setenv("ROTAKEY_CODEX_DATA_DIR", t.TempDir())
	if err := ensureDirs(); err != nil {
		t.Fatal(err)
	}
	store, err := saveSecret("rk_test_secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deleteSecret(store) })
	got, err := loadSecret(store)
	if err != nil || got != "rk_test_secret" {
		t.Fatalf("secret round trip got %q, %v", got, err)
	}
}

func TestRemoveManagedBlockRejectsIncompleteMarkers(t *testing.T) {
	if _, _, err := removeManagedBlock(beginMarker + "\n"); err == nil {
		t.Fatal("expected incomplete marker error")
	}
}

func TestNormalizeGatewayURL(t *testing.T) {
	got, err := normalizeGatewayURL("https://ai.example.com/v1/")
	if err != nil || got != "https://ai.example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := normalizeGatewayURL("http://ai.example.com"); err == nil {
		t.Fatal("remote HTTP URL should be rejected")
	}
	if _, err := normalizeGatewayURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("loopback HTTP should be allowed: %v", err)
	}
}

func TestFetchManifestFiltersUnreadyModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatal("missing bearer key")
		}
		_, _ = w.Write([]byte(`{"models":[{"id":"ready","catalog_ready":true,"supports_responses":true},{"id":"warning","catalog_ready":false,"supports_responses":true}]}`))
	}))
	defer server.Close()
	got, err := fetchManifest(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "ready" {
		t.Fatalf("unexpected filtered manifest: %#v", got.Models)
	}
}
