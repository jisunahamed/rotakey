package app

import (
	"testing"
)

// The two payloads below are the two production rejections this pass exists
// for, reduced to their offending shapes. req_FUtg6wfPk9tKFQ9OxHfu2-EO: Codex
// replayed its history at /responses with the model's own reply labeled
// input_text, and Azure answered "Supported values are: 'output_text' and
// 'refusal'". req_rcGZ-F0Fh1WtPCW8MmyA-FBL: the same kind of history, carried
// by the chat→anthropic translation into a tool_result block whole, and
// Anthropic answered "Input tag 'input_text' … does not match any of the
// expected tags: … 'text' …".

func codexResponsesPayload() map[string]any {
	return map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
			}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "input_text", "text": "hi — what can I do?"},
			}},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "read", "arguments": "{}"},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "fix the bug"},
			}},
		},
	}
}

func anthropicToolResultPayload() map[string]any {
	return map[string]any{
		"model": "claude-fable-5",
		"system": []any{
			map[string]any{"type": "input_text", "text": "be brief"},
		},
		"messages": []any{
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "output_text", "text": "checking"},
				map[string]any{"type": "tool_use", "id": "tu_1", "name": "read", "input": map[string]any{}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{
					"type": "tool_result", "tool_use_id": "tu_1",
					"content": []any{map[string]any{"type": "input_text", "text": "file contents"}},
				},
			}},
		},
	}
}

func partType(t *testing.T, payload map[string]any, root string, item, part int) string {
	t.Helper()
	object := payload[root].([]any)[item].(map[string]any)
	parts, ok := object["content"].([]any)
	if !ok {
		t.Fatalf("%s[%d] carries no content array", root, item)
	}
	kind, _ := parts[part].(map[string]any)["type"].(string)
	return kind
}

// TestRelabelConversationSpeaksEachWiresVocabulary is the two production
// rejections plus the third wire, which has not failed yet only because
// nothing strict has been asked to read a foreign label there.
func TestRelabelConversationSpeaksEachWiresVocabulary(t *testing.T) {
	t.Run("responses types text by who said it", func(t *testing.T) {
		payload := codexResponsesPayload()
		applied := relabelConversation(payload, "responses")
		if got := partType(t, payload, "input", 1, 0); got != "output_text" {
			t.Fatalf("the model's own reply went out as %q", got)
		}
		if got := partType(t, payload, "input", 0, 0); got != "input_text" {
			t.Fatalf("the caller's turn was relabeled to %q", got)
		}
		if applied["input_text"] != "output_text" {
			t.Fatalf("the rename is not in the evidence: %v", applied)
		}
		// The function_call item has no role and no content array; the pass has
		// no business inside it.
		call := payload["input"].([]any)[2].(map[string]any)
		if call["type"] != "function_call" || call["call_id"] != "call_1" {
			t.Fatalf("a non-message item was rewritten: %v", call)
		}
	})

	t.Run("responses relabels chat history by role", func(t *testing.T) {
		payload := map[string]any{"input": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "q"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "a"}}},
		}}
		relabelConversation(payload, "responses")
		if got := partType(t, payload, "input", 0, 0); got != "input_text" {
			t.Fatalf("chat text on a user turn became %q", got)
		}
		if got := partType(t, payload, "input", 1, 0); got != "output_text" {
			t.Fatalf("chat text on an assistant turn became %q", got)
		}
	})

	t.Run("anthropic spells every text the same and reads inside tool_result", func(t *testing.T) {
		payload := anthropicToolResultPayload()
		applied := relabelConversation(payload, "anthropic")
		if got := partType(t, payload, "messages", 0, 0); got != "text" {
			t.Fatalf("the assistant's output_text went out as %q", got)
		}
		result := payload["messages"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
		inner, _ := result["content"].([]any)[0].(map[string]any)["type"].(string)
		if inner != "text" {
			// The exact production path: messages.1.content.0.tool_result.content.0.
			t.Fatalf("the tool_result's inner part went out as %q", inner)
		}
		if result["type"] != "tool_result" || result["tool_use_id"] != "tu_1" {
			t.Fatalf("the tool_result block itself was rewritten: %v", result)
		}
		if got := payload["system"].([]any)[0].(map[string]any)["type"]; got != "text" {
			t.Fatalf("the system block went out as %q", got)
		}
		if applied["input_text"] != "text" || applied["output_text"] != "text" {
			t.Fatalf("the renames are not in the evidence: %v", applied)
		}
	})

	t.Run("chat spells every text the same", func(t *testing.T) {
		payload := map[string]any{"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "input_text", "text": "q"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "a"}}},
		}}
		relabelConversation(payload, "chat")
		if got := partType(t, payload, "messages", 0, 0); got != "text" {
			t.Fatalf("input_text on the chat wire went out as %q", got)
		}
		if got := partType(t, payload, "messages", 1, 0); got != "text" {
			t.Fatalf("output_text on the chat wire went out as %q", got)
		}
	})
}

