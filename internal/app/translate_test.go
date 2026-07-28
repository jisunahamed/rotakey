package app

import (
	"encoding/json"
	"testing"
)

func TestTranslateResponsesRequest(t *testing.T) {
	source := map[string]any{
		"input":             "hello",
		"instructions":      "be concise",
		"max_output_tokens": float64(128),
		"tools": []any{
			map[string]any{"type": "function", "name": "weather", "parameters": map[string]any{"type": "object"}},
		},
	}
	chat, err := translateResponsesRequest(source)
	if err != nil {
		t.Fatal(err)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	if numberAsInt64(chat["max_tokens"]) != 128 {
		t.Fatalf("unexpected max_tokens %#v", chat["max_tokens"])
	}
}

func TestTranslateResponsesRejectsHostedTools(t *testing.T) {
	source := map[string]any{
		"input": "hello",
		"tools": []any{map[string]any{"type": "web_search_preview"}},
	}
	if _, err := translateResponsesRequest(source); err == nil {
		t.Fatal("expected hosted tool to be rejected")
	}
}

func TestTranslateChatResponse(t *testing.T) {
	source := map[string]any{
		"id": "chat_1",
		"choices": []any{map[string]any{
			"message": map[string]any{"role": "assistant", "content": "hello"},
		}},
		"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2},
	}
	body, _ := json.Marshal(source)
	translated, input, output, err := translateChatResponse(body, "demo/model")
	if err != nil {
		t.Fatal(err)
	}
	if input != 3 || output != 2 {
		t.Fatalf("unexpected usage %d/%d", input, output)
	}
	var response map[string]any
	if json.Unmarshal(translated, &response) != nil || response["model"] != "demo/model" {
		t.Fatal("translated response has wrong model")
	}
}
