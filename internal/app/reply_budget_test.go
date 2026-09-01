package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestStarvedReplyIsRecognisedOnEveryWire pins the detector against each
// wire's spelling of "the budget ran out before anything visible", and
// against the replies that must NOT be retried: anything with text, anything
// with a tool call, anything that finished normally.
func TestStarvedReplyIsRecognisedOnEveryWire(t *testing.T) {
	encode := func(value map[string]any) []byte {
		body, _ := json.Marshal(value)
		return body
	}
	cases := []struct {
		name    string
		format  string
		wire    string
		body    map[string]any
		starved bool
	}{
		{
			// The production shape: claude-fable-5, all thinking, max_tokens.
			"anthropic thinking-only truncation", "anthropic", "chat",
			map[string]any{"stop_reason": "max_tokens", "content": []any{
				map[string]any{"type": "thinking", "thinking": "…"},
			}}, true,
		},
		{
			"anthropic truncated mid-sentence keeps its text", "anthropic", "chat",
			map[string]any{"stop_reason": "max_tokens", "content": []any{
				map[string]any{"type": "text", "text": "half an ans"},
			}}, false,
		},
		{
			"anthropic tool call is an answer", "anthropic", "chat",
			map[string]any{"stop_reason": "max_tokens", "content": []any{
				map[string]any{"type": "tool_use", "id": "tu_1", "name": "read", "input": map[string]any{}},
			}}, false,
		},
		{
			"anthropic finished normally with nothing to say", "anthropic", "chat",
			map[string]any{"stop_reason": "end_turn", "content": []any{}}, false,
		},
		{
			// The NVIDIA shape: a reasoning model on the chat wire burns the
			// budget before its first visible word.
			"chat empty completion finishing with length", "openai", "chat",
			map[string]any{"choices": []any{map[string]any{
				"finish_reason": "length", "message": map[string]any{"role": "assistant", "content": ""},
			}}}, true,
		},
		{
			"chat truncated but visible", "openai", "chat",
			map[string]any{"choices": []any{map[string]any{
				"finish_reason": "length", "message": map[string]any{"role": "assistant", "content": "part of"},
			}}}, false,
		},
		{
			"responses incomplete on its budget with no output text", "openai", "responses",
			map[string]any{
				"status": "incomplete", "incomplete_details": map[string]any{"reason": "max_output_tokens"},
				"output": []any{map[string]any{"type": "reasoning", "id": "rs_1", "summary": []any{}}},
			}, true,
		},
		{
			"responses incomplete for another reason", "openai", "responses",
			map[string]any{
				"status": "incomplete", "incomplete_details": map[string]any{"reason": "content_filter"},
				"output": []any{},
			}, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := starvedReply(tc.format, tc.wire, encode(tc.body)); got != tc.starved {
				t.Fatalf("starved = %v, want %v", got, tc.starved)
			}
		})
	}
}

// TestReplyBudgetEscalationIsBoundedBothWays: proportional to what the route
// considered normal, never a pointless nudge, never past what a model's own
// output maximum could refuse.
func TestReplyBudgetEscalationIsBoundedBothWays(t *testing.T) {
	steps := map[int64]int64{1024: 4096, 4096: 16384, 16384: 32768, 32768: 32768, 100: 4096}
	for from, want := range steps {
		if got := escalateReplyBudget(from); got != want {
			t.Fatalf("escalate(%d) = %d, want %d", from, got, want)
		}
	}
}

func TestReplyFloorSpeaksEachWiresSpelling(t *testing.T) {
	anthropic := map[string]any{"max_tokens": 1024}
	if !applyReplyFloor(anthropic, "anthropic", "chat", 4096) || numberAsInt64(anthropic["max_tokens"]) != 4096 {
		t.Fatalf("anthropic floor: %v", anthropic)
	}
	responses := map[string]any{"max_output_tokens": 1024}
	if !applyReplyFloor(responses, "openai", "responses", 4096) || numberAsInt64(responses["max_output_tokens"]) != 4096 {
		t.Fatalf("responses floor: %v", responses)
	}
	chat := map[string]any{"max_completion_tokens": 1024}
	if !applyReplyFloor(chat, "openai", "chat", 4096) || numberAsInt64(chat["max_completion_tokens"]) != 4096 {
		t.Fatalf("chat floor kept the caller's own field: %v", chat)
	}
	// A floor never lowers: a generous cap stays generous.
	generous := map[string]any{"max_tokens": 64000}
	if applyReplyFloor(generous, "anthropic", "chat", 4096) {
		t.Fatal("the floor lowered a cap")
	}
}

