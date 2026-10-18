package app

import (
	"context"
	"testing"
)

func TestNormalizeInvalidToolArguments(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{"role": "assistant", "tool_calls": []any{
				map[string]any{"function": map[string]any{"name": "broken", "arguments": `{"id":`}},
				map[string]any{"function": map[string]any{"name": "valid", "arguments": `{"id":1}`}},
			}},
		},
		"input": []any{
			map[string]any{"type": "function_call", "name": "fenced", "arguments": "```json\n{\"q\":\"x\"}\n```"},
			map[string]any{"type": "function_call", "name": "object", "arguments": map[string]any{"n": 2}},
		},
	}
	changed := normalizeInvalidToolArguments(payload)
	if len(changed) != 2 {
		t.Fatalf("changed paths = %#v", changed)
	}
	calls := payload["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
	if got := calls[0].(map[string]any)["function"].(map[string]any)["arguments"]; got != emptyToolArguments {
		t.Fatalf("malformed Chat arguments = %#v", got)
	}
	if got := calls[1].(map[string]any)["function"].(map[string]any)["arguments"]; got != `{"id":1}` {
		t.Fatalf("valid Chat arguments changed = %#v", got)
	}
	input := payload["input"].([]any)
	if got := input[0].(map[string]any)["arguments"]; got != `{"q":"x"}` {
		t.Fatalf("fenced Responses arguments = %#v", got)
	}
	if got := input[1].(map[string]any)["arguments"]; got != `{"n":2}` {
		t.Fatalf("object Responses arguments = %#v", got)
	}
}

func TestEveryOpenAIWireNormalizesReplayedToolArguments(t *testing.T) {
	chatPayload := func() map[string]any {
		return map[string]any{
			"model": "public/alias",
			"messages": []any{
				map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "lookup", "arguments": `{"id":`},
				}}},
				map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "done"},
			},
		}
	}
	responsesPayload := func() map[string]any {
		return map[string]any{
			"model": "public/alias",
			"input": []any{
				map[string]any{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": "not-json"},
				map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "done"},
			},
		}
	}
	tests := []struct {
		name, mode, wantPath, replacedPath string
		route                              ModelRoute
		payload                            func() map[string]any
	}{
		{"Chat to Chat", messageModeChat, "/chat/completions", "messages[].tool_calls[].function.arguments", ModelRoute{SupportsChat: true}, chatPayload},
		{"Chat to Responses", messageModeChat, "/responses", "input[].arguments", ModelRoute{SupportsResponses: true}, chatPayload},
		{"Responses to Responses", messageModeResponses, "/responses", "input[].arguments", ModelRoute{SupportsResponses: true}, responsesPayload},
		{"Responses to Chat", messageModeResponses, "/chat/completions", "messages[].tool_calls[].function.arguments", ModelRoute{SupportsChat: true}, responsesPayload},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{redis: unreachableRedis()}
			defer server.redis.Close()
			public := test.payload()
			test.route.ID = "mdl_arguments"
			test.route.UpstreamModel = "upstream"
			test.route.DefaultMaxOutputTokens = 128
			plan, err := server.buildPlan(context.Background(), dispatchRequest{
				PublicMode: test.mode, Public: public, Raw: mustJSON(public),
			}, routeRuntime{Provider: Provider{APIFormat: "openai"}, Model: test.route}, dispatchState{})
			if err != nil {
				t.Fatal(err)
			}
			if plan.Path != test.wantPath {
				t.Fatalf("path = %s", plan.Path)
			}
			if plan.Replaced[test.replacedPath] != emptyToolArguments {
				t.Fatalf("replacements = %#v", plan.Replaced)
			}
			if test.wantPath == "/responses" {
				items := plan.Payload["input"].([]any)
				if got := items[0].(map[string]any)["arguments"]; got != emptyToolArguments {
					t.Fatalf("Responses arguments = %#v", got)
				}
			} else {
				messages := plan.Payload["messages"].([]any)
				calls := messages[0].(map[string]any)["tool_calls"].([]any)
				if got := calls[0].(map[string]any)["function"].(map[string]any)["arguments"]; got != emptyToolArguments {
					t.Fatalf("Chat arguments = %#v", got)
				}
			}
		})
	}
}
