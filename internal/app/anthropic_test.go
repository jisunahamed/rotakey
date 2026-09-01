package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestTranslateAnthropicRequestToChatCoreSurface(t *testing.T) {
	source := map[string]any{
		"model": "claude", "max_tokens": json.Number("128"),
		"system": "Be concise.",
		"messages": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "text", "text": "Hello"},
				map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "AAAA"}},
			}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": "tool_1", "name": "weather", "input": map[string]any{"city": "Dhaka"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "tool_1", "content": "Sunny"},
			}},
		},
		"tools": []any{map[string]any{"name": "weather", "description": "Weather", "input_schema": map[string]any{"type": "object"}}},
	}
	chat, _, err := translateAnthropicRequestToChat(source)
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := chat["messages"].([]any)
	if len(messages) != 4 || numberAsInt64(chat["max_tokens"]) != 128 {
		t.Fatalf("translated chat = %#v", chat)
	}
	toolMessage, _ := messages[3].(map[string]any)
	if toolMessage["role"] != "tool" || toolMessage["tool_call_id"] != "tool_1" {
		t.Fatalf("tool result = %#v", toolMessage)
	}
}

// TestAnthropicOnlyFeaturesDegradeInsteadOfFailing pins the contract that
// replaced the old cross-protocol refusal: an Anthropic-only extra such as a
// citations hint is stripped from the block and the conversation still reaches
// the model, because losing one convenience beats losing the whole request.
func TestAnthropicOnlyFeaturesDegradeInsteadOfFailing(t *testing.T) {
	chat, _, err := translateAnthropicRequestToChat(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "Hi", "citations": map[string]any{"enabled": true}},
		}}},
	})
	if err != nil {
		t.Fatalf("Anthropic-only block was refused: %v", err)
	}
	messages, _ := chat["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("translated messages = %#v", chat)
	}
	message, _ := messages[0].(map[string]any)
	if message["content"] != "Hi" {
		t.Fatalf("citation hint changed the text: %#v", message)
	}
	if strings.Contains(string(mustJSON(chat)), "citations") {
		t.Fatalf("citation hint leaked to the OpenAI upstream: %#v", chat)
	}
}

func TestTranslateChatRequestToAnthropicTools(t *testing.T) {
	source := map[string]any{
		"max_completion_tokens": json.Number("64"),
		"messages": []any{
			map[string]any{"role": "system", "content": "Use tools."},
			map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function", "function": map[string]any{"name": "lookup", "arguments": `{"id":1}`},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "done"},
		},
	}
	message, _, err := translateChatRequestToAnthropic(source)
	if err != nil {
		t.Fatal(err)
	}
	if message["system"] != "Use tools." || numberAsInt64(message["max_tokens"]) != 64 {
		t.Fatalf("translated message = %#v", message)
	}
}

func TestAnthropicMetadataAndCacheHintsAreIgnoredCrossProtocol(t *testing.T) {
	chat, _, err := translateAnthropicRequestToChat(map[string]any{
		"messages":           []any{map[string]any{"role": "user", "content": "Hi"}},
		"metadata":           map[string]any{"user_id": "u1"},
		"thinking":           map[string]any{"type": "enabled", "budget_tokens": 1024},
		"context_management": map[string]any{"edits": []any{}},
		"output_config":      map[string]any{"effort": "medium"},
	})
	if err != nil {
		t.Fatalf("Claude Code control-field translation failed: %v", err)
	}
	for _, field := range []string{"metadata", "thinking", "context_management", "output_config"} {
		if _, exists := chat[field]; exists {
			t.Fatalf("%s leaked into OpenAI request: %#v", field, chat)
		}
	}
	chat, _, err = translateAnthropicRequestToChat(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "text", "text": "Hi", "cache_control": map[string]any{"type": "ephemeral"},
		}}}},
	})
	if err != nil {
		t.Fatalf("cache hint translation failed: %v", err)
	}
	if _, exists := chat["cache_control"]; exists {
		t.Fatalf("cache hint leaked into OpenAI request: %#v", chat)
	}
	// reasoning_effort has no Messages equivalent, so it is now named in the
	// dropped list and the request continues instead of failing.
	message, dropped, err := translateChatRequestToAnthropic(map[string]any{
		"messages":         []any{map[string]any{"role": "user", "content": "Hi"}},
		"reasoning_effort": "high",
	})
	if err != nil {
		t.Fatalf("OpenAI reasoning field refused the request: %v", err)
	}
	if _, leaked := message["reasoning_effort"]; leaked {
		t.Fatalf("reasoning_effort leaked to the Anthropic upstream: %#v", message)
	}
	if !slices.Contains(dropped, "reasoning_effort") {
		t.Fatalf("reasoning_effort was dropped without being reported: %#v", dropped)
	}
}

