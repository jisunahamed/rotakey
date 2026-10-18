package app

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

func retainCatalogFacts(profile map[string]string, previous []byte) {
	var facts map[string]string
	if json.Unmarshal(previous, &facts) != nil {
		return
	}
	if value := facts["context_window"]; value != "" && profile["context_window"] == "" {
		profile["context_window"] = value
	}
	if facts["capability_source"] == "provider_catalog" {
		for _, key := range []string{"images", "thinking", "tools", "streaming", "json_output", "web_search", "model_type", "capability_source"} {
			if value := facts[key]; value != "" {
				profile[key] = value
			}
		}
	}
}

// Catalog declarations are evidence of upstream support, not inference probes.
func catalogCapabilityFacts(model DiscoveredModel) map[string]string {
	facts := map[string]string{}
	if model.ContextWindow > 0 {
		facts["context_window"] = strconv.Itoa(model.ContextWindow)
	}
	if model.ModelType != "" {
		facts["model_type"] = model.ModelType
	}
	if model.Capabilities == nil {
		return facts
	}
	declared := map[string]bool{}
	for _, capability := range model.Capabilities {
		declared[strings.ToLower(strings.TrimSpace(capability))] = true
	}
	for capability, key := range map[string]string{
		"vision": "images", "reasoning": "thinking", "tools": "tools",
		"streaming": "streaming", "json_mode": "json_output", "web_search": "web_search",
	} {
		facts[key] = "unsupported"
		if declared[capability] {
			facts[key] = "catalog_supported"
		}
	}
	facts["capability_source"] = "provider_catalog"
	return facts
}

func isCleanAPIsProvider(provider Provider) bool {
	parsed, err := url.Parse(provider.BaseURL)
	return err == nil && (strings.EqualFold(parsed.Hostname(), "cleanapis.com") || strings.EqualFold(parsed.Hostname(), "www.cleanapis.com"))
}

// Clean APIs documents a minimum reasoning budget of 2048 and an omitted-cap
// default of 8192. Reserve the same amount we send, including translated calls.
func applyCatalogReplyBudget(payload map[string]any, route routeRuntime, endpoint string) {
	if !isCleanAPIsProvider(route.Provider) || route.Model.CapabilityProfile["thinking"] != "catalog_supported" {
		return
	}
	if !payloadCarriesOutputCap(payload) {
		applyReplyFloor(payload, route.Provider.APIFormat, endpoint, max(int64(route.Model.DefaultMaxOutputTokens), 8192))
		return
	}
	applyReplyFloor(payload, route.Provider.APIFormat, endpoint, 2048)
}
