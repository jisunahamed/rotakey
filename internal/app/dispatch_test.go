package app

import (
	"encoding/json"
	"testing"
)

// TestTranslateUpstreamResponseCoversEveryPoolCombination is the correctness
// guard for a pooled alias that spans API formats: whichever provider serves the
// attempt, the caller must receive its own protocol's shape.
func TestTranslateUpstreamResponseCoversEveryPoolCombination(t *testing.T) {
	anthropicBody := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`)
	chatBody := []byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)

	cases := []struct {
		name       string
		mode       string
		plan       upstreamPlan
		body       []byte
		wantObject string
		wantType   string
	}{
		{
			name: "anthropic upstream to anthropic caller",
			mode: messageModeAnthropic, plan: upstreamPlan{Format: "anthropic"},
			body: anthropicBody, wantType: "message",
		},
		{
			name: "anthropic upstream to chat caller",
			mode: messageModeChat, plan: upstreamPlan{Format: "anthropic", Translated: true},
			body: anthropicBody, wantObject: "chat.completion",
		},
		{
			name: "anthropic upstream to responses caller",
			mode: messageModeResponses, plan: upstreamPlan{Format: "anthropic", Translated: true},
			body: anthropicBody, wantObject: "response",
		},
		{
			name: "openai upstream to anthropic caller",
			mode: messageModeAnthropic, plan: upstreamPlan{Format: "openai", Translated: true},
			body: chatBody, wantType: "message",
		},
		{
			name: "openai upstream to responses caller via translation",
			mode: messageModeResponses, plan: upstreamPlan{Format: "openai", Translated: true},
			body: chatBody, wantObject: "response",
		},
		{
			name: "openai upstream to chat caller passes through",
			mode: messageModeChat, plan: upstreamPlan{Format: "openai"},
			body: chatBody, wantObject: "chat.completion",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			req := dispatchRequest{PublicMode: test.mode, Alias: "opus-5"}
			body, input, output, err := translateUpstreamResponse(req, test.plan, test.body)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("translated body is not JSON: %v\n%s", err, body)
			}
			if test.wantObject != "" && payload["object"] != test.wantObject {
				t.Fatalf("object = %v, want %q\n%s", payload["object"], test.wantObject, body)
			}
			if test.wantType != "" && payload["type"] != test.wantType {
				t.Fatalf("type = %v, want %q\n%s", payload["type"], test.wantType, body)
			}
			// The caller must always see the public alias, never the provider's
			// own upstream model name.
			if payload["model"] != "opus-5" {
				t.Fatalf("model = %v, want the public alias\n%s", payload["model"], body)
			}
			if input != 11 || output != 3 {
				t.Fatalf("usage = %d/%d, want 11/3", input, output)
			}
		})
	}
}

func TestRouteSupportsRequestFiltersUnusableProviders(t *testing.T) {
	chatOnly := routeRuntime{
		Model:    ModelRoute{SupportsChat: true},
		Provider: Provider{APIFormat: "openai"},
	}
	if routeSupportsRequest(chatOnly, dispatchRequest{PublicMode: messageModeAnthropic}) {
		t.Fatal("a route without Messages was offered to an Anthropic caller")
	}
	if !routeSupportsRequest(chatOnly, dispatchRequest{PublicMode: messageModeChat}) {
		t.Fatal("a chat route was rejected for a chat caller")
	}
	// Responses is served either natively or by translating to chat, so a
	// chat-only route still qualifies.
	if !routeSupportsRequest(chatOnly, dispatchRequest{PublicMode: messageModeResponses}) {
		t.Fatal("a chat route was rejected for a Responses caller")
	}
	messagesOnly := routeRuntime{
		Model:    ModelRoute{SupportsMessages: true},
		Provider: Provider{APIFormat: "anthropic"},
	}
	if routeSupportsRequest(messagesOnly, dispatchRequest{PublicMode: messageModeChat}) {
		t.Fatal("a route without Chat was offered to a chat caller")
	}
	if !routeSupportsRequest(messagesOnly, dispatchRequest{PublicMode: messageModeAnthropic}) {
		t.Fatal("a Messages route was rejected for an Anthropic caller")
	}
}

func TestWireEndpointIsKeyedByUpstreamShape(t *testing.T) {
	if got := (upstreamPlan{Path: "/responses"}).wireEndpoint(); got != "responses" {
		t.Fatalf("wire endpoint = %q", got)
	}
	for _, path := range []string{"/chat/completions", "/messages"} {
		if got := (upstreamPlan{Path: path}).wireEndpoint(); got != "chat" {
			t.Fatalf("wire endpoint for %s = %q, want chat", path, got)
		}
	}
}