func TestTranslateClaudeCodeHistoryRolesToChat(t *testing.T) {
	chat, _, err := translateAnthropicRequestToChat(map[string]any{
		"messages": []any{
			map[string]any{"role": "developer", "content": "Be concise."},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "Hidden reasoning"},
				map[string]any{"type": "text", "text": "I need a tool."},
			}},
			map[string]any{"role": "tool", "tool_use_id": "call_1", "content": []any{
				map[string]any{"type": "text", "text": "Done"},
				map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://example.com/result.png"}},
			}},
			map[string]any{"role": "user", "content": "Continue"},
		},
	})
	if err != nil {
		t.Fatalf("Claude Code history translation failed: %v", err)
	}
	messages, _ := chat["messages"].([]any)
	if len(messages) != 4 {
		t.Fatalf("translated history = %#v", chat)
	}
	for index, want := range []string{"system", "assistant", "tool", "user"} {
		message, _ := messages[index].(map[string]any)
		if message["role"] != want {
			t.Fatalf("message %d = %#v, want role %q", index, message, want)
		}
	}
	tool, _ := messages[2].(map[string]any)
	if tool["tool_call_id"] != "call_1" {
		t.Fatalf("tool message = %#v", tool)
	}
	content, _ := tool["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("tool content = %#v", tool)
	}
}

func TestTranslateClaudeToolReferencesAsNoOp(t *testing.T) {
	chat, _, err := translateAnthropicRequestToChat(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{
			"type": "tool_result", "tool_use_id": "tool_search_1", "content": []any{
				map[string]any{"type": "tool_reference", "tool_name": "lookup_weather"},
			},
		}}}},
	})
	if err != nil {
		t.Fatalf("tool reference translation failed: %v", err)
	}
	messages, _ := chat["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("translated tool reference = %#v", chat)
	}
	tool, _ := messages[0].(map[string]any)
	if tool["role"] != "tool" || tool["tool_call_id"] != "tool_search_1" || tool["content"] != "" {
		t.Fatalf("tool reference result = %#v", tool)
	}
}

