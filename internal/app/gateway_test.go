package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCopyStreamingResponseRequiresCompletion(t *testing.T) {
	for name, source := range map[string]string{
		"done":        "event: response.completed\ndata: {}\n\n",
		"done_marker": "data: [DONE]\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			if err := copyStreamingResponse(&flushingRecorder{body: output}, strings.NewReader(source), nil); err != nil {
				t.Fatal(err)
			}
		})
	}
	recorder := &flushingRecorder{}
	if err := copyStreamingResponse(recorder, strings.NewReader("event: response.output_text.delta\ndata: {}\n\n"), nil); err == nil {
		t.Fatal("interrupted native stream should fail")
	}
}

func TestWriteStreamFailureUsesEndpointProtocol(t *testing.T) {
	t.Run("chat", func(t *testing.T) {
		var output bytes.Buffer
		writeStreamFailure(&output, nil, "chat", "stream_interrupted", "ended early", "demo/model")
		body := output.String()
		if strings.Contains(body, "response.failed") {
			t.Fatalf("chat stream failure used Responses event:\n%s", body)
		}
		if !strings.Contains(body, `"error"`) || !strings.Contains(body, "data: [DONE]") {
			t.Fatalf("chat stream failure is not OpenAI-compatible:\n%s", body)
		}
	})

	t.Run("responses", func(t *testing.T) {
		var output bytes.Buffer
		writeStreamFailure(&output, nil, "responses", "stream_interrupted", "ended early", "demo/model")
		body := output.String()
		if !strings.Contains(body, "event: response.failed") {
			t.Fatalf("responses stream failure did not use Responses event:\n%s", body)
		}
		if !strings.Contains(body, `"object":"response"`) || !strings.Contains(body, `"model":"demo/model"`) {
			t.Fatalf("responses stream failure is missing response metadata:\n%s", body)
		}
	})
}

type flushingRecorder struct {
	header  http.Header
	body    bytes.Buffer
	flushes int
}

func (r *flushingRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *flushingRecorder) Write(body []byte) (int, error) {
	return r.body.Write(body)
}

func (r *flushingRecorder) WriteHeader(_ int) {}

func (r *flushingRecorder) Flush() {
	r.flushes++
}

func TestCopyStreamingResponseFlushesAndCaptures(t *testing.T) {
	destination := &flushingRecorder{}
	capture := &bytes.Buffer{}
	source := bytes.NewBufferString("data: one\n\ndata: two\n\ndata: [DONE]\n\n")
	if err := copyStreamingResponse(destination, source, capture); err != nil {
		t.Fatalf("copy stream: %v", err)
	}
	if destination.body.String() != sourceString(capture) {
		t.Fatalf("destination and capture differ: destination=%q capture=%q", destination.body.String(), capture.String())
	}
	if destination.flushes == 0 {
		t.Fatal("stream response was not flushed")
	}
}

func TestCopyStreamingResponseAllowsNoCapture(t *testing.T) {
	destination := &flushingRecorder{}
	if err := copyStreamingResponse(destination, bytes.NewBufferString("data: one\n\ndata: [DONE]\n\n"), nil); err != nil {
		t.Fatalf("copy stream without capture: %v", err)
	}
	if destination.body.Len() == 0 || destination.flushes == 0 {
		t.Fatal("stream was not delivered and flushed")
	}
}

func sourceString(buffer *bytes.Buffer) string {
	return buffer.String()
}