// TestReplyFloorSurvivesRedisAndRefusesGarbage is Redis-gated like the other
// learned-state tests.
func TestReplyFloorSurvivesRedisAndRefusesGarbage(t *testing.T) {
	server := learnedServer(t)
	ctx := t.Context()
	modelID := "mdl_floor_test"
	t.Cleanup(func() { server.forgetLearnedRouteState(ctx, modelID) })

	server.rememberReplyFloor(ctx, modelID, 4096)
	if got := server.learnedReplyFloor(ctx, modelID); got != 4096 {
		t.Fatalf("floor came back as %d", got)
	}
	// Remembering less than what is known is not an update.
	server.rememberReplyFloor(ctx, modelID, 2048)
	if got := server.learnedReplyFloor(ctx, modelID); got != 4096 {
		t.Fatalf("a smaller floor overwrote a proven one: %d", got)
	}
	// Redis outlives deploys; a value this code cannot have written is ignored.
	if err := server.redis.Set(ctx, replyFloorKey(modelID), "999999", adaptiveCompatibilityTTL).Err(); err != nil {
		t.Fatalf("seed garbage: %v", err)
	}
	if got := server.learnedReplyFloor(ctx, modelID); got != 0 {
		t.Fatalf("a hand-written floor was believed: %d", got)
	}
}

// TestOrphanedPairingIsRepairedByDetachingIDs is req_soR8wK3v9TVVSzDIfOgO90AC:
// Codex replayed a message without the reasoning item it was born with, and
// Azure demanded the pair. Without the id there is no pair to demand.
func TestOrphanedPairingIsRepairedByDetachingIDs(t *testing.T) {
	rejection := []byte(`{"error":{"code":"invalid_request_error","message":"Item 'msg_0bee41ea7484eb39006a973eab283881919189efbc3a29528e' of type 'message' was provided without its required 'reasoning' item: 'rs_0bee41ea7484eb39006a973ea55b788191914751a7781bed92'."}}`)
	payload := map[string]any{"input": []any{
		map[string]any{"role": "user", "content": "fix the bug"},
		map[string]any{"role": "assistant", "id": "msg_0bee41ea7484eb39006a973eab283881919189efbc3a29528e", "content": []any{
			map[string]any{"type": "output_text", "text": "working on it"},
		}},
		map[string]any{"type": "function_call", "id": "fc_1", "call_id": "call_1", "name": "read", "arguments": "{}"},
	}}

	if !orphanedReasoningPairing(rejection, payload) {
		t.Fatal("the production rejection was not recognised")
	}
	// A request with no replayed ids left has nothing this repair can do, and
	// learning it anyway would burn a retry on an identical request.
	if orphanedReasoningPairing(rejection, map[string]any{"input": []any{map[string]any{"role": "user", "content": "hi"}}}) {
		t.Fatal("recognised a rejection the payload cannot explain")
	}

	if !stripReplayedItemIDs(payload) {
		t.Fatal("nothing was detached")
	}
	message := payload["input"].([]any)[1].(map[string]any)
	if _, carries := message["id"]; carries {
		t.Fatalf("the replayed message kept its id: %v", message)
	}
	if message["content"].([]any)[0].(map[string]any)["text"] != "working on it" {
		t.Fatal("the message lost more than its id")
	}
	// A function_call pairs with its output by call_id; its id is not replayed
	// state and stays.
	call := payload["input"].([]any)[2].(map[string]any)
	if call["id"] != "fc_1" || call["call_id"] != "call_1" {
		t.Fatalf("a non-message item was touched: %v", call)
	}
}

// TestStreamPeekFailsOverAnErrorFirstStream is the NVIDIA request: HTTP 200,
// 121 seconds of silence, then ResourceExhausted as the stream's only event.
// The peek turns that into a failed attempt while failover is still possible.
func TestStreamPeekFailsOverAnErrorFirstStream(t *testing.T) {
	stream := func(body string) *http.Response {
		return &http.Response{
			Header: http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:   io.NopCloser(strings.NewReader(body)),
		}
	}

	t.Run("an error-first stream is refused before any byte commits", func(t *testing.T) {
		_, frame, err := prepareOpenAIStreamSource(stream(
			"data: {\"error\":{\"code\":\"ResourceExhausted\",\"message\":\"Worker local total request limit reached (118/32)\"}}\n\n"))
		if err == nil {
			t.Fatal("the in-band error was committed to the caller")
		}
		if !bytes.Contains(frame, []byte("ResourceExhausted")) {
			t.Fatalf("the provider's own words were lost: %s", frame)
		}
	})

	t.Run("a healthy stream passes through byte for byte, prelude included", func(t *testing.T) {
		body := ": ping\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\ndata: [DONE]\n\n"
		source, _, err := prepareOpenAIStreamSource(stream(body))
		if err != nil || source == nil {
			t.Fatalf("a healthy stream was refused: %v", err)
		}
		forwarded, _ := io.ReadAll(bufio.NewReader(source))
		if string(forwarded) != body {
			t.Fatalf("the stream was reshaped:\n%q\n%q", body, forwarded)
		}
	})

	t.Run("a non-SSE body is someone else's problem", func(t *testing.T) {
		response := &http.Response{
			Header: http.Header{"Content-Type": []string{"application/json"}},
			Body:   io.NopCloser(strings.NewReader(`{"error":{}}`)),
		}
		source, frame, err := prepareOpenAIStreamSource(response)
		if source != nil || frame != nil || err != nil {
			t.Fatal("a non-SSE response was intercepted")
		}
	})

	t.Run("a stream that dies before its first event is a failed attempt", func(t *testing.T) {
		if _, _, err := prepareOpenAIStreamSource(stream(": warming up\n\n")); err == nil {
			t.Fatal("an eventless stream was committed to the caller")
		}
	})
}
