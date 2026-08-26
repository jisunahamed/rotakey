package app

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
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
	chat, _, err := translateResponsesRequest(source)
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

func TestTranslateResponsesRequestAcceptsOutputTextHistory(t *testing.T) {
	source := map[string]any{
		"input": []any{
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": "previous answer"},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "input_text", "text": "continue"},
				},
			},
		},
	}
	chat, _, err := translateResponsesRequest(source)
	if err != nil {
		t.Fatal(err)
	}
	messages := chat["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(messages))
	}
	assistant := messages[0].(map[string]any)
	// A turn carrying one text part collapses to a plain content string, which
	// every Chat implementation accepts, where a one-element array is refused by
	// some of the stricter OpenAI-compatible upstreams.
	if assistant["role"] != "assistant" || assistant["content"] != "previous answer" {
		t.Fatalf("unexpected translated assistant history: %#v", assistant)
	}
	user := messages[1].(map[string]any)
	if user["role"] != "user" || user["content"] != "continue" {
		t.Fatalf("unexpected translated user turn: %#v", user)
	}
}
func TestTranslateChatStreamCompletesTextAndToolLifecycle(t *testing.T) {
	source := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello "}}]}`,
		`data: {"choices":[{"delta":{"content":"world","tool_calls":[{"index":0,"id":"call_1","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"x\"}"}}]}}]}`,
		`data: [DONE]`,
		"",
	}, "\n")
	var output bytes.Buffer
	if err := translateChatStream(strings.NewReader(source), &output, "demo/model", nil); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.function_call_arguments.done", "response.completed", "data: [DONE]",
	} {
		if !strings.Contains(output.String(), event) {
			t.Fatalf("stream is missing %q:\n%s", event, output.String())
		}
	}
	if !strings.Contains(output.String(), `"sequence_number":1`) {
		t.Fatal("stream events do not carry increasing sequence numbers")
	}
}

func TestTranslateChatStreamAcceptsTerminalFinishReasonWithoutDone(t *testing.T) {
	source := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"complete"}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"",
	}, "\n")
	var output bytes.Buffer
	if err := translateChatStream(strings.NewReader(source), &output, "demo/model", nil); err != nil {
		t.Fatalf("terminal finish_reason should complete stream: %v", err)
	}
	if !strings.Contains(output.String(), "response.completed") || !strings.Contains(output.String(), "data: [DONE]") {
		t.Fatalf("terminal completion was not emitted:\n%s", output.String())
	}
}

func TestTranslateChatStreamRejectsInterruptedAndMalformedInput(t *testing.T) {
	for name, source := range map[string]string{
		"interrupted": `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n",
		"malformed":   "data: {not-json}\n\ndata: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := translateChatStream(strings.NewReader(source), &output, "demo/model", nil); err == nil {
				t.Fatal("expected stream translation to fail")
			}
		})
	}
}

// TestTranslateResponsesDropsHostedToolsWithoutFailing covers the rule that a
// hosted tool nobody but OpenAI can run is discarded by name while the rest of
// the request continues, so a Responses caller on a third-party route still gets
// an answer instead of a rejection.
func TestTranslateResponsesDropsHostedToolsWithoutFailing(t *testing.T) {
	source := map[string]any{
		"input": "hello",
		"tools": []any{
			map[string]any{"type": "web_search_preview"},
			map[string]any{"type": "function", "name": "weather", "parameters": map[string]any{"type": "object"}},
		},
	}
	chat, dropped, err := translateResponsesRequest(source)
	if err != nil {
		t.Fatalf("hosted tool refused the request: %v", err)
	}
	tools, _ := chat["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("surviving tools = %#v", chat["tools"])
	}
	if !slices.Contains(dropped, "hosted tool web_search_preview") {
		t.Fatalf("hosted tool was dropped without being reported: %#v", dropped)
	}
}

// TestTranslateResponsesDropsToolChoiceWithoutTools pins the pairing rule: some
// upstreams reject tool_choice when no tools survived translation.
func TestTranslateResponsesDropsToolChoiceWithoutTools(t *testing.T) {
	chat, _, err := translateResponsesRequest(map[string]any{
		"input":       "hello",
		"tools":       []any{map[string]any{"type": "web_search_preview"}},
		"tool_choice": "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := chat["tool_choice"]; leaked {
		t.Fatalf("tool_choice survived without tools: %#v", chat)
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
