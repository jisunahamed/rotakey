package app

import (
	"encoding/json"
	"testing"
)

// azureUnknownParameterBody is the rejection that made every Codex turn against
// an Azure route fail, written out the way Azure sends it. The message is the
// one on the request log; the param field carries the same path whole.
func azureUnknownParameterBody(param, message string) []byte {
	body, err := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "invalid_request_error",
			"param":   param,
			"code":    "unknown_parameter",
		},
	})
	if err != nil {
		panic(err)
	}
	return body
}

// codexPayload is a Responses request shaped the way Codex CLI sends it: its own
// bookkeeping field hung off every turn, beside the fields the protocol defines.
func codexPayload() map[string]any {
	turn := func(role, text string) map[string]any {
		return map[string]any{
			"role":    role,
			"content": []any{map[string]any{"type": "input_text", "text": text}},
			"internal_chat_message_metadata_passthrough": map[string]any{
				"content_type": "text", "origin": "codex",
			},
		}
	}
	return map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{turn("user", "hello"), turn("assistant", "hi"), turn("user", "again")},
	}
}

// TestUnsupportedItemFieldReadsTheReportedRejection is the regression guard for
// the failure this repair was written for. Every Codex turn against Azure came
// back 400 unknown_parameter, final, one attempt — and the credential that
// served it took a strike for a field the operator never wrote.
func TestUnsupportedItemFieldReadsTheReportedRejection(t *testing.T) {
	cases := []struct {
		name    string
		param   string
		message string
	}{
		{
			name:    "param carries the path",
			param:   "input[0].internal_chat_message_metadata_passthrough.content_type",
			message: "Unknown parameter: 'input[0].internal_chat_message_metadata_passthrough.content_type'.",
		},
		{
			// Providers shorten their own messages. The field one level under the
			// item is still readable, and it is the one that has to go.
			name:    "message is cut off mid-path",
			param:   "",
			message: "Unknown parameter: 'input[0].internal_chat_message_metadata_passthrough.c",
		},
		{
			// Not every provider fills param, and not every one says "unknown".
			name:    "another provider's adjective, no param",
			param:   "",
			message: "Unsupported parameter: 'messages[2].vendor_trace_id'.",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := codexPayload()
			if testCase.name == "another provider's adjective, no param" {
				payload = map[string]any{"messages": []any{
					map[string]any{"role": "user", "content": "hello", "vendor_trace_id": "abc"},
				}}
			}
			strip, ok := unsupportedItemField(
				azureUnknownParameterBody(testCase.param, testCase.message), payload,
			)
			if !ok {
				t.Fatalf("no repair was read out of %q", testCase.message)
			}
			if removed := stripItemFields(payload, []itemFieldStrip{strip}); len(removed) != 1 {
				t.Fatalf("the strip removed %v", removed)
			}
		})
	}
}

// TestUnsupportedItemFieldRefusesWhatItMustNotDelete. This repair acts on a name
// the gateway has never seen before, which is what makes it useful and what
// makes it dangerous. The guards are the whole design, so they are the test.
func TestUnsupportedItemFieldRefusesWhatItMustNotDelete(t *testing.T) {
	cases := []struct {
		name    string
		param   string
		payload map[string]any
		why     string
	}{
		{
			name:    "a field the protocol defines",
			param:   "input[0].content",
			payload: codexPayload(),
			why:     "deleting content retries with an empty turn, which reads as the model ignoring the caller",
		},
		{
			name:    "the role of a turn",
			param:   "input[1].role",
			payload: codexPayload(),
			why:     "a turn with no role is not a turn",
		},
		{
			name:    "an array that is not the conversation",
			param:   "tools[0].strict",
			payload: map[string]any{"tools": []any{map[string]any{"strict": true}}},
			why:     "the operator configured the tools; the answer there is to fix the configuration",
		},
		{
			name:    "a top-level name with no index",
			param:   "input",
			payload: codexPayload(),
			why:     "deleting input is deleting the request",
		},
		{
			name:    "a field nothing in the request carries",
			param:   "input[0].some_other_field",
			payload: codexPayload(),
			why:     "learning a repair that changes nothing burns the retry budget and caches uselessness for a day",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := azureUnknownParameterBody(testCase.param, "Unknown parameter: '"+testCase.param+"'.")
			if strip, ok := unsupportedItemField(body, testCase.payload); ok {
				t.Fatalf("%s was accepted as a repair (%s): %s", testCase.param, testCase.why, strip)
			}
		})
	}
}