// TestRelabelConversationTouchesOnlyTheThreeTextLabels is the corruption
// guard. Every label outside the text set has its own payload shape, and a
// pass that renamed one would be manufacturing a block no protocol defines.
func TestRelabelConversationTouchesOnlyTheThreeTextLabels(t *testing.T) {
	payload := map[string]any{"messages": []any{
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": "https://x.example/a.png"}},
			map[string]any{"type": "browser_state", "state": "…"},
		}},
		map[string]any{"role": "assistant", "content": "plain string, no labels"},
	}}
	if applied := relabelConversation(payload, "anthropic"); len(applied) != 0 {
		t.Fatalf("labels outside the text set were renamed: %v", applied)
	}
	if got := partType(t, payload, "messages", 0, 0); got != "image" {
		t.Fatalf("an image block became %q", got)
	}
	if got := partType(t, payload, "messages", 0, 1); got != "browser_state" {
		t.Fatalf("a provider's own block type became %q", got)
	}
	if payload["messages"].([]any)[1].(map[string]any)["content"] != "plain string, no labels" {
		t.Fatal("a plain-string content was rewritten")
	}
}

// TestRelabelConversationStaysOutOfItemsThatAreNotTurns pins the role guard.
// A reasoning item carries a content array of its own, and nothing in it is
// conversation text — even a part whose label happens to sit in the text set
// belongs to the item's shape, not to the wire's turn vocabulary.
func TestRelabelConversationStaysOutOfItemsThatAreNotTurns(t *testing.T) {
	payload := map[string]any{"input": []any{
		map[string]any{"type": "reasoning", "id": "rs_1", "content": []any{
			map[string]any{"type": "reasoning_text", "text": "thinking"},
			map[string]any{"type": "text", "text": "a label in the text set, in a roleless item"},
		}},
	}}
	if applied := relabelConversation(payload, "responses"); len(applied) != 0 {
		t.Fatalf("a roleless item's parts were renamed: %v", applied)
	}
	if got := partType(t, payload, "input", 0, 1); got != "text" {
		t.Fatalf("a reasoning item's part became %q", got)
	}
}

