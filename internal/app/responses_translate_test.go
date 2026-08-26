package app

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// unreachableRedis stands in for the compatibility-learning store in tests that
// only care about translation. Every command fails fast, which is the same thing
// the gateway sees during a Redis outage, so these tests double as the guard that
// a plan is still built when learning is unavailable.
func unreachableRedis() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
}

// TestChatRequestReachesResponsesOnlyUpstream is the core of the promise that a
// caller's protocol is never a dead end: a route whose provider publishes only
// /responses still answers a Chat Completions call.
func TestChatRequestReachesResponsesOnlyUpstream(t *testing.T) {
	server := &Server{redis: unreachableRedis()}
	defer server.redis.Close()
	route := routeRuntime{
		Provider: Provider{APIFormat: "openai"},
		Model:    ModelRoute{ID: "mdl_1", SupportsResponses: true, UpstreamModel: "gpt-upstream", DefaultMaxOutputTokens: 1024},
	}
	public := map[string]any{
		"model":    "public/alias",
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}
	plan, err := server.buildPlan(context.Background(), dispatchRequest{
		PublicMode: messageModeChat, Alias: "public/alias", Public: public,
		Raw: mustJSON(public),
	}, route, dispatchState{})
	if err != nil {
		t.Fatalf("a Responses-only route refused a Chat caller: %v", err)
	}
	if plan.Path != "/responses" || !plan.Translated || plan.wireEndpoint() != "responses" {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Payload["model"] != "gpt-upstream" {
		t.Fatalf("upstream model = %#v", plan.Payload["model"])
	}
	input, _ := plan.Payload["input"].([]any)
	if len(input) != 1 {
		t.Fatalf("translated input = %#v", plan.Payload["input"])
	}
	if _, leaked := plan.Payload["messages"]; leaked {
		t.Fatalf("Chat messages leaked to a Responses upstream: %#v", plan.Payload)
	}
}

// TestAnthropicRequestReachesResponsesOnlyUpstream covers the longest path in the
// gateway: Messages in, through the Chat shape, into Responses.
func TestAnthropicRequestReachesResponsesOnlyUpstream(t *testing.T) {
	server := &Server{redis: unreachableRedis()}
	defer server.redis.Close()
	route := routeRuntime{
		Provider: Provider{APIFormat: "openai"},
		Model:    ModelRoute{ID: "mdl_2", SupportsResponses: true, UpstreamModel: "gpt-upstream", DefaultMaxOutputTokens: 512},
	}
	public := map[string]any{
		"model": "public/alias", "max_tokens": json.Number("64"),
		"system":   "Be brief.",
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}
	plan, err := server.buildPlan(context.Background(), dispatchRequest{
		PublicMode: messageModeAnthropic, Alias: "public/alias", Public: public,
		Raw: mustJSON(public),
	}, route, dispatchState{})
	if err != nil {
		t.Fatalf("a Responses-only route refused an Anthropic caller: %v", err)
	}
	if plan.Path != "/responses" || !plan.Translated {
		t.Fatalf("plan = %#v", plan)
	}
	if plan.Payload["instructions"] != "Be brief." {
		t.Fatalf("system prompt did not survive: %#v", plan.Payload)
	}
	if numberAsInt64(plan.Payload["max_output_tokens"]) != 64 {
		t.Fatalf("output cap did not survive: %#v", plan.Payload)
	}
}

// TestNativeResponsesUnavailableFallsBackToChat pins the automatic recovery the
// user asked for: once a provider has answered 404 at /responses, later attempts
// translate down to Chat rather than asking again.
func TestNativeResponsesUnavailableFallsBackToChat(t *testing.T) {
	server := &Server{redis: unreachableRedis()}
	defer server.redis.Close()
	route := routeRuntime{
		Provider: Provider{APIFormat: "openai"},
		Model:    ModelRoute{ID: "mdl_3", SupportsResponses: true, SupportsChat: true, UpstreamModel: "gpt-upstream", DefaultMaxOutputTokens: 256},
	}
	public := map[string]any{"model": "public/alias", "input": "Hello"}
	req := dispatchRequest{PublicMode: messageModeResponses, Alias: "public/alias", Public: public, Raw: mustJSON(public)}

	native, err := server.buildPlan(context.Background(), req, route, dispatchState{})
	if err != nil || native.Path != "/responses" {
		t.Fatalf("native plan = %#v, %v", native, err)
	}
	state := dispatchState{NativeResponsesUnavailable: map[string]bool{"mdl_3": true}}
	fallback, err := server.buildPlan(context.Background(), req, route, state)
	if err != nil {
		t.Fatalf("fallback plan failed: %v", err)
	}
	if fallback.Path != "/chat/completions" || !fallback.Translated {
		t.Fatalf("fallback plan = %#v", fallback)
	}
	if _, translated := fallback.Payload["messages"]; !translated {
		t.Fatalf("Responses input was not translated to Chat messages: %#v", fallback.Payload)
	}
}

func TestTranslateChatRequestToResponsesCoversToolsAndStructuredOutput(t *testing.T) {
	translated, dropped, err := translateChatRequestToResponses(map[string]any{
		"model": "gpt-upstream",
		"messages": []any{
			map[string]any{"role": "system", "content": "First rule."},
			map[string]any{"role": "developer", "content": "Second rule."},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Look at this"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/a.png"}},
			}},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "lookup", "arguments": `{"id":1}`},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "done"},
		},
		"max_completion_tokens": json.Number("256"),
		"reasoning_effort":      "high",
		"tools": []any{
			map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "parameters": map[string]any{"type": "object"}}},
			map[string]any{"type": "custom", "custom": map[string]any{"name": "unsupported"}},
		},
		"response_format": map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "answer", "schema": map[string]any{"type": "object"}, "strict": true,
		}},
		"logit_bias": map[string]any{"1": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Both system turns are kept, because a caller that splits its prompt across
	// messages means every line of it.
	if translated["instructions"] != "First rule.\nSecond rule." {
		t.Fatalf("instructions = %#v", translated["instructions"])
	}
	items, _ := translated["input"].([]any)
	if len(items) != 3 {
		t.Fatalf("translated input = %#v", translated["input"])
	}
	call, _ := items[1].(map[string]any)
	if call["type"] != "function_call" || call["call_id"] != "call_1" {
		t.Fatalf("tool call item = %#v", call)
	}
	result, _ := items[2].(map[string]any)
	if result["type"] != "function_call_output" || result["output"] != "done" {
		t.Fatalf("tool result item = %#v", result)
	}
	if numberAsInt64(translated["max_output_tokens"]) != 256 {
		t.Fatalf("output cap = %#v", translated["max_output_tokens"])
	}
	reasoning, _ := translated["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("reasoning = %#v", translated["reasoning"])
	}
	tools, _ := translated["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %#v", translated["tools"])
	}
	text, _ := translated["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "answer" {
		t.Fatalf("structured output = %#v", translated["text"])
	}
	for _, want := range []string{"logit_bias", "tools"} {
		if !slices.Contains(dropped, want) {
			t.Fatalf("%q was dropped without being reported: %#v", want, dropped)
		}
	}
}