// TestStripItemFieldsLeavesTheCallersRequestAlone is the guard for the one thing
// that would be silently wrong. buildPlan clones the public request one level
// deep, so every candidate's payload shares the caller's own turn objects: a
// delete in place would edit the request itself, and the next candidate — a
// different provider, perhaps a different protocol — would be planned from a
// body the caller never sent.
func TestStripItemFieldsLeavesTheCallersRequestAlone(t *testing.T) {
	public := codexPayload()
	plan := cloneMap(public)

	removed := stripItemFields(plan, []itemFieldStrip{
		{Root: "input", Field: "internal_chat_message_metadata_passthrough"},
	})
	if len(removed) != 1 || removed[0] != "input[].internal_chat_message_metadata_passthrough" {
		t.Fatalf("removed %v", removed)
	}

	for index, item := range plan["input"].([]any) {
		turn := item.(map[string]any)
		if _, carries := turn["internal_chat_message_metadata_passthrough"]; carries {
			t.Fatalf("turn %d still carries the rejected field", index)
		}
		// Every turn, not just the one the provider blamed first: the field is in
		// all of them, and repairing one per round trip would spend the retry
		// budget on the second turn and fail the request anyway.
		if turn["role"] == nil || turn["content"] == nil {
			t.Fatalf("turn %d lost a field the protocol defines", index)
		}
	}
	for index, item := range public["input"].([]any) {
		if _, carries := item.(map[string]any)["internal_chat_message_metadata_passthrough"]; !carries {
			t.Fatalf("the caller's own turn %d was edited", index)
		}
	}

	// Nothing to remove is not an error, and it must not be reported as a repair.
	if removed := stripItemFields(cloneMap(public), []itemFieldStrip{{Root: "input", Field: "absent"}}); len(removed) != 0 {
		t.Fatalf("a strip that changed nothing reported %v", removed)
	}
}

// TestItemFieldStripSurvivesRedisAndIsRecheckedComingBack. The strips are stored
// as text in a set that outlives a deploy, so a value read back is treated as
// untrusted input — the same two guards run again on the way out.
func TestItemFieldStripSurvivesRedisAndIsRecheckedComingBack(t *testing.T) {
	original := itemFieldStrip{Root: "input", Field: "internal_chat_message_metadata_passthrough"}
	parsed, ok := parseItemFieldStrip(original.String())
	if !ok || parsed != original {
		t.Fatalf("round trip gave %v, %v", parsed, ok)
	}
	for _, stored := range []string{"input[].content", "tools[].strict", "input", "", "[].x"} {
		if strip, ok := parseItemFieldStrip(stored); ok {
			t.Fatalf("%q was read back as an actionable strip: %s", stored, strip)
		}
	}
}

// TestTopLevelStripStillRefusesTheConversation is the other half of the reason
// this file exists. The adaptive strip pass reads a parameter name through a
// pattern that stops at the '[', so all it ever saw was "input" — and it was
// right to refuse it. That refusal must not become a strip now that a second
// pass knows what to do with the rest of the path.
func TestTopLevelStripStillRefusesTheConversation(t *testing.T) {
	body := azureUnknownParameterBody(
		"input[0].internal_chat_message_metadata_passthrough.content_type",
		"Unknown parameter: 'input[0].internal_chat_message_metadata_passthrough.content_type'.",
	)
	payload := codexPayload()
	if parameters := unsupportedCompatibilityParameters(body, payload); len(parameters) != 0 {
		t.Fatalf("the top-level pass wanted to strip %v", parameters)
	}
	if _, ok := unsupportedCompatibilityReplacement(body, payload); ok {
		t.Fatalf("the replacement pass claimed a rename out of a path with no suggestion in it")
	}
}
