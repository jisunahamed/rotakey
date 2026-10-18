package app

import "testing"

func TestCatalogCapabilityFacts(t *testing.T) {
	facts := catalogCapabilityFacts(DiscoveredModel{
		ID: "clean/model", ContextWindow: 128000,
		Capabilities: []string{"reasoning", "vision", "tools", "streaming", "json_mode"},
	})
	for key, want := range map[string]string{
		"context_window": "128000", "images": "catalog_supported", "thinking": "catalog_supported",
		"tools": "catalog_supported", "streaming": "catalog_supported", "json_output": "catalog_supported",
		"web_search": "unsupported", "capability_source": "provider_catalog",
	} {
		if facts[key] != want {
			t.Fatalf("%s = %q, want %q", key, facts[key], want)
		}
	}
}

func TestPublicModelMetadataIncludesDocumentedCapabilities(t *testing.T) {
	metadata := publicModelMetadata(ModelRoute{CapabilityProfile: map[string]string{
		"context_window": "128000", "images": "catalog_supported", "thinking": "catalog_supported", "tools": "catalog_supported", "streaming": "catalog_supported",
	}})
	if metadata["context_window"] != 128000 {
		t.Fatalf("context window = %#v", metadata["context_window"])
	}
	capabilities := metadata["capabilities"].([]string)
	if len(capabilities) != 4 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}
