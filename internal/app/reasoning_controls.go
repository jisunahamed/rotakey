package app

// readReasoningControl reads public wire vocabularies without changing the
// caller's original request. Translation happens for each fallback candidate.
func readReasoningControl(source map[string]any) (effort string, thinking map[string]any) {
	effort, _ = source["reasoning_effort"].(string)
	if r, ok := source["reasoning"].(map[string]any); ok && effort == "" {
		effort, _ = r["effort"].(string)
	}
	if r, ok := source["output_config"].(map[string]any); ok && effort == "" {
		effort, _ = r["effort"].(string)
	}
	thinking, _ = source["thinking"].(map[string]any)
	return
}

// Preserve effort across all three public APIs. Model-specific switches are
// applied only when the exact NVIDIA catalog entry documents that control.
func applyReasoningControl(payload map[string]any, source map[string]any, route routeRuntime, endpoint string) {
	effort, thinking := readReasoningControl(source)
	switch endpoint {
	case "messages":
		if effort != "" {
			config, _ := payload["output_config"].(map[string]any)
			if config == nil {
				config = map[string]any{}
			}
			config["effort"] = effort
			payload["output_config"] = config
		}
		if thinking != nil {
			payload["thinking"] = thinking
		}
	case "responses":
		if effort != "" {
			config, _ := payload["reasoning"].(map[string]any)
			if config == nil {
				config = map[string]any{}
			}
			config["effort"] = effort
			payload["reasoning"] = config
		}
	default:
		if template, ok := source["chat_template_kwargs"].(map[string]any); ok {
			copy := map[string]any{}
			for k, v := range template {
				copy[k] = v
			}
			payload["chat_template_kwargs"] = copy
		}
		if effort != "" {
			payload["reasoning_effort"] = effort
		}
		facts := nvidiaFacts(route.Provider, route.Model.UpstreamModel)
		if thinking != nil && facts["thinking_control"] != "" {
			kind, _ := thinking["type"].(string)
			if kind == "disabled" || kind == "enabled" || kind == "adaptive" {
				config, _ := payload["chat_template_kwargs"].(map[string]any)
				if config == nil {
					config = map[string]any{}
				}
				// Clone nested settings before adding a provider-specific flag.
				copy := map[string]any{}
				for k, v := range config {
					copy[k] = v
				}
				copy["enable_thinking"] = kind != "disabled"
				payload["chat_template_kwargs"] = copy
				delete(payload, "thinking")
			}
		}
	}
}
