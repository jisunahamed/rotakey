package app

import (
	"encoding/json"
	"strings"
)

const emptyToolArguments = "{}"

// normalizeInvalidToolArguments repairs replayed model tool calls before they
// reach an upstream validator. Both Chat and Responses define arguments as a
// JSON-object string, but clients commonly replay a model's truncated, empty or
// fenced argument text on the next turn. Providers that validate strictly reject
// the entire conversation before generating a response. The completed call's
// result is already present in the history, so an empty object preserves the
// call/result pairing without inventing arguments or dropping conversation.
func normalizeInvalidToolArguments(payload map[string]any) []string {
	changed := []string{}
	if messages, ok := payload["messages"].([]any); ok {
		for _, rawMessage := range messages {
			message, _ := rawMessage.(map[string]any)
			calls, _ := message["tool_calls"].([]any)
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				if function == nil {
					continue
				}
				if normalized, update := normalizedToolArgumentValue(function["arguments"]); update {
					function["arguments"] = normalized
					changed = appendUniqueStrings(changed, "messages[].tool_calls[].function.arguments")
				}
			}
		}
	}
	if input, ok := payload["input"].([]any); ok {
		for _, rawItem := range input {
			item, _ := rawItem.(map[string]any)
			if item == nil || item["type"] != "function_call" {
				continue
			}
			if normalized, update := normalizedToolArgumentValue(item["arguments"]); update {
				item["arguments"] = normalized
				changed = appendUniqueStrings(changed, "input[].arguments")
			}
		}
	}
	return changed
}

func normalizedToolArgumentValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		if validToolArgumentObject(typed) {
			return typed, false
		}
		// Some model endpoints wrap otherwise valid arguments in a Markdown fence.
		// Recover that exact object before falling back to an empty one.
		if unfenced := stripJSONFence(typed); unfenced != typed && validToolArgumentObject(unfenced) {
			return unfenced, true
		}
		return emptyToolArguments, true
	case map[string]any:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return string(encoded), true
		}
		return emptyToolArguments, true
	default:
		return emptyToolArguments, true
	}
}

func validToolArgumentObject(value string) bool {
	var object map[string]any
	return strings.TrimSpace(value) != "" && json.Unmarshal([]byte(value), &object) == nil && object != nil
}

func stripJSONFence(value string) string {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return value
	}
	trimmed = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "```"), "```"))
	if line, rest, found := strings.Cut(trimmed, "\n"); found && strings.EqualFold(strings.TrimSpace(line), "json") {
		trimmed = strings.TrimSpace(rest)
	}
	return trimmed
}
