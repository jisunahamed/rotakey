package app

import (
	"encoding/base64"
	"strings"
)

const anthropicDiscoveryModelPrefix = "claude-rotakey-v1-"

// Claude Code only accepts discovered model IDs beginning with "claude" or
// "anthropic". Non-Anthropic public aliases therefore get a reversible wire ID
// while their display name and Rotakey route stay unchanged.
func anthropicDiscoveryModelID(alias string) string {
	lower := strings.ToLower(alias)
	if strings.HasPrefix(lower, "claude") || strings.HasPrefix(lower, "anthropic") {
		return alias
	}
	return anthropicDiscoveryModelPrefix + base64.RawURLEncoding.EncodeToString([]byte(alias))
}

func resolveAnthropicDiscoveryModelID(modelID string) string {
	if !strings.HasPrefix(modelID, anthropicDiscoveryModelPrefix) {
		return modelID
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(modelID, anthropicDiscoveryModelPrefix))
	if err != nil {
		return modelID
	}
	alias := string(decoded)
	if !aliasPattern.MatchString(alias) || anthropicDiscoveryModelID(alias) != modelID {
		return modelID
	}
	return alias
}
