package app

import "strings"

func modelReasoningLevels(model ModelRoute) []string {
	levels := []string{}
	for _, level := range strings.Split(model.CapabilityProfile["reasoning_levels"], ",") {
		switch strings.TrimSpace(level) {
		case "low", "medium", "high", "xhigh", "max":
			levels = append(levels, strings.TrimSpace(level))
		}
	}
	return levels
}

func addAnthropicModelFacts(info map[string]any, model ModelRoute) {
	metadata := publicModelMetadata(model)
	if context, ok := metadata["context_window"]; ok {
		info["max_input_tokens"] = context
		info["context_window"] = context
	}
	profile := model.CapabilityProfile
	if profile["capability_source"] != "nvidia_official_catalog" && profile["reasoning_levels"] == "" {
		return
	}
	caps := map[string]any{}
	if profile["thinking"] == "catalog_supported" || profile["thinking"] == "unsupported" {
		support := profile["thinking"] == "catalog_supported"
		types := map[string]any{}
		for _, kind := range []string{"enabled", "disabled", "adaptive", "between_tools"} {
			types[kind] = map[string]bool{"supported": profile["thinking_control"] != "" && kind != "between_tools"}
		}
		caps["thinking"] = map[string]any{"supported": support, "types": types}
	}
	levels := modelReasoningLevels(model)
	effort := map[string]any{"supported": len(levels) > 0}
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		supported := false
		for _, known := range levels {
			if level == known {
				supported = true
			}
		}
		effort[level] = map[string]bool{"supported": supported}
	}
	caps["effort"] = effort
	for key, field := range map[string]string{"image_input": "images", "structured_outputs": "json_output"} {
		if value := profile[field]; value == "unsupported" || value == "catalog_supported" {
			caps[key] = map[string]bool{"supported": value == "catalog_supported"}
		}
	}
	info["capabilities"] = caps
}