func TestParallelToolSettingTranslatesToAnthropic(t *testing.T) {
	message, _, err := translateChatRequestToAnthropic(map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "lookup", "parameters": map[string]any{"type": "object"},
		}}},
		"parallel_tool_calls": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	choice, _ := message["tool_choice"].(map[string]any)
	if choice["type"] != "auto" || choice["disable_parallel_tool_use"] != true {
		t.Fatalf("tool choice = %#v", choice)
	}
	// Anthropic rejects tool_choice without tools, so the setting is dropped with
	// the tool list it depended on rather than sent alone.
	bare, _, err := translateChatRequestToAnthropic(map[string]any{
		"messages":            []any{map[string]any{"role": "user", "content": "Hi"}},
		"parallel_tool_calls": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := bare["tool_choice"]; leaked {
		t.Fatalf("tool_choice survived without tools: %#v", bare)
	}
}

func TestOpenAIStreamOptionsTranslateToAnthropic(t *testing.T) {
	message, _, err := translateChatRequestToAnthropic(map[string]any{
		"messages":       []any{map[string]any{"role": "user", "content": "Hi"}},
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, leaked := message["stream_options"]; leaked {
		t.Fatal("OpenAI stream_options leaked to Anthropic upstream")
	}
}

func TestAnthropicStreamEmulatesOpenAIUsageChunk(t *testing.T) {
	source := strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":2,\"output_tokens\":1}}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":4}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	destination := &bytes.Buffer{}
	if _, err := translateAnthropicStreamToOpenAI(source, destination, "public/model", nil, true, nil); err != nil {
		t.Fatal(err)
	}
	output := destination.String()
	if !strings.Contains(output, `"choices":[],"created"`) || !strings.Contains(output, `"completion_tokens":4`) || !strings.Contains(output, `"prompt_tokens":12`) || !strings.Contains(output, "data: [DONE]") {
		t.Fatalf("translated stream = %s", output)
	}
}

func TestAnthropicStreamNormalizesCompatibleVariantsAndTracksUsage(t *testing.T) {
	source := strings.NewReader("  data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n  data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"Hello\"}}\n\n  data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Reason\"}}\n\n  data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n  data: {\"type\":\"message_stop\"}\n\n")
	destination := &bytes.Buffer{}
	stats := &anthropicStreamStats{}
	code, err := translateAnthropicStreamToOpenAI(source, destination, "public/model", nil, true, stats)
	if err != nil || code != "" {
		t.Fatalf("stream result = %q, %v", code, err)
	}
	output := destination.String()
	if !strings.Contains(output, `"content":"Hello"`) || !strings.Contains(output, `"reasoning_content":"Reason"`) {
		t.Fatalf("translated variants = %s", output)
	}
	if stats.InputTokens != 7 || stats.OutputTokens != 3 || stats.ContentParts != 2 || !stats.SawStop {
		t.Fatalf("stream stats = %#v", stats)
	}
}

func TestAnthropicStreamRejectsEmptySuccess(t *testing.T) {
	source := strings.NewReader("data: {\"type\":\"message_start\",\"message\":{}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
	destination := &bytes.Buffer{}
	code, err := translateAnthropicStreamToOpenAI(source, destination, "public/model", nil, false, nil)
	if err != nil || code != "upstream_stream_empty" || !strings.Contains(destination.String(), "completed without any text or tool output") {
		t.Fatalf("empty stream = %q, %v, %s", code, err, destination.String())
	}
}

func TestAnthropicResponseRejectsEmptyAndPreservesOpenAIEnvelope(t *testing.T) {
	// The line between refused and translated is the content field itself. A
	// body without one is not an Anthropic message — a proxy's own JSON — and
	// refusing it keeps a fabricated empty completion off the caller. A body
	// whose content yields nothing visible is a real reply a thinking model
	// produces by spending its whole budget inside its own head, and refusing
	// those manufactured production 502s out of billed 200s — see
	// thinking_response_test.go for that contract.
	if _, _, _, err := translateAnthropicResponseToChat([]byte(`{"type":"message"}`), "public/model"); err == nil {
		t.Fatal("a body with no content field was accepted")
	}
	if _, _, _, err := translateAnthropicResponseToChat([]byte(`{"type":"message","content":[]}`), "public/model"); err != nil {
		t.Fatalf("an empty reply was refused: %v", err)
	}
	input := []byte(`{"id":"chat_1","object":"chat.completion","model":"upstream","choices":[{"message":{"role":"assistant","content":"Hello"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`)
	output, in, out, err := translateAnthropicResponseToChat(input, "public/model")
	if err != nil || in != 2 || out != 1 || !strings.Contains(string(output), `"model":"public/model"`) || !strings.Contains(string(output), `"content":"Hello"`) {
		t.Fatalf("OpenAI envelope = %s, %d/%d, %v", output, in, out, err)
	}
}

func TestAnthropicJSONResponseCanBecomeOpenAIStream(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"upstream","content":[{"type":"text","text":"Hello"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":3,"output_tokens":2}}`)
	synthetic, err := anthropicJSONToSSE(body)
	if err != nil {
		t.Fatal(err)
	}
	destination := &bytes.Buffer{}
	if _, err := translateAnthropicStreamToOpenAI(bytes.NewReader(synthetic), destination, "public/model", nil, true, nil); err != nil {
		t.Fatal(err)
	}
	output := destination.String()
	if !strings.Contains(output, `"content":"Hello"`) || !strings.Contains(output, `"prompt_tokens":3`) || !strings.Contains(output, `"completion_tokens":2`) || !strings.HasSuffix(output, "data: [DONE]\n\n") {
		t.Fatalf("translated stream = %s", output)
	}
}

func TestAnthropicJSONStreamRepairRejectsEmptySuccess(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(`{}`), []byte(`{"type":"error"}`)} {
		if _, err := anthropicJSONToSSE(body); err == nil {
			t.Fatalf("invalid HTTP 200 body was accepted: %s", body)
		}
	}
}

func TestPrepareAnthropicSSERejectsBlankAndPreservesFirstEvent(t *testing.T) {
	if _, err := prepareAnthropicSSE(strings.NewReader("\n: keepalive\n")); err == nil {
		t.Fatal("blank SSE response was accepted")
	}
	source := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	prepared, err := prepareAnthropicSSE(strings.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(prepared)
	if err != nil || string(body) != source {
		t.Fatalf("prepared stream = %q, %v", body, err)
	}
}

func TestAnthropicUsageIncludesCacheTokens(t *testing.T) {
	input, output := extractAnthropicUsage(map[string]any{"usage": map[string]any{
		"input_tokens": json.Number("10"), "cache_creation_input_tokens": json.Number("20"),
		"cache_read_input_tokens": json.Number("30"), "output_tokens": json.Number("4"),
	}})
	if input != 60 || output != 4 {
		t.Fatalf("usage = %d/%d", input, output)
	}
}

func TestRewriteAnthropicStreamPreservesUnknownEvents(t *testing.T) {
	source := strings.NewReader("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"model\":\"upstream\"}}\n\nevent: future_event\ndata: {\"type\":\"future_event\",\"value\":1}\n\nevent: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}\n\n")
	destination := &bytes.Buffer{}
	code, err := rewriteAnthropicStream(source, destination, "public/alias", nil)
	if err != nil || code != "overloaded_error" {
		t.Fatalf("stream result = %q %v", code, err)
	}
	if !strings.Contains(destination.String(), `"model":"public/alias"`) || !strings.Contains(destination.String(), "future_event") {
		t.Fatalf("stream output = %s", destination.String())
	}
}

func TestAnthropicProviderInspectionSendsNativeHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "ant-key" || r.Header.Get("Anthropic-Version") != "2023-06-01" {
			t.Fatalf("headers = %#v", r.Header)
		}
		if r.URL.Path == "/messages" {
			_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"a"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-test","display_name":"Claude Test"}],"has_more":false}`))
	}))
	defer upstream.Close()
	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL, APIFormat: "anthropic", AnthropicVersion: "2023-06-01",
		AuthHeader: "X-Api-Key", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("ant-key"))
	if !result.Valid || !result.ProtocolVerified || !result.CatalogAvailable || len(result.Models) != 1 {
		t.Fatalf("inspection = %#v", result)
	}
}

func TestAnthropicCatalog305AllowsManualFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUseProxy) }))
	defer upstream.Close()
	result := inspectProviderSecret(context.Background(), Provider{
		BaseURL: upstream.URL, APIFormat: "anthropic", AuthHeader: "X-Api-Key",
		AnthropicVersion: "2023-06-01", TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}, []byte("ant-key"))
	if !result.Valid || result.CatalogAvailable || !strings.Contains(result.Warning, "manually") {
		t.Fatalf("inspection = %#v", result)
	}
}

