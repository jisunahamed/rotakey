package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSuccessKeepsNativeMessageDespiteConfiguredChatProvider(t *testing.T) {
	body := thinkingOnlyAnthropicBody()
	got, in, out := successfulUpstreamBody(dispatchRequest{PublicMode: messageModeAnthropic}, upstreamPlan{Format: "openai", Path: "/chat/completions"}, body)
	if string(got) != string(body) || in != 5000 || out != 1024 {
		t.Fatalf("native message was lost: %s %d/%d", got, in, out)
	}
}

func TestSuccessfulHTTPBodyNeverBecomesEmptyResponseFailure(t *testing.T) {
	for _, body := range []string{
		"", "unrecognized provider response", `{"metadata":{"request_id":"upstream"}}`,
		`{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"length"}],"usage":{"prompt_tokens":44,"completion_tokens":1}}`,
		`{"choices":[{"message":{"role":"assistant","reasoning_content":"provider reasoning"},"finish_reason":"length"}]}`,
		string(thinkingOnlyAnthropicBody()),
	} {
		t.Run(body, func(t *testing.T) {
			upstream := upstreamAnswering(t, body)
			server, candidate, plan := chatAttempt(t, upstream.URL)
			server.db = unreachablePostgres(t)
			plan.Recovered = true
			plan.Payload["max_tokens"] = 1
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			outcome := server.runAttempt(rec, req, dispatchRequest{PublicMode: messageModeAnthropic, Alias: "claude-sonnet-5"}, candidate, plan, upstream.Client(), context.Background(), reservation{}, true)
			if !outcome.Done || outcome.Status != 200 || rec.Code != 200 || outcome.ErrorCode != "" || outcome.Compatibility {
				t.Fatalf("2xx became failure/retry: %+v", outcome)
			}
			if strings.Contains(body, "provider reasoning") && !strings.Contains(rec.Body.String(), "provider reasoning") {
				t.Fatal("reasoning was discarded")
			}
			if body == "" && rec.Body.Len() != 0 {
				t.Fatal("fabricated an answer for empty upstream")
			}
		})
	}
}

func TestSuccessfulStreamPreservesAllChunksAndStatus(t *testing.T) {
	for _, body := range []string{
		"", "data: {\"error\":{\"message\":\"provider error\"}}\n\n",
		": warming up\n\ndata: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"late answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"content\":[]}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	} {
		t.Run(body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, body)
			}))
			defer upstream.Close()
			server, candidate, plan := chatAttempt(t, upstream.URL)
			server.db = unreachablePostgres(t)
			_, actual := successfulStreamSource(strings.NewReader(body))
			mode := messageModeChat
			if actual == messageModeAnthropic {
				mode = messageModeAnthropic
			}
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			outcome := server.runAttempt(rec, req, dispatchRequest{Stream: true, PublicMode: mode, Alias: "alias"}, candidate, plan, upstream.Client(), context.Background(), reservation{}, true)
			if !outcome.Done || outcome.Status != 200 || rec.Code != 200 || rec.Body.String() != body {
				t.Fatalf("stream altered/rejected: %+v %q", outcome, rec.Body.String())
			}
		})
	}
}

func TestReasoningSurvivesProtocolTranslation(t *testing.T) {
	body, _, _ := successfulUpstreamBody(dispatchRequest{PublicMode: messageModeResponses, Alias: "alias"}, upstreamPlan{}, thinkingOnlyAnthropicBody())
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil || !strings.Contains(string(body), "nineteen seconds") {
		t.Fatalf("thinking lost: %s", body)
	}
}

func TestNativeAnthropicStreamUsageSurvivesPassThrough(t *testing.T) {
	stream := "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":44,\"output_tokens\":0,\"cache_read_input_tokens\":10}}}\n\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":12}}\n\n"
	evidence := &streamEvidenceReader{source: strings.NewReader(stream)}
	got, err := io.ReadAll(evidence)
	if err != nil || string(got) != stream || evidence.input != 54 || evidence.output != 12 {
		t.Fatalf("usage/bytes changed: input=%d output=%d err=%v", evidence.input, evidence.output, err)
	}
}

func TestStreamingClientReceivesJSONMessageAsCompleteSSE(t *testing.T) {
	body := string(thinkingOnlyAnthropicBody())
	upstream := upstreamAnswering(t, body)
	server, candidate, plan := chatAttempt(t, upstream.URL)
	server.db = unreachablePostgres(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	outcome := server.runAttempt(rec, req, dispatchRequest{Stream: true, PublicMode: messageModeAnthropic}, candidate, plan, upstream.Client(), context.Background(), reservation{}, true)
	if !outcome.Done || rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" || !strings.Contains(rec.Body.String(), "nineteen seconds") || !strings.Contains(rec.Body.String(), "message_stop") {
		t.Fatalf("JSON message lost or rejected: %+v %s", outcome, rec.Body.String())
	}
}

func TestSuccessAboveBufferLimitIsForwardedWithoutLosingBytes(t *testing.T) {
	body := strings.Repeat("provider output ", 200)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "streaming request with JSON reply"}[stream], func(t *testing.T) {
			upstream := upstreamAnswering(t, body)
			server, candidate, plan := chatAttempt(t, upstream.URL)
			server.db = unreachablePostgres(t)
			server.cfg.MaxResponseBytes = 32
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			outcome := server.runAttempt(rec, req, dispatchRequest{Stream: stream, PublicMode: messageModeAnthropic}, candidate, plan, upstream.Client(), context.Background(), reservation{}, true)
			if !outcome.Done || rec.Code != 200 || rec.Body.String() != body {
				t.Fatalf("large success lost data: status=%d bytes=%d want=%d", rec.Code, rec.Body.Len(), len(body))
			}
		})
	}
}

func TestCrossProtocolStreamKeepsLateReasoningTextAndTools(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"kept reasoning\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"late text\",\"tool_calls\":[{\"index\":0,\"id\":\"tool_a\",\"function\":{\"name\":\"write\",\"arguments\":\"{\\\"file_path\\\":\\\"a.txt\\\",\\\"content\\\":\\\"hello\\\"}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	defer upstream.Close()
	server, candidate, plan := chatAttempt(t, upstream.URL)
	server.db = unreachablePostgres(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	outcome := server.runAttempt(rec, req, dispatchRequest{Stream: true, PublicMode: messageModeAnthropic, Alias: "alias"}, candidate, plan, upstream.Client(), context.Background(), reservation{}, true)
	for _, want := range []string{"kept reasoning", "late text", "file_path", "a.txt", "hello", "message_stop"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("missing %q: %s", want, rec.Body.String())
		}
	}
	if !outcome.Done || rec.Code != 200 {
		t.Fatalf("stream rejected: %+v", outcome)
	}
}