// TestRelabelConversationLeavesTheCallersRequestAlone mirrors the strip pass's
// guard, and for the same reason: buildPlan's clone is one level deep, so the
// items and parts are the caller's own objects. A pass that wrote through them
// would edit the request every other candidate is planned from.
func TestRelabelConversationLeavesTheCallersRequestAlone(t *testing.T) {
	public := codexResponsesPayload()
	callerAssistantPart := public["input"].([]any)[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	callerUserItem := public["input"].([]any)[0]

	payload := cloneMap(public)
	relabelConversation(payload, "responses")

	if callerAssistantPart["type"] != "input_text" {
		t.Fatalf("the caller's own part was edited in place to %q", callerAssistantPart["type"])
	}
	if got := partType(t, payload, "input", 1, 0); got != "output_text" {
		t.Fatalf("the plan's part was not relabeled: %q", got)
	}
	// An untouched item is carried by reference, not copied for nothing.
	if planUserItem := payload["input"].([]any)[0]; &planUserItem == nil || !sameMap(callerUserItem, planUserItem) {
		t.Fatal("an untouched item was needlessly copied")
	}
}

func sameMap(a, b any) bool {
	left, leftOK := a.(map[string]any)
	right, rightOK := b.(map[string]any)
	if !leftOK || !rightOK {
		return false
	}
	// Two map values compare equal as references only through their backing
	// storage; writing through one and reading the other is the test.
	left["__probe"] = true
	_, shared := right["__probe"]
	delete(left, "__probe")
	return shared
}

// TestCodexRequestSurvivesBothRepairsInPlanOrder is req_FUtg6wfPk9tKFQ9OxHfu2-EO
// whole: the same body needed the learned strip and the relabeling, in the
// order buildPlan runs them, and the second repair works on the slice the
// first one already replaced. The caller's own request must come through both
// still carrying what it originally said.
func TestCodexRequestSurvivesBothRepairsInPlanOrder(t *testing.T) {
	public := codexResponsesPayload()
	for _, item := range public["input"].([]any) {
		if object, ok := item.(map[string]any); ok && object["role"] != nil {
			object["internal_chat_message_metadata_passthrough"] = map[string]any{"content_item_kinds": []any{"text"}}
		}
	}

	payload := cloneMap(public)
	strip := itemFieldStrip{Root: "input", Field: "internal_chat_message_metadata_passthrough"}
	removed := stripItemFields(payload, []itemFieldStrip{strip})
	applied := relabelConversation(payload, "responses")

	if len(removed) != 1 || applied["input_text"] != "output_text" {
		t.Fatalf("the two repairs did not both act: removed %v, applied %v", removed, applied)
	}
	planItem := payload["input"].([]any)[1].(map[string]any)
	if _, carries := planItem["internal_chat_message_metadata_passthrough"]; carries {
		t.Fatal("the plan still carries the stripped field")
	}
	if got := partType(t, payload, "input", 1, 0); got != "output_text" {
		t.Fatalf("the plan's assistant part went out as %q", got)
	}
	callerItem := public["input"].([]any)[1].(map[string]any)
	if _, carries := callerItem["internal_chat_message_metadata_passthrough"]; !carries {
		t.Fatal("the caller's request lost its field")
	}
	if got := callerItem["content"].([]any)[0].(map[string]any)["type"]; got != "input_text" {
		t.Fatalf("the caller's request was relabeled in place to %q", got)
	}
}

// TestChatToolTurnSurvivesTheAnthropicWire is the production request
// end to end through the translation it actually took: an OpenAI chat body
// whose tool turn carries Responses-vocabulary parts, translated to Anthropic
// — which copies the tool content whole — then relabeled for the wire.
func TestChatToolTurnSurvivesTheAnthropicWire(t *testing.T) {
	chat := map[string]any{
		"model": "claude-fable-5",
		"messages": []any{
			map[string]any{"role": "user", "content": "read the file"},
			map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{"name": "read", "arguments": "{}"},
			}}},
			map[string]any{"role": "tool", "tool_call_id": "call_1", "content": []any{
				map[string]any{"type": "input_text", "text": "file contents"},
			}},
		},
	}
	translated, _, err := translateChatRequestToAnthropic(chat)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	applied := relabelConversation(translated, "anthropic")
	messages := translated["messages"].([]any)
	result := messages[len(messages)-1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" {
		t.Fatalf("the tool turn did not become a tool_result: %v", result)
	}
	inner := result["content"].([]any)[0].(map[string]any)
	if inner["type"] != "text" || inner["text"] != "file contents" {
		t.Fatalf("the tool_result still carries a foreign label: %v", inner)
	}
	if applied["input_text"] != "text" {
		t.Fatalf("the rename is not in the evidence: %v", applied)
	}
}