func TestBatchConstraintsAggregateSharedAndPerModelLimits(t *testing.T) {
	sharedRPM, sharedTPM := int64(100), int64(1000)
	modelARPM, modelATPM, modelATPR := int64(2), int64(100), int64(60)
	modelBRPD, modelBTPR := int64(3), int64(20)
	credential := credentialRuntime{CredentialView: CredentialView{
		ID:     "cred_1",
		Limits: RatePolicy{RPM: &sharedRPM, TPM: &sharedTPM},
		ModelLimits: map[string]RatePolicy{
			"model_a": {RPM: &modelARPM, TPM: &modelATPM, TPR: &modelATPR},
			"model_b": {RPD: &modelBRPD, TPR: &modelBTPR},
		},
	}}
	costs := map[string]modelReservationCost{
		"model_a": {Requests: 2, Tokens: 90, TPR: 50},
		"model_b": {Requests: 3, Tokens: 50, TPR: 15},
	}
	constraints, rejected, totalTokens := buildBatchConstraints(credential, costs)
	if len(rejected) != 0 || totalTokens != 140 {
		t.Fatalf("batch reservation = rejected %#v, tokens %d", rejected, totalTokens)
	}
	wantCosts := map[string]int64{
		"limit:cred_1:all:rpm":           5,
		"limit:cred_1:all:tpm":           140,
		"limit:cred_1:model:model_a:rpm": 2,
		"limit:cred_1:model:model_a:tpm": 90,
		"limit:cred_1:model:model_b:rpd": 3,
	}
	for _, constraint := range constraints {
		if want, ok := wantCosts[constraint.Key]; ok {
			if constraint.Cost != want {
				t.Fatalf("%s cost = %d, want %d", constraint.Key, constraint.Cost, want)
			}
			delete(wantCosts, constraint.Key)
		}
	}
	if len(wantCosts) != 0 {
		t.Fatalf("missing constraints: %#v", wantCosts)
	}
	costs["model_b"] = modelReservationCost{Requests: 1, Tokens: 25, TPR: 25}
	_, rejected, _ = buildBatchConstraints(credential, costs)
	if len(rejected) != 1 || rejected[0].Scope != "model" || rejected[0].Dimension != "tpr" {
		t.Fatalf("model TPR rejection = %#v", rejected)
	}
}