func TestUnsupportedCompatibilityParameters(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		payload map[string]any
		want    []string
	}{
		{
			name: "azure unrecognized argument",
			body: `{"error":{"message":"Unrecognized request argument supplied: thinking","type":"invalid_request_error","param":null,"code":"unrecognized_request_argument"}}`,
			payload: map[string]any{
				"model": "Kimi-K2.5", "thinking": map[string]any{"type": "enabled"},
			},
			want: []string{"thinking"},
		},
		{
			name: "validation unsupported parameter",
			body: `{"error":{"message":"Validation: Unsupported parameter(s): ` + "`" + `reasoning_effort` + "`" + `","code":"invalid_request"}}`,
			payload: map[string]any{
				"model": "some-model", "reasoning_effort": "high",
			},
			want: []string{"reasoning_effort"},
		},
		{
			name: "deprecated model temperature",
			body: `{"error":{"message":"` + "`" + `temperature` + "`" + ` is deprecated for this model.","type":"invalid_request_error","code":"invalid_request_error"}}`,
			payload: map[string]any{
				"model": "claude-sonnet-5", "temperature": 0.7,
			},
			want: []string{"temperature"},
		},
		{
			name: "model does not support seed",
			body: `{"error":{"message":"seed is not supported with this model","type":"invalid_request_error"}}`,
			payload: map[string]any{
				"model": "some-model", "seed": 42,
			},
			want: []string{"seed"},
		},
		{
			// Azure Foundry blames the field in the plural and names the model, and
			// the caller's tools are mentioned in the same breath. Only the parameter
			// directly before "are not supported" may be learned: dropping `tools`
			// would answer a tool call with prose.
			name: "azure plural rejection names one parameter",
			body: `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-5.6-sol in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.","type":"invalid_request_error","param":null,"code":null}}`,
			payload: map[string]any{
				"model": "gpt-5.6-sol", "reasoning_effort": "medium",
				"tools": []any{map[string]any{"type": "function"}},
			},
			want: []string{"reasoning_effort"},
		},
		{
			// Azure's word for the same complaint. Until the gateway read it, every
			// rejection like this one was final and cost the key a strike, even
			// though the field it names is one the gateway is allowed to drop.
			name: "azure unknown parameter",
			body: `{"error":{"message":"Unknown parameter: 'stream_options'.","type":"invalid_request_error","param":"stream_options","code":"unknown_parameter"}}`,
			payload: map[string]any{
				"model": "gpt-5.6-sol", "stream_options": map[string]any{"include_usage": true},
			},
			want: []string{"stream_options"},
		},
		{
			// The rejection that started this: the field is real, the spelling is
			// now recognised, and the answer is still to leave it alone. An output
			// cap is not a decoration the gateway may drop to make a call succeed —
			// removing it would silently change what the caller asked for.
			name: "an output cap is never stripped, however it is spelled",
			body: `{"error":{"message":"Unknown parameter: 'max_tokens'.","type":"invalid_request_error","param":"max_tokens","code":"unknown_parameter"}}`,
			payload: map[string]any{
				"model": "gpt-5.6-sol", "max_tokens": json.Number("1024"),
			},
			want: nil,
		},
		{
			name: "param requires unsupported signal",
			body: `{"error":{"message":"thinking has an invalid value","param":"thinking","code":"invalid_value"}}`,
			payload: map[string]any{
				"thinking": "aggressive",
			},
			want: nil,
		},
		{
			name: "protected or content fields are never learned",
			body: `{"error":{"message":"Unsupported parameter(s): messages","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"messages": []any{},
			},
			want: nil,
		},
		{
			name: "reported field must be present at the top level",
			body: `{"error":{"message":"Unsupported parameter(s): verbosity","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"options": map[string]any{"verbosity": "high"},
			},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := unsupportedCompatibilityParameters([]byte(test.body), test.payload)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parameters = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestErrorDemandsResponsesEndpoint covers the signal that turns a dead-end 400
// into a retry: a provider naming /responses is telling the gateway which
// endpoint the request belongs on, and that is worth more than the route's own
// configuration. The direction matters — a provider steering a request away from
// /responses must not be read as an invitation to send one there.
func TestErrorDemandsResponsesEndpoint(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "azure directs function tools to responses",
			body: `{"error":{"message":"Function tools with reasoning_effort are not supported for gpt-5.6-sol in /v1/chat/completions. To use function tools, use /v1/responses or set reasoning_effort to 'none'.","type":"invalid_request_error"}}`,
			want: true,
		},
		{
			name: "responses api named in prose",
			body: `{"error":{"message":"This model is only available through the Responses API. Use the Responses API instead."}}`,
			want: true,
		},
		{
			name: "steered away from responses",
			body: `{"error":{"message":"This model is not available in /v1/responses. Use /v1/chat/completions instead of /v1/responses."}}`,
			want: false,
		},
		{
			name: "unrelated rejection",
			body: `{"error":{"message":"Unsupported parameter(s): reasoning_effort","code":"unsupported_parameter"}}`,
			want: false,
		},
		{
			name: "non json body",
			body: `400 page not found`,
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := errorDemandsResponsesEndpoint([]byte(test.body)); got != test.want {
				t.Fatalf("demands responses = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUpstreamErrorMessageIsStructuredCappedAndRedacted(t *testing.T) {
	secret := []byte("super-secret-key")
	body := []byte(`{"error":{"message":"Invalid key super-secret-key\nplease replace it"}}`)
	got := upstreamErrorMessage(body, secret)
	if got != "Invalid key [redacted] please replace it" {
		t.Fatalf("unexpected sanitized message %q", got)
	}
	long := `{"error":{"message":"` + strings.Repeat("x", 600) + `"}}`
	if got := upstreamErrorMessage([]byte(long), nil); len(got) > 503 {
		t.Fatalf("message was not capped: %d bytes", len(got))
	}
}

func TestAppendUniqueStrings(t *testing.T) {
	got := appendUniqueStrings([]string{"thinking"}, "thinking", "verbosity")
	want := []string{"thinking", "verbosity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
}

func TestUnsupportedCompatibilityReplacement(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		payload map[string]any
		want    compatibilityReplacement
		ok      bool
	}{
		{
			name: "azure max tokens replacement",
			body: `{"error":{"message":"Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"max_tokens": json.Number("128"),
			},
			want: compatibilityReplacement{From: "max_tokens", To: "max_completion_tokens"},
			ok:   true,
		},
		{
			name: "message source fallback",
			body: `{"error":{"message":"Unsupported parameter: max_completion_tokens is not supported with this model. Use max_tokens instead.","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"max_completion_tokens": 64,
			},
			want: compatibilityReplacement{From: "max_completion_tokens", To: "max_tokens"},
			ok:   true,
		},
		{
			// The one repair that does apply to an output cap: the provider named a
			// replacement, so the field is renamed rather than dropped and the
			// caller's limit survives. Recognising "Unknown parameter" is what puts
			// this case in reach at Azure.
			name: "azure unknown parameter names a replacement",
			body: `{"error":{"message":"Unknown parameter: 'max_tokens'. Use 'max_output_tokens' instead.","type":"invalid_request_error","param":"max_tokens","code":"unknown_parameter"}}`,
			payload: map[string]any{
				"max_tokens": json.Number("1024"),
			},
			want: compatibilityReplacement{From: "max_tokens", To: "max_output_tokens"},
			ok:   true,
		},
		{
			// Without a named replacement there is nothing to rename it to, so the
			// rejection stands. This is the exact body Azure returned for request
			// req_TrwPgSoxFIVzRX9rBTjwisMX: no repair existed, because the fault was
			// that the gateway wrote a Chat field onto a Responses request at all.
			name: "an output cap alone cannot be repaired",
			body: `{"error":{"message":"Unknown parameter: 'max_tokens'.","type":"invalid_request_error","param":"max_tokens","code":"unknown_parameter"}}`,
			payload: map[string]any{
				"max_tokens": json.Number("1024"),
			},
			ok: false,
		},
		{
			name: "arbitrary rename is rejected",
			body: `{"error":{"message":"Unsupported parameter: temperature. Use top_p instead.","param":"temperature","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"temperature": 0.2,
			},
			ok: false,
		},
		{
			name: "source must be top level",
			body: `{"error":{"message":"Unsupported parameter: max_tokens. Use max_completion_tokens instead.","param":"max_tokens","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"options": map[string]any{"max_tokens": 128},
			},
			ok: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := unsupportedCompatibilityReplacement([]byte(test.body), test.payload)
			if ok != test.ok || got != test.want {
				t.Fatalf("replacement = %#v, %v; want %#v, %v", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestApplyCompatibilityReplacements(t *testing.T) {
	payload := map[string]any{
		"max_tokens":            json.Number("128"),
		"max_completion_tokens": json.Number("256"),
		"messages":              []any{},
	}
	applied := applyCompatibilityReplacements(payload, map[string]string{
		"max_tokens": "max_completion_tokens",
		"messages":   "input",
	})
	if _, exists := payload["max_tokens"]; exists {
		t.Fatal("source parameter was not removed")
	}
	if got := numberAsInt64(payload["max_completion_tokens"]); got != 256 {
		t.Fatalf("existing target was overwritten: %d", got)
	}
	if _, exists := payload["messages"]; !exists {
		t.Fatal("unsafe replacement changed a protected field")
	}
	want := map[string]string{"max_tokens": "max_completion_tokens"}
	if !reflect.DeepEqual(applied, want) {
		t.Fatalf("applied = %#v, want %#v", applied, want)
	}
	if got := formatCompatibilityReplacements(applied); got != "max_tokens=max_completion_tokens" {
		t.Fatalf("formatted replacements = %q", got)
	}
}

func TestResolveCompatibilityReplacement(t *testing.T) {
	target, ok := resolveCompatibilityReplacement("max_tokens", map[string]string{
		"max_tokens":            "max_completion_tokens",
		"max_completion_tokens": "max_output_tokens",
	})
	if !ok || target != "max_output_tokens" {
		t.Fatalf("resolved target = %q, %v", target, ok)
	}

	if target, ok := resolveCompatibilityReplacement("max_tokens", map[string]string{
		"max_tokens":            "max_completion_tokens",
		"max_completion_tokens": "max_tokens",
	}); ok || target != "" {
		t.Fatalf("cycle was accepted: %q, %v", target, ok)
	}
}

func TestActiveRequestLogsExposeRunningStateAndFilter(t *testing.T) {
	server := &Server{}
	started := time.Now().Add(-1500 * time.Millisecond)
	server.beginActiveRequest("req_running", "chat", started)
	server.updateActiveRequest("req_running", func(log *RequestLog) {
		log.ModelAlias = "azure/Kimi-K2.6"
		log.ProviderName = "azure"
		log.CredentialLabel = "primary"
	})

	logs := server.activeRequestLogs("kimi")
	if len(logs) != 1 {
		t.Fatalf("active logs = %d, want 1", len(logs))
	}
	if !logs[0].Running || logs[0].StatusCode != 0 {
		t.Fatalf("active state = running %v status %d", logs[0].Running, logs[0].StatusCode)
	}
	if logs[0].CredentialLabel != "primary" || logs[0].LatencyMS < 1000 {
		t.Fatalf("active metadata = %#v", logs[0])
	}
	if got := server.activeRequestLogs("missing"); len(got) != 0 {
		t.Fatalf("unexpected filtered logs: %#v", got)
	}
}


func TestProviderRetryTimeout(t *testing.T) {
	minute := time.Minute
	cases := []struct {
		name    string
		seconds int
		stream  bool
		want    time.Duration
	}{
		{"default when unset", 0, false, 120 * time.Second},
		{"honors configured timeout", 300, false, 300 * time.Second},
		{"raised timeout extends window", 600, false, 600 * time.Second},
		{"stream floors short timeout", 120, true, 15 * minute},
		{"stream keeps longer timeout", 1200, true, 1200 * time.Second},
		{"stream default floors to 15m", 0, true, 15 * minute},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := providerRetryTimeout(Provider{TimeoutSeconds: test.seconds}, test.stream)
			if got != test.want {
				t.Fatalf("providerRetryTimeout(%d, stream=%v) = %s, want %s", test.seconds, test.stream, got, test.want)
			}
		})
	}
}