func TestTranslateResponsesResponseToChatRecoversTextAndTools(t *testing.T) {
	body := []byte(`{"id":"resp_1","object":"response","status":"completed","model":"gpt-upstream",
		"output":[
			{"type":"reasoning","summary":[]},
			{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello "},{"type":"output_text","text":"world"}]},
			{"id":"fc_1","call_id":"call_1","type":"function_call","name":"lookup","arguments":"{}"}
		],
		"usage":{"input_tokens":11,"output_tokens":3}}`)
	translated, input, output, err := translateResponsesResponseToChat(body, "public/alias")
	if err != nil {
		t.Fatal(err)
	}
	if input != 11 || output != 3 {
		t.Fatalf("usage = %d/%d", input, output)
	}
	var chat map[string]any
	if err := json.Unmarshal(translated, &chat); err != nil {
		t.Fatal(err)
	}
	if chat["object"] != "chat.completion" || chat["model"] != "public/alias" {
		t.Fatalf("envelope = %s", translated)
	}
	choices, _ := chat["choices"].([]any)
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "Hello world" {
		t.Fatalf("content = %#v", message["content"])
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish reason = %#v", choice["finish_reason"])
	}
	calls, _ := message["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool calls = %#v", message["tool_calls"])
	}
}

// TestTranslateResponsesResponseKeepsChatEnvelope covers the gateways that answer
// /responses with a Chat body: relabelling it beats walking it for items it can
// never contain.
func TestTranslateResponsesResponseKeepsChatEnvelope(t *testing.T) {
	body := []byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`)
	translated, input, output, err := translateResponsesResponseToChat(body, "public/alias")
	if err != nil || input != 4 || output != 1 {
		t.Fatalf("usage = %d/%d, %v", input, output, err)
	}
	if !strings.Contains(string(translated), `"model":"public/alias"`) || !strings.Contains(string(translated), `"content":"hi"`) {
		t.Fatalf("relabelled envelope = %s", translated)
	}
}

func TestTranslateResponsesResponseToAnthropicShapesMessage(t *testing.T) {
	body := []byte(`{"id":"resp_1","object":"response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":5,"output_tokens":2}}`)
	translated, input, output, err := translateResponsesResponseToAnthropic(body, "public/alias")
	if err != nil || input != 5 || output != 2 {
		t.Fatalf("usage = %d/%d, %v", input, output, err)
	}
	var message map[string]any
	if err := json.Unmarshal(translated, &message); err != nil {
		t.Fatal(err)
	}
	if message["type"] != "message" || message["model"] != "public/alias" {
		t.Fatalf("Anthropic envelope = %s", translated)
	}
}

func TestTranslateResponsesStreamToChatEmitsTextToolsAndUsage(t *testing.T) {
	source := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_1"}}`,
		`data: {"type":"response.output_text.delta","delta":"Hello "}`,
		`data: {"type":"response.output_text.delta","delta":"world"}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"id":"fc_1","call_id":"call_1","type":"function_call","name":"lookup"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc_1","call_id":"call_1","delta":"{\"q\":1}"}`,
		`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":9,"output_tokens":4}}}`,
		"",
	}, "\n\n")
	var output bytes.Buffer
	code, err := translateResponsesStreamToChat(strings.NewReader(source), &output, "public/alias", nil, true)
	if err != nil || code != "" {
		t.Fatalf("stream result = %q, %v", code, err)
	}
	rendered := output.String()
	for _, want := range []string{
		`"content":"Hello "`, `"content":"world"`, `"name":"lookup"`,
		`"finish_reason":"tool_calls"`, `"prompt_tokens":9`, `"completion_tokens":4`, "data: [DONE]",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("stream is missing %s:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, `"role":"assistant"`) != 1 {
		t.Fatalf("assistant role was announced more than once:\n%s", rendered)
	}
}