func TestAnthropicResponseHeadersKeepGatewayRequestID(t *testing.T) {
	destination := http.Header{"Request-Id": []string{"req_gateway"}}
	source := http.Header{
		"Request-Id":                           []string{"req_upstream"},
		"Anthropic-Ratelimit-Tokens-Remaining": []string{"42"},
	}
	copyAnthropicHeaders(destination, source)
	if destination.Get("Request-Id") != "req_gateway" {
		t.Fatalf("public request id = %q", destination.Get("Request-Id"))
	}
	if destination.Get("Anthropic-Ratelimit-Tokens-Remaining") != "42" {
		t.Fatalf("rate header was not forwarded")
	}
}

func TestAnthropicDiscoveryModelIDsExposeEveryPublicAlias(t *testing.T) {
	for _, alias := range []string{"claude-opus", "anthropic/sonnet"} {
		if got := anthropicDiscoveryModelID(alias); got != alias {
			t.Fatalf("native-looking alias %q changed to %q", alias, got)
		}
	}

	alias := "nvidia/minimaxai/minimax-m3"
	discoveryID := anthropicDiscoveryModelID(alias)
	if !strings.HasPrefix(discoveryID, "claude") {
		t.Fatalf("discovery ID %q will be hidden by Claude Code", discoveryID)
	}
	if got := resolveAnthropicDiscoveryModelID(discoveryID); got != alias {
		t.Fatalf("resolved discovery ID = %q, want %q", got, alias)
	}
	invalid := "claude-rotakey-v1-not-valid!"
	if got := resolveAnthropicDiscoveryModelID(invalid); got != invalid {
		t.Fatalf("invalid bridge ID changed to %q", got)
	}
}

func TestGatewayHeaderParsingIsStrictAndCaseInsensitive(t *testing.T) {
	bearer, anthropicKey, err := gatewayKeysFromHeaders(http.Header{
		"Authorization": []string{"bearer gateway-key"},
		"X-Api-Key":     []string{"gateway-key"},
	})
	if err != nil || bearer != "gateway-key" || anthropicKey != "gateway-key" {
		t.Fatalf("parsed headers = %q/%q, %v", bearer, anthropicKey, err)
	}
	for _, value := range []string{"Basic gateway-key", "Bearer", "Bearer one two"} {
		if _, _, err := gatewayKeysFromHeaders(http.Header{"Authorization": []string{value}}); err == nil {
			t.Fatalf("malformed Authorization %q was accepted", value)
		}
	}
}

func TestProviderHeadersRejectHopByHopAndInvalidNames(t *testing.T) {
	base := providerInput{
		Name: "Test provider", BaseURL: "http://127.0.0.1:9000/v1",
		APIFormat: "openai", AuthHeader: "Authorization", AuthScheme: "Bearer",
		TimeoutSeconds: 5, AllowPrivateNetwork: true,
	}
	for _, header := range []string{"Host", "Content-Length", "Transfer-Encoding", "Bad Header"} {
		candidate := base
		candidate.AuthHeader = header
		if err := validateProviderInput(&candidate); err == nil {
			t.Fatalf("unsafe auth header %q was accepted", header)
		}
	}
	candidate := base
	candidate.ExtraHeaders = map[string]string{"Connection": "keep-alive"}
	if err := validateProviderInput(&candidate); err == nil {
		t.Fatal("hop-by-hop extra header was accepted")
	}
}
