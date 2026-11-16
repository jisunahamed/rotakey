package app

import (
	"encoding/json"
	"testing"
)

func TestNvidiaOfficialCatalogMatchesExactModelsAndProvider(t *testing.T) {
	p := Provider{BaseURL: "https://integrate.api.nvidia.com/v1"}
	for model, want := range map[string]string{"nvidia/nemotron-3-ultra-550b-a55b": "1048576", "minimaxai/minimax-m3.1-flash": "262144", "nvidia/nemotron-3.5-bolt-e4b": "65536"} {
		if got := nvidiaFacts(p, model)["context_window"]; got != want {
			t.Fatalf("%s: %s", model, got)
		}
	}
	if nvidiaFacts(p, "minimax") != nil || nvidiaFacts(Provider{BaseURL: "https://example.com/v1"}, "minimaxai/minimax-m3.1-flash") != nil {
		t.Fatal("unverified model/provider received NVIDIA facts")
	}
}

func TestReasoningEffortSurvivesEveryPublicProtocol(t *testing.T) {
	for name, source := range map[string]map[string]any{
		"chat": {"reasoning_effort": "high"}, "responses": {"reasoning": map[string]any{"effort": "high"}}, "messages": {"output_config": map[string]any{"effort": "high"}},
	} {
		for _, endpoint := range []string{"chat", "responses", "messages"} {
			t.Run(name+"-to-"+endpoint, func(t *testing.T) {
				payload := map[string]any{}
				applyReasoningControl(payload, source, routeRuntime{}, endpoint)
				effort, _ := readReasoningControl(payload)
				if effort != "high" {
					t.Fatalf("effort lost: %#v", payload)
				}
			})
		}
	}
}

func TestNvidiaThinkingToggleDoesNotMutateInputOrInventEffort(t *testing.T) {
	route := routeRuntime{Provider: Provider{BaseURL: "https://integrate.api.nvidia.com/v1"}, Model: ModelRoute{UpstreamModel: "nvidia/nemotron-3-ultra-550b-a55b"}}
	original := map[string]any{"thinking": map[string]any{"type": "disabled"}}
	existing := map[string]any{"other_option": true}
	payload := map[string]any{"chat_template_kwargs": existing}
	applyReasoningControl(payload, original, route, "chat")
	opts := payload["chat_template_kwargs"].(map[string]any)
	if opts["enable_thinking"] != false || opts["other_option"] != true {
		t.Fatal(payload)
	}
	if _, changed := existing["enable_thinking"]; changed {
		t.Fatal("modified original provider settings")
	}
	if len(modelReasoningLevels(ModelRoute{CapabilityProfile: nvidiaFacts(route.Provider, route.Model.UpstreamModel)})) != 0 {
		t.Fatal("invented effort levels for a toggle-only model")
	}
}

func TestAnthropicDiscoveryPublishesNestedCapabilities(t *testing.T) {
	model := ModelRoute{CapabilityProfile: nvidiaFacts(Provider{BaseURL: "https://integrate.api.nvidia.com/v1"}, "openai/gpt-oss-20b")}
	info := map[string]any{}
	addAnthropicModelFacts(info, model)
	if info["max_input_tokens"] != 131072 {
		t.Fatal(info)
	}
	raw, _ := json.Marshal(info)
	var result struct {
		Capabilities struct {
			Effort struct {
				High  struct{ Supported bool }
				Xhigh struct{ Supported bool }
			}
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Capabilities.Effort.High.Supported || result.Capabilities.Effort.Xhigh.Supported {
		t.Fatalf("incorrect supported levels: %s", raw)
	}
}

func TestGLMEffortDiscoveryAndProtocolMapping(t *testing.T) {
	model := ModelRoute{UpstreamModel: "z-ai/glm-5.3-flash", CapabilityProfile: nvidiaFacts(Provider{BaseURL: "https://integrate.api.nvidia.com/v1"}, "z-ai/glm-5.3-flash")}
	info := map[string]any{}
	addAnthropicModelFacts(info, model)
	effort := info["capabilities"].(map[string]any)["effort"].(map[string]any)
	for _, level := range []string{"low", "medium", "high", "xhigh", "max"} {
		want := level == "low" || level == "high" || level == "max"
		if effort[level].(map[string]bool)["supported"] != want {
			t.Fatalf("incorrect GLM effort %s: %#v", level, effort)
		}
	}
	for _, level := range modelReasoningLevels(model) {
		payload := map[string]any{}
		applyReasoningControl(payload, map[string]any{"output_config": map[string]any{"effort": level}}, routeRuntime{Model: model}, "chat")
		if payload["reasoning_effort"] != level {
			t.Fatalf("GLM effort lost in Messages to Chat translation: %#v", payload)
		}
	}
}