// TestTranslateResponsesStreamToChatReportsInterruption is the guard against a
// silent truncation: a stream that dies before any content must be reported, or a
// client would treat an empty answer as a complete one.
func TestTranslateResponsesStreamToChatReportsInterruption(t *testing.T) {
	var output bytes.Buffer
	code, err := translateResponsesStreamToChat(
		strings.NewReader(`data: {"type":"response.created","response":{"id":"resp_1"}}`+"\n\n"),
		&output, "public/alias", nil, false,
	)
	if err == nil || code != "stream_interrupted" {
		t.Fatalf("interrupted stream = %q, %v", code, err)
	}
	if !strings.Contains(output.String(), "ended before completion") {
		t.Fatalf("stream output = %s", output.String())
	}
}

func TestTranslateResponsesStreamToChatSurfacesUpstreamFailure(t *testing.T) {
	source := `data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"slow down"}}}` + "\n\n"
	var output bytes.Buffer
	code, err := translateResponsesStreamToChat(strings.NewReader(source), &output, "public/alias", nil, false)
	if err != nil || code != "rate_limit_exceeded" {
		t.Fatalf("failed stream = %q, %v", code, err)
	}
	if !strings.Contains(output.String(), "slow down") {
		t.Fatalf("stream output = %s", output.String())
	}
}

func TestTranslateResponsesStreamToAnthropicEmitsMessageLifecycle(t *testing.T) {
	source := strings.Join([]string{
		`data: {"type":"response.output_text.delta","delta":"Hi"}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":3,"output_tokens":1}}}`,
		"",
	}, "\n\n")
	var output bytes.Buffer
	code, err := translateResponsesStreamToAnthropic(strings.NewReader(source), &output, "public/alias", nil)
	if err != nil || code != "" {
		t.Fatalf("stream result = %q, %v", code, err)
	}
	rendered := output.String()
	for _, want := range []string{
		"event: message_start", `"model":"public/alias"`, "event: content_block_delta",
		`"text":"Hi"`, "event: content_block_stop", "event: message_delta", "event: message_stop",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("stream is missing %s:\n%s", want, rendered)
		}
	}
}
