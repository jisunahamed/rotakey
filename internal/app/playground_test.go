package app

import "testing"

func TestValidatePlaygroundInputDefaults(t *testing.T) {
	input := playgroundInput{Model: "  demo/model  ", Prompt: "  hello  "}
	if err := validatePlaygroundInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Model != "demo/model" || input.Prompt != "hello" {
		t.Fatalf("trimmed input = %#v", input)
	}
	if input.Protocol != playgroundProtocolAuto || input.MaxTokens != 1024 {
		t.Fatalf("defaults = protocol %q, max tokens %d", input.Protocol, input.MaxTokens)
	}
}

func TestValidatePlaygroundInputRejectsInvalidOptions(t *testing.T) {
	temperature := 2.1
	tests := []playgroundInput{
		{Prompt: "hello"},
		{Model: "demo/model"},
		{Model: "demo/model", Prompt: "hello", Protocol: "unknown"},
		{Model: "demo/model", Prompt: "hello", Temperature: &temperature},
	}
	for _, input := range tests {
		if err := validatePlaygroundInput(&input); err == nil {
			t.Fatalf("validatePlaygroundInput(%#v) succeeded", input)
		}
	}
}

func TestPlaygroundPayloadUsesProtocolShape(t *testing.T) {
	input := playgroundInput{Model: "demo/model", Prompt: "hello", System: "be concise", MaxTokens: 321}
	chat := playgroundPayload(input, playgroundProtocolChat)
	if chat["messages"] == nil || chat["max_tokens"] != 321 {
		t.Fatalf("chat payload = %#v", chat)
	}
	responses := playgroundPayload(input, playgroundProtocolResponses)
	if responses["input"] != "hello" || responses["instructions"] != "be concise" || responses["max_output_tokens"] != 321 {
		t.Fatalf("responses payload = %#v", responses)
	}
	if responses["messages"] != nil {
		t.Fatalf("responses payload contains messages: %#v", responses)
	}
	messages := playgroundPayload(input, playgroundProtocolMessages)
	if messages["system"] != "be concise" {
		t.Fatalf("messages payload system = %#v", messages["system"])
	}
	items, ok := messages["messages"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["role"] != "user" {
		t.Fatalf("messages payload messages = %#v", messages["messages"])
	}
}
