package app

import (
	"encoding/json"
	"testing"
)

// req_Y0r9yiju4l2aYv4kGaL_dISd and req_NwgCFhEAkDU5WNsyqmAk…: claude-fable-5
// answered HTTP 200 after ~19s of work, and the gateway turned it into a 502.
// The reply was thinking blocks alone — the model spent its whole output
// budget inside its own head — which the translator refused as "no text or
// tool calls". The upstream was paid for that answer; the caller got an error
// that read as an outage.
func thinkingOnlyAnthropicBody() []byte {
	body, _ := json.Marshal(map[string]any{
		"id": "msg_prod_502", "type": "message", "role": "assistant", "model": "claude-fable-5",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "…nineteen seconds of it…", "signature": "sig"},
		},
		"stop_reason": "max_tokens", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 5000, "output_tokens": 1024},
	})
	return body
}

func TestThinkingOnlyReplyTranslatesInsteadOfFailing(t *testing.T) {
	t.Run("to chat, as an empty message finishing with length", func(t *testing.T) {
		body, input, output, err := translateAnthropicResponseToChat(thinkingOnlyAnthropicBody(), "micro/claude-fable-5")
		if err != nil {
			t.Fatalf("a billed upstream 200 was refused: %v", err)
		}
		var chat struct {
			Choices []struct {
				Message      map[string]any `json:"message"`
				FinishReason string         `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &chat); err != nil || len(chat.Choices) != 1 {
			t.Fatalf("translation shape: %v %s", err, body)
		}
		if chat.Choices[0].Message["content"] != "" {
			t.Fatalf("thinking leaked into the visible reply: %q", chat.Choices[0].Message["content"])
		}
		// "length" is the part the caller can act on: it says the budget ran
		// out, so the fix is a bigger max_tokens, not a retry.
		if chat.Choices[0].FinishReason != "length" {
			t.Fatalf("finish_reason %q does not say the budget ran out", chat.Choices[0].FinishReason)
		}
		if input != 5000 || output != 1024 {
			t.Fatalf("usage was dropped: %d in, %d out", input, output)
		}
	})

	t.Run("to responses, the shape the production caller spoke", func(t *testing.T) {
		body, input, output, err := translateAnthropicResponseToResponses(thinkingOnlyAnthropicBody(), "micro/claude-fable-5")
		if err != nil {
			t.Fatalf("a billed upstream 200 was refused: %v", err)
		}
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatalf("translation is not JSON: %v", err)
		}
		if input != 5000 || output != 1024 {
			t.Fatalf("usage was dropped: %d in, %d out", input, output)
		}
	})

	t.Run("a body with no content field is still refused", func(t *testing.T) {
		// The guard this replaces existed for a reason: a proxy's own JSON —
		// an error page, a rate-limit notice — must not become an empty
		// completion the caller reads as the model saying nothing.
		if _, _, _, err := translateAnthropicResponseToChat([]byte(`{"ok":true}`), "alias"); err == nil {
			t.Fatal("a non-message body was translated into an empty completion")
		}
	})
}

// req_qTOW8NS7tzZPT9VVdeRqmzDR: a caller sent stream_options with the stream
// off, Rotakey passed it along, and Azure refused the request — "The
// 'stream_options' parameter is only allowed when 'stream' is enabled." The
// parameter means nothing without a stream, so it is dropped at plan time.
func TestStreamOptionsLeaveABodyTheyCannotBeValidOn(t *testing.T) {
	cases := []struct {
		name    string
		shape   string
		stream  any
		dropped bool
	}{
		{"chat while streaming keeps it", "chat", true, false},
		{"chat without a stream drops it", "chat", false, true},
		{"chat with stream unset drops it", "chat", nil, true},
		{"responses never carries it", "responses", true, true},
		{"anthropic never carries it", "anthropic", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{"stream_options": map[string]any{"include_usage": true}}
			if tc.stream != nil {
				payload["stream"] = tc.stream
			}
			if got := dropMisplacedStreamOptions(payload, tc.shape); got != tc.dropped {
				t.Fatalf("dropped = %v, want %v", got, tc.dropped)
			}
			_, carries := payload["stream_options"]
			if carries == tc.dropped {
				t.Fatalf("payload carries stream_options = %v after dropped = %v", carries, tc.dropped)
			}
		})
	}
	t.Run("a body without it reports nothing", func(t *testing.T) {
		if dropMisplacedStreamOptions(map[string]any{"stream": false}, "chat") {
			t.Fatal("claimed to drop a parameter that was never there")
		}
	})
}

// TestConstraintRejectionsFeedTheStripPass pins the wider net: a provider that
// rejects a parameter for the company it keeps — rather than for being
// unknown — still names a parameter the strip pass may remove, and only ever
// an allowlisted one.
func TestConstraintRejectionsFeedTheStripPass(t *testing.T) {
	azure := []byte(`{"error":{"code":"invalid_request_error","message":"The 'stream_options' parameter is only allowed when 'stream' is enabled."}}`)
	payload := map[string]any{"stream_options": map[string]any{"include_usage": true}, "stream": false}
	if got := unsupportedCompatibilityParameters(azure, payload); len(got) != 1 || got[0] != "stream_options" {
		t.Fatalf("the constraint rejection did not name its parameter: %v", got)
	}

	// The same phrasing about a protected parameter must strip nothing —
	// "stream" is deliberately absent from the allowlist.
	protected := []byte(`{"error":{"message":"The 'stream' parameter is only allowed when the model supports it."}}`)
	if got := unsupportedCompatibilityParameters(protected, map[string]any{"stream": true}); len(got) != 0 {
		t.Fatalf("a protected parameter was offered for stripping: %v", got)
	}
}
