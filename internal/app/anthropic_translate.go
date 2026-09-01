package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type anthropicUnsupportedError struct{ Feature string }

func (e anthropicUnsupportedError) Error() string {
	return fmt.Sprintf("%s cannot be translated across provider protocols", e.Feature)
}

// translateAnthropicRequestToChat converts a Messages request into an OpenAI
// Chat request. Anything Chat has no place for is dropped and named in the
// returned slice rather than refused: a caller that sends one Anthropic-only
// field should still get an answer from the model, and the operator can see in
// the request log exactly what did not survive the crossing.
func translateAnthropicRequestToChat(source map[string]any) (map[string]any, []string, error) {
	dropped := unsupportedFields(source, "model", "max_tokens", "messages", "system", "temperature", "top_p", "stop_sequences", "stream", "tools", "tool_choice", "metadata", "thinking", "container", "mcp_servers", "context_management", "output_config")
	chat := map[string]any{}
	for _, field := range []string{"temperature", "top_p", "stream"} {
		if value, ok := source[field]; ok {
			chat[field] = value
		}
	}
	if value, ok := source["max_tokens"]; ok {
		chat["max_tokens"] = value
	}
	if value, ok := source["stop_sequences"]; ok {
		chat["stop"] = value
	}

	messages := make([]any, 0)
	if system, ok := source["system"]; ok {
		text := anthropicText(system)
		if text != "" {
			messages = append(messages, map[string]any{"role": "system", "content": text})
		}
	}
	// An empty conversation is the one thing that cannot be repaired: there is no
	// request left to send once it is dropped.
	rawMessages, ok := source["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, dropped, anthropicUnsupportedError{Feature: "messages"}
	}
	for _, raw := range rawMessages {
		message, ok := raw.(map[string]any)
		if !ok {
			dropped = appendUniqueStrings(dropped, "message")
			continue
		}
		role, _ := message["role"].(string)
		switch role {
		case "system", "developer":
			content, lost := anthropicContentToChat(message["content"])
			dropped = appendUniqueStrings(dropped, lost...)
			messages = append(messages, map[string]any{"role": "system", "content": content})
			continue
		case "tool":
			content, lost := anthropicContentToChat(message["content"])
			dropped = appendUniqueStrings(dropped, lost...)
			toolCallID, _ := message["tool_call_id"].(string)
			if toolCallID == "" {
				toolCallID, _ = message["tool_use_id"].(string)
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": toolCallID, "content": content})
			continue
		case "user", "assistant":
		default:
			// An unrecognised role is carried as a user turn instead of being
			// discarded, because its text is still part of the conversation.
			dropped = appendUniqueStrings(dropped, "message role "+role)
			role = "user"
		}
		if text, ok := message["content"].(string); ok {
			messages = append(messages, map[string]any{"role": role, "content": text})
			continue
		}
		blocks, ok := message["content"].([]any)
		if !ok {
			dropped = appendUniqueStrings(dropped, "message content")
			continue
		}
		parts := make([]any, 0)
		toolCalls := make([]any, 0)
		toolResults := make([]any, 0)
		for _, rawBlock := range blocks {
			block, ok := rawBlock.(map[string]any)
			if !ok {
				dropped = appendUniqueStrings(dropped, "content block")
				continue
			}
			switch block["type"] {
			case "text":
				parts = append(parts, map[string]any{"type": "text", "text": block["text"]})
			case "image":
				image, ok := anthropicImageToChat(block)
				if !ok {
					dropped = appendUniqueStrings(dropped, "image source")
					continue
				}
				parts = append(parts, image)
			case "tool_use":
				encoded, err := json.Marshal(block["input"])
				if err != nil {
					dropped = appendUniqueStrings(dropped, "tool_use input")
					continue
				}
				toolCalls = append(toolCalls, map[string]any{
					"id": block["id"], "type": "function",
					"function": map[string]any{"name": block["name"], "arguments": string(encoded)},
				})
			case "tool_result":
				content, lost := anthropicContentToChat(block["content"])
				dropped = appendUniqueStrings(dropped, lost...)
				toolResults = append(toolResults, map[string]any{
					"role": "tool", "tool_call_id": block["tool_use_id"], "content": content,
				})
			case "thinking", "redacted_thinking":
				// OpenAI chat has no equivalent history block. It is safe to omit
				// this prior reasoning while preserving the assistant's visible output.
			default:
				// A document, a search result or any block a later Anthropic version
				// adds is summarised as whatever text it carries, so the turn keeps
				// its meaning instead of vanishing.
				if text := anthropicText(block); text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": text})
				}
				dropped = appendUniqueStrings(dropped, fmt.Sprint(block["type"]))
			}
		}
		if len(parts) > 0 || len(toolCalls) > 0 {
			content := any(parts)
			if len(parts) == 1 {
				if part, ok := parts[0].(map[string]any); ok && part["type"] == "text" {
					content = part["text"]
				}
			}
			entry := map[string]any{"role": role, "content": content}
			if len(toolCalls) > 0 {
				entry["tool_calls"] = toolCalls
			}
			messages = append(messages, entry)
		}
		messages = append(messages, toolResults...)
	}
	chat["messages"] = messages
	if tools, ok := source["tools"].([]any); ok {
		translated := make([]any, 0, len(tools))
		for _, raw := range tools {
			tool, ok := raw.(map[string]any)
			if !ok {
				dropped = appendUniqueStrings(dropped, "tool")
				continue
			}
			// A server-side tool runs inside Anthropic's own infrastructure, so an
			// OpenAI upstream cannot be asked to call it. The rest of the tool list
			// still reaches the model.
			if kind, _ := tool["type"].(string); kind != "" && kind != "custom" {
				dropped = appendUniqueStrings(dropped, "server tool "+kind)
				continue
			}
			translated = append(translated, map[string]any{
				"type": "function", "function": map[string]any{
					"name": tool["name"], "description": tool["description"], "parameters": tool["input_schema"],
				},
			})
		}
		if len(translated) > 0 {
			chat["tools"] = translated
		}
	}
	if choice, ok := source["tool_choice"].(map[string]any); ok {
		switch choice["type"] {
		case "auto":
			chat["tool_choice"] = "auto"
		case "any":
			chat["tool_choice"] = "required"
		case "tool":
			chat["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": choice["name"]}}
		case "none":
			chat["tool_choice"] = "none"
		default:
			// Letting the model choose is the safe reading of an instruction this
			// gateway does not recognise.
			dropped = appendUniqueStrings(dropped, "tool_choice")
		}
		if disabled, _ := choice["disable_parallel_tool_use"].(bool); disabled {
			chat["parallel_tool_calls"] = false
		}
	}
	// A tool list that did not survive makes tool_choice meaningless, and some
	// upstreams reject the pair.
	if _, hasTools := chat["tools"]; !hasTools {
		delete(chat, "tool_choice")
	}
	return chat, dropped, nil
}

// translateChatRequestToAnthropic converts an OpenAI Chat request into a Messages
// request. Like its mirror image it drops what Anthropic has no place for and
// names it, so a caller using one OpenAI-only knob still gets an answer.
func translateChatRequestToAnthropic(source map[string]any) (map[string]any, []string, error) {
	dropped := unsupportedFields(source, "model", "messages", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "stop", "stream", "stream_options", "tools", "tool_choice", "parallel_tool_calls", "response_format")
	// response_format has no Anthropic equivalent, but asking for JSON is a
	// meaningful instruction, so it is restated to the model in the system prompt
	// rather than silently discarded.
	jsonInstruction := ""
	if format, ok := source["response_format"].(map[string]any); ok {
		switch format["type"] {
		case "json_object":
			jsonInstruction = "Reply with a single valid JSON object and nothing else."
		case "json_schema":
			schema, _ := json.Marshal(format["json_schema"])
			jsonInstruction = "Reply with a single valid JSON object and nothing else, matching this schema: " + string(schema)
		}
	}
	result := map[string]any{}
	for _, field := range []string{"temperature", "top_p", "stream"} {
		if value, ok := source[field]; ok {
			result[field] = value
		}
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens", "max_output_tokens"} {
		if value, ok := source[field]; ok {
			result["max_tokens"] = value
			break
		}
	}
	if stop, ok := source["stop"]; ok {
		if text, ok := stop.(string); ok {
			result["stop_sequences"] = []any{text}
		} else {
			result["stop_sequences"] = stop
		}
	}

	system := make([]string, 0)
	if jsonInstruction != "" {
		system = append(system, jsonInstruction)
	}
	messages := make([]any, 0)
	rawMessages, ok := source["messages"].([]any)
	if !ok || len(rawMessages) == 0 {
		return nil, dropped, anthropicUnsupportedError{Feature: "messages"}
	}
	for _, raw := range rawMessages {
		message, ok := raw.(map[string]any)
		if !ok {
			dropped = appendUniqueStrings(dropped, "message")
			continue
		}
		role, _ := message["role"].(string)
		if role == "system" || role == "developer" {
			system = append(system, openAIText(message["content"]))
			continue
		}
		if role == "tool" {
			messages = append(messages, map[string]any{
				"role": "user", "content": []any{map[string]any{
					"type": "tool_result", "tool_use_id": message["tool_call_id"], "content": message["content"],
				}},
			})
			continue
		}
		if role != "user" && role != "assistant" {
			// The turn's text still belongs in the conversation, so an unrecognised
			// role is carried as a user turn rather than dropped.
			dropped = appendUniqueStrings(dropped, "message role "+role)
			role = "user"
		}
		blocks, lost := openAIContentToAnthropic(message["content"])
		dropped = appendUniqueStrings(dropped, lost...)
		if calls, ok := message["tool_calls"].([]any); ok {
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				input := map[string]any{}
				if arguments, ok := function["arguments"].(string); ok && strings.TrimSpace(arguments) != "" {
					decoder := json.NewDecoder(strings.NewReader(arguments))
					decoder.UseNumber()
					if err := decoder.Decode(&input); err != nil {
						// Malformed arguments came from the model's own earlier turn, so
						// the call is preserved with an empty input rather than failing
						// the whole conversation.
						input = map[string]any{}
						dropped = appendUniqueStrings(dropped, "tool arguments")
					}
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call["id"], "name": function["name"], "input": input,
				})
			}
		}
		// Anthropic rejects a turn with no content at all, so an empty one is
		// skipped instead of being sent.
		if len(blocks) == 0 {
			continue
		}
		messages = append(messages, map[string]any{"role": role, "content": blocks})
	}
	if len(system) > 0 {
		result["system"] = strings.Join(system, "\n\n")
	}
	result["messages"] = messages

	if tools, ok := source["tools"].([]any); ok {
		translated := make([]any, 0, len(tools))
		for _, raw := range tools {
			tool, _ := raw.(map[string]any)
			if tool["type"] != "function" {
				// A hosted OpenAI tool runs in OpenAI's infrastructure and cannot be
				// offered to Claude, but the remaining tools still can.
				dropped = appendUniqueStrings(dropped, "non-function tool")
				continue
			}
			function, _ := tool["function"].(map[string]any)
			translated = append(translated, map[string]any{
				"name": function["name"], "description": function["description"], "input_schema": function["parameters"],
			})
		}
		if len(translated) > 0 {
			result["tools"] = translated
		}
	}
	if choice, ok := source["tool_choice"]; ok {
		switch value := choice.(type) {
		case string:
			switch value {
			case "auto":
				result["tool_choice"] = map[string]any{"type": "auto"}
			case "required":
				result["tool_choice"] = map[string]any{"type": "any"}
			case "none":
				result["tool_choice"] = map[string]any{"type": "none"}
			}
		case map[string]any:
			function, _ := value["function"].(map[string]any)
			result["tool_choice"] = map[string]any{"type": "tool", "name": function["name"]}
		}
	}
	if parallel, ok := source["parallel_tool_calls"].(bool); ok && !parallel {
		choice, _ := result["tool_choice"].(map[string]any)
		if choice == nil {
			choice = map[string]any{"type": "auto"}
		}
		choice["disable_parallel_tool_use"] = true
		result["tool_choice"] = choice
	}
	if _, hasTools := result["tools"]; !hasTools {
		delete(result, "tool_choice")
	}
	return result, dropped, nil
}

// unsupportedFields names every top-level field the target protocol has no place
// for. They are dropped rather than refused: failing a conversation mid-flight
// over one field the caller can live without is the outcome this gateway works
// hardest to avoid, and the names travel to the request log so an operator can
// still see what was left behind.
func unsupportedFields(source map[string]any, allowed ...string) []string {
	allowedSet := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		allowedSet[field] = true
	}
	dropped := make([]string, 0)
	for field, value := range source {
		if !allowedSet[field] && value != nil {
			dropped = append(dropped, field)
		}
	}
	sort.Strings(dropped)
	return dropped
}

// openAIContentToAnthropic converts one OpenAI message's content parts and
// reports what it had to leave behind, so no part type can fail a whole request.
func openAIContentToAnthropic(raw any) ([]any, []string) {
	dropped := make([]string, 0)
	if raw == nil {
		return []any{}, dropped
	}
	if text, ok := raw.(string); ok {
		if text == "" {
			return []any{}, dropped
		}
		return []any{map[string]any{"type": "text", "text": text}}, dropped
	}
	parts, ok := raw.([]any)
	if !ok {
		return []any{map[string]any{"type": "text", "text": fmt.Sprint(raw)}}, dropped
	}
	blocks := make([]any, 0, len(parts))
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		switch part["type"] {
		case "text", "input_text", "output_text":
			blocks = append(blocks, map[string]any{"type": "text", "text": part["text"]})
		case "image_url", "input_image":
			image, ok := part["image_url"].(map[string]any)
			url := ""
			if ok {
				url, _ = image["url"].(string)
			} else {
				url, _ = part["image_url"].(string)
			}
			if strings.HasPrefix(url, "data:") {
				pieces := strings.SplitN(strings.TrimPrefix(url, "data:"), ";base64,", 2)
				if len(pieces) != 2 {
					dropped = appendUniqueStrings(dropped, "image data URL")
					continue
				}
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{
					"type": "base64", "media_type": pieces[0], "data": pieces[1],
				}})
			} else if url != "" {
				blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": url}})
			} else {
				dropped = appendUniqueStrings(dropped, "image URL")
			}
		default:
			// An audio or file part cannot cross to Messages, but whatever text it
			// carries can, so the turn keeps as much meaning as possible.
			if text, ok := part["text"].(string); ok && text != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": text})
			}
			dropped = appendUniqueStrings(dropped, fmt.Sprint(part["type"]))
		}
	}
	return blocks, dropped
}

// anthropicText flattens any Anthropic content shape to plain text. It never
// fails: a block this gateway does not model still usually carries readable text
// somewhere inside it, and returning that is always better for the caller than
// refusing the request over a field name.
func anthropicText(raw any) string {
	switch typed := raw.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, block := range typed {
			if text := anthropicText(block); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := typed["text"].(string); ok {
			return text
		}
		// A document or search-result block nests its readable part one level
		// deeper, so follow the two keys Anthropic uses for it.
		for _, key := range []string{"content", "source"} {
			if text := anthropicText(typed[key]); text != "" {
				return text
			}
		}
		return ""
	default:
		return ""
	}
}

// anthropicContentToChat converts one message's content blocks and reports the
// block types it could not carry across, which the caller folds into the
// request's dropped-field list.
func anthropicContentToChat(raw any) (any, []string) {
	dropped := make([]string, 0)
	if raw == nil {
		return "", dropped
	}
	if text, ok := raw.(string); ok {
		return text, dropped
	}
	blocks, ok := raw.([]any)
	if !ok {
		// A content value of an unexpected shape is still readable as text more
		// often than not, so it is flattened rather than rejected.
		return anthropicText(raw), dropped
	}
	parts := make([]any, 0, len(blocks))
	for _, rawBlock := range blocks {
		block, ok := rawBlock.(map[string]any)
		if !ok {
			dropped = appendUniqueStrings(dropped, "content block")
			continue
		}
		switch block["type"] {
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": block["text"]})
		case "image":
			image, ok := anthropicImageToChat(block)
			if !ok {
				dropped = appendUniqueStrings(dropped, "image source")
				continue
			}
			parts = append(parts, image)
		case "thinking", "redacted_thinking":
			// These are Anthropic-only history blocks and have no OpenAI equivalent.
		case "tool_reference":
			// Deferred tool discovery is an Anthropic-only optimization. Every
			// client function definition is already sent to the OpenAI upstream.
		default:
			if text := anthropicText(block); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
			dropped = appendUniqueStrings(dropped, fmt.Sprint(block["type"]))
		}
	}
	if len(parts) == 0 {
		return "", dropped
	}
	if len(parts) == 1 {
		if part, ok := parts[0].(map[string]any); ok && part["type"] == "text" {
			return part["text"], dropped
		}
	}
	return parts, dropped
}

// anthropicImageToChat reports false when the image source names a transport
// this gateway cannot express as a URL, which is the one case where dropping the
// block is the only honest option.
func anthropicImageToChat(block map[string]any) (map[string]any, bool) {
	source, _ := block["source"].(map[string]any)
	var url string
	switch source["type"] {
	case "base64":
		url = "data:" + fmt.Sprint(source["media_type"]) + ";base64," + fmt.Sprint(source["data"])
	case "url":
		url = fmt.Sprint(source["url"])
	default:
		return nil, false
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}, true
}

// openAIText flattens a system or developer message to plain text. A part shape
// it does not recognise contributes whatever text it holds instead of failing,
// because a system prompt is too important to drop over its packaging.
func openAIText(raw any) string {
	if text, ok := raw.(string); ok {
		return text
	}
	parts, ok := raw.([]any)
	if !ok {
		if raw == nil {
			return ""
		}
		return fmt.Sprint(raw)
	}
	values := make([]string, 0, len(parts))
	for _, rawPart := range parts {
		part, _ := rawPart.(map[string]any)
		if text, ok := part["text"].(string); ok && text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}

func translateAnthropicResponseToChat(body []byte, alias string) ([]byte, int64, int64, error) {
	var message map[string]any
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, 0, 0, err
	}
	// A few nominally Anthropic-compatible gateways return an OpenAI Chat
	// envelope. Preserve that valid response instead of translating it into an
	// empty assistant message.
	if choices, ok := message["choices"].([]any); ok && len(choices) > 0 {
		encoded, input, output := replaceResponseModel(body, alias)
		return encoded, input, output, nil
	}
	// A body with no content field at all is not an Anthropic message — a
	// misbehaving proxy's JSON, most likely — and fabricating an empty
	// completion from it would hide a real fault. A content field that yields
	// no text and no tool call is different, and it happens in production: a
	// thinking model given a hard question and a small output budget spends
	// the whole budget inside its own head and answers with thinking blocks
	// alone, stop_reason max_tokens. That is a real, billed reply — OpenAI's
	// reasoning models produce exactly this shape as an empty message
	// finishing with "length" — so it is translated, not refused. Refusing it
	// manufactured a 502 out of an upstream 200, and the finish reason, which
	// tells the caller it was the budget, never reached them.
	if _, hasContent := message["content"]; !hasContent {
		return nil, 0, 0, fmt.Errorf("response carries no content field, so it is not an Anthropic message")
	}
	content := strings.Builder{}
	toolCalls := make([]any, 0)
	if text, ok := message["content"].(string); ok {
		content.WriteString(text)
	}
	if blocks, ok := message["content"].([]any); ok {
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			switch block["type"] {
			case "text":
				content.WriteString(fmt.Sprint(block["text"]))
			case "tool_use":
				arguments, _ := json.Marshal(block["input"])
				toolCalls = append(toolCalls, map[string]any{
					"id": block["id"], "type": "function",
					"function": map[string]any{"name": block["name"], "arguments": string(arguments)},
				})
			}
		}
	}
	outputMessage := map[string]any{"role": "assistant", "content": content.String()}
	if len(toolCalls) > 0 {
		outputMessage["tool_calls"] = toolCalls
	}
	input, output := extractAnthropicUsage(message)
	response := map[string]any{
		"id": message["id"], "object": "chat.completion", "created": time.Now().Unix(), "model": alias,
		"choices": []any{map[string]any{"index": 0, "message": outputMessage, "finish_reason": anthropicStopToOpenAI(fmt.Sprint(message["stop_reason"]))}},
		"usage":   map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output},
	}
	encoded, err := json.Marshal(response)
	return encoded, input, output, err
}

func translateAnthropicResponseToResponses(body []byte, alias string) ([]byte, int64, int64, error) {
	chat, input, output, err := translateAnthropicResponseToChat(body, alias)
	if err != nil {
		return nil, 0, 0, err
	}
	translated, _, _, err := translateChatResponse(chat, alias)
	return translated, input, output, err
}

func translateChatResponseToAnthropic(body []byte, alias string) ([]byte, int64, int64, error) {
	var chat map[string]any
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, 0, 0, err
	}
	blocks := make([]any, 0)
	stopReason := any(nil)
	if choices, ok := chat["choices"].([]any); ok && len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		if content, ok := message["content"].(string); ok && content != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": content})
		}
		if calls, ok := message["tool_calls"].([]any); ok {
			for _, rawCall := range calls {
				call, _ := rawCall.(map[string]any)
				function, _ := call["function"].(map[string]any)
				input := map[string]any{}
				if arguments, ok := function["arguments"].(string); ok && arguments != "" {
					decoder := json.NewDecoder(strings.NewReader(arguments))
					decoder.UseNumber()
					if decoder.Decode(&input) != nil {
						input = map[string]any{"_raw": arguments}
					}
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call["id"], "name": function["name"], "input": input,
				})
			}
		}
		stopReason = openAIStopToAnthropic(fmt.Sprint(choice["finish_reason"]))
	}
	input, output := extractUsage(chat, "chat")
	id := fmt.Sprint(chat["id"])
	if id == "" || id == "<nil>" {
		id = "msg_" + strings.TrimPrefix(requestLikeID(), "req_")
	}
	message := map[string]any{
		"id": id, "type": "message", "role": "assistant", "model": alias, "content": blocks,
		"stop_reason": stopReason, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": input, "output_tokens": output},
	}
	encoded, err := json.Marshal(message)
	return encoded, input, output, err
}

func replaceAnthropicModel(body []byte, alias string) ([]byte, int64, int64) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body, 0, 0
	}
	payload["model"] = alias
	input, output := extractAnthropicUsage(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, input, output
	}
	return encoded, input, output
}

func extractAnthropicUsage(payload map[string]any) (int64, int64) {
	usage, _ := payload["usage"].(map[string]any)
	input := numberAsInt64(usage["input_tokens"]) + numberAsInt64(usage["cache_creation_input_tokens"]) + numberAsInt64(usage["cache_read_input_tokens"])
	return input, numberAsInt64(usage["output_tokens"])
}

func anthropicStopToOpenAI(reason string) any {
	switch reason {
	case "max_tokens", "model_context_window_exceeded":
		return "length"
	case "tool_use":
		return "tool_calls"
	case "refusal":
		return "content_filter"
	case "", "<nil>":
		return nil
	default:
		return "stop"
	}
}

func openAIStopToAnthropic(reason string) any {
	switch reason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "refusal"
	case "", "<nil>":
		return nil
	default:
		return "end_turn"
	}
}

func rewriteAnthropicStream(source io.Reader, destination io.Writer, alias string, capture *limitedCapture) (string, error) {
	reader := bufio.NewReaderSize(source, 128<<10)
	errorCode := ""
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			out := line
			if bytes.HasPrefix(line, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
				var payload map[string]any
				if json.Unmarshal(data, &payload) == nil {
					if payload["type"] == "message_start" {
						if message, ok := payload["message"].(map[string]any); ok {
							message["model"] = alias
						}
					}
					if payload["type"] == "error" {
						if envelope, ok := payload["error"].(map[string]any); ok {
							errorCode = fmt.Sprint(envelope["type"])
						}
					}
					encoded, _ := json.Marshal(payload)
					out = append([]byte("data: "), encoded...)
					out = append(out, '\n')
				}
			}
			writeRaw(destination, capture, out)
		}
		if err != nil {
			if err == io.EOF {
				return errorCode, nil
			}
			return errorCode, err
		}
	}
}

func prepareAnthropicSSE(source io.Reader) (io.Reader, error) {
	reader := bufio.NewReaderSize(source, 128<<10)
	prefix := &bytes.Buffer{}
	for prefix.Len() <= 256<<10 {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			_, _ = prefix.Write(line)
			trimmed := bytes.TrimSpace(line)
			if bytes.HasPrefix(trimmed, []byte("data:")) {
				data := bytes.TrimSpace(bytes.TrimPrefix(trimmed, []byte("data:")))
				var event map[string]any
				if json.Unmarshal(data, &event) == nil {
					eventType := strings.TrimSpace(fmt.Sprint(event["type"]))
					if eventType == "error" {
						return nil, fmt.Errorf("Anthropic error event arrived before the response stream started")
					}
					if eventType != "" && eventType != "<nil>" {
						return io.MultiReader(bytes.NewReader(prefix.Bytes()), reader), nil
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("Anthropic SSE prelude exceeded 256 KiB")
}

// anthropicJSONToSSE repairs providers that accept stream=true but return a
// regular Anthropic Message object. Keeping this normalization at the protocol
// boundary lets every public mode reuse the same tested streaming translators.
func anthropicJSONToSSE(body []byte) ([]byte, error) {
	var message map[string]any
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, err
	}
	if message["type"] != "message" {
		return nil, fmt.Errorf("expected Anthropic Message, got %v", message["type"])
	}

	output := &bytes.Buffer{}
	start := cloneMap(message)
	blocks, _ := message["content"].([]any)
	start["content"] = []any{}
	if usage, ok := start["usage"].(map[string]any); ok {
		start["usage"] = map[string]any{
			"input_tokens":                usage["input_tokens"],
			"cache_creation_input_tokens": usage["cache_creation_input_tokens"],
			"cache_read_input_tokens":     usage["cache_read_input_tokens"],
			"output_tokens":               0,
		}
	}
	writeSSE(output, nil, "message_start", map[string]any{"type": "message_start", "message": start})

	for index, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid Anthropic content block")
		}
		switch block["type"] {
		case "text":
			writeSSE(output, nil, "content_block_start", map[string]any{
				"type": "content_block_start", "index": index,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			writeSSE(output, nil, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "text_delta", "text": block["text"]},
			})
		case "tool_use":
			writeSSE(output, nil, "content_block_start", map[string]any{
				"type": "content_block_start", "index": index,
				"content_block": map[string]any{"type": "tool_use", "id": block["id"], "name": block["name"], "input": map[string]any{}},
			})
			arguments, err := json.Marshal(block["input"])
			if err != nil {
				return nil, err
			}
			writeSSE(output, nil, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": index,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": string(arguments)},
			})
		default:
			writeSSE(output, nil, "content_block_start", map[string]any{
				"type": "content_block_start", "index": index, "content_block": block,
			})
		}
		writeSSE(output, nil, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
	}

	_, outputTokens := extractAnthropicUsage(message)
	writeSSE(output, nil, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": message["stop_reason"], "stop_sequence": message["stop_sequence"]},
		"usage": map[string]any{"output_tokens": outputTokens},
	})
	writeSSE(output, nil, "message_stop", map[string]any{"type": "message_stop"})
	return output.Bytes(), nil
}

type anthropicStreamStats struct {
	InputTokens  int64
	OutputTokens int64
	ContentParts int
	SawStop      bool
}

func translateAnthropicStreamToOpenAI(source io.Reader, destination io.Writer, alias string, capture *limitedCapture, includeUsage bool, stats *anthropicStreamStats) (string, error) {
	reader := bufio.NewReaderSize(source, 128<<10)
	id := "chatcmpl_" + strings.TrimPrefix(requestLikeID(), "req_")
	created := time.Now().Unix()
	errorCode := ""
	var inputTokens, outputTokens int64
	contentParts := 0
	outputBytes := 0
	sawStop := false
	defer func() {
		if outputTokens == 0 && outputBytes > 0 {
			outputTokens = int64((outputBytes + 3) / 4)
		}
		if stats != nil {
			stats.InputTokens = inputTokens
			stats.OutputTokens = outputTokens
			stats.ContentParts = contentParts
			stats.SawStop = sawStop
		}
	}()
	for {
		line, err := reader.ReadString('\n')
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(trimmedLine, "data:"))
			var event map[string]any
			if json.Unmarshal([]byte(data), &event) == nil {
				delta := map[string]any{}
				finish := any(nil)
				switch event["type"] {
				case "message_start":
					if message, ok := event["message"].(map[string]any); ok {
						if value := fmt.Sprint(message["id"]); value != "" && value != "<nil>" {
							id = value
						}
						if usage, ok := message["usage"].(map[string]any); ok {
							inputTokens = numberAsInt64(usage["input_tokens"]) + numberAsInt64(usage["cache_creation_input_tokens"]) + numberAsInt64(usage["cache_read_input_tokens"])
							outputTokens = numberAsInt64(usage["output_tokens"])
						}
					}
					delta["role"] = "assistant"
				case "content_block_start":
					block, _ := event["content_block"].(map[string]any)
					if block["type"] == "tool_use" {
						delta["tool_calls"] = []any{map[string]any{
							"index": event["index"], "id": block["id"], "type": "function",
							"function": map[string]any{"name": block["name"], "arguments": ""},
						}}
						contentParts++
					} else if text, ok := block["text"].(string); ok && text != "" {
						delta["content"] = text
						contentParts++
						outputBytes += len(text)
					} else if thinking, ok := block["thinking"].(string); ok && thinking != "" {
						delta["reasoning_content"] = thinking
						contentParts++
						outputBytes += len(thinking)
					}
				case "content_block_delta":
					blockDelta, _ := event["delta"].(map[string]any)
					switch blockDelta["type"] {
					case "text_delta":
						if text, ok := blockDelta["text"].(string); ok && text != "" {
							delta["content"] = text
							contentParts++
							outputBytes += len(text)
						}
					case "thinking_delta":
						delta["reasoning_content"] = blockDelta["thinking"]
						contentParts++
						outputBytes += len(fmt.Sprint(blockDelta["thinking"]))
					case "input_json_delta":
						delta["tool_calls"] = []any{map[string]any{
							"index": event["index"], "function": map[string]any{"arguments": blockDelta["partial_json"]},
						}}
						contentParts++
						outputBytes += len(fmt.Sprint(blockDelta["partial_json"]))
					}
					if _, exists := delta["content"]; !exists {
						if text, ok := blockDelta["text"].(string); ok && text != "" {
							delta["content"] = text
							contentParts++
							outputBytes += len(text)
						}
					}
				case "completion":
					if text, ok := event["completion"].(string); ok && text != "" {
						delta["content"] = text
						contentParts++
						outputBytes += len(text)
					}
				case "message_delta":
					top, _ := event["delta"].(map[string]any)
					finish = anthropicStopToOpenAI(fmt.Sprint(top["stop_reason"]))
					if usage, ok := event["usage"].(map[string]any); ok {
						if value := numberAsInt64(usage["input_tokens"]); value > 0 {
							inputTokens = value + numberAsInt64(usage["cache_creation_input_tokens"]) + numberAsInt64(usage["cache_read_input_tokens"])
						}
						if value := numberAsInt64(usage["output_tokens"]); value >= 0 {
							outputTokens = value
						}
					}
				case "error":
					envelope, _ := event["error"].(map[string]any)
					errorCode = fmt.Sprint(envelope["type"])
					payload := map[string]any{"error": map[string]any{"type": errorCode, "code": errorCode, "message": envelope["message"]}}
					encoded, _ := json.Marshal(payload)
					writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
					continue
				case "message_stop":
					sawStop = true
					if contentParts == 0 && errorCode == "" {
						errorCode = "upstream_stream_empty"
						payload := map[string]any{"error": map[string]any{
							"type": errorCode, "code": errorCode,
							"message": "The upstream completed without any text or tool output.",
						}}
						encoded, _ := json.Marshal(payload)
						writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
					}
					if includeUsage {
						usageChunk := map[string]any{
							"id": id, "object": "chat.completion.chunk", "created": created, "model": alias,
							"choices": []any{},
							"usage":   map[string]any{"prompt_tokens": inputTokens, "completion_tokens": outputTokens, "total_tokens": inputTokens + outputTokens},
						}
						encoded, _ := json.Marshal(usageChunk)
						writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
					}
					writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
					continue
				default:
					continue
				}
				chunk := map[string]any{
					"id": id, "object": "chat.completion.chunk", "created": created, "model": alias,
					"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
				}
				encoded, _ := json.Marshal(chunk)
				writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
			}
		}
		if err != nil {
			if err == io.EOF {
				if !sawStop && errorCode == "" {
					return "stream_interrupted", io.ErrUnexpectedEOF
				}
				return errorCode, nil
			}
			return errorCode, err
		}
	}
}

func translateAnthropicStreamToResponses(source io.Reader, destination io.Writer, alias string, capture *limitedCapture) (string, error) {
	reader, writer := io.Pipe()
	type result struct {
		code string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		code, err := translateAnthropicStreamToOpenAI(source, writer, alias, nil, false, nil)
		_ = writer.CloseWithError(err)
		completed <- result{code: code, err: err}
	}()
	err := translateChatStream(reader, destination, alias, capture)
	upstream := <-completed
	if err != nil {
		return upstream.code, err
	}
	return upstream.code, upstream.err
}

func translateOpenAIStreamToAnthropic(source io.Reader, destination io.Writer, alias string, capture *limitedCapture) (string, error) {
	id := "msg_" + strings.TrimPrefix(requestLikeID(), "req_")
	writeSSE(destination, capture, "message_start", map[string]any{
		"type": "message_start", "message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "content": []any{}, "model": alias,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})
	reader := bufio.NewReaderSize(source, 128<<10)
	textIndex := -1
	openBlocks := map[int]bool{}
	toolBlockIndex := map[int]int{}
	nextIndex := 0
	errorCode := ""
	finishReason := any(nil)
	inputTokens, outputTokens := int64(0), int64(0)
	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			var chunk map[string]any
			if json.Unmarshal([]byte(data), &chunk) == nil {
				if envelope, ok := chunk["error"].(map[string]any); ok {
					errorCode = fmt.Sprint(envelope["type"])
					writeSSE(destination, capture, "error", map[string]any{"type": "error", "error": envelope})
					continue
				}
				if usage, ok := chunk["usage"].(map[string]any); ok {
					inputTokens = numberAsInt64(usage["prompt_tokens"])
					outputTokens = numberAsInt64(usage["completion_tokens"])
				}
				choices, _ := chunk["choices"].([]any)
				if len(choices) == 0 {
					continue
				}
				choice, _ := choices[0].(map[string]any)
				delta, _ := choice["delta"].(map[string]any)
				if text, ok := delta["content"].(string); ok && text != "" {
					if textIndex < 0 {
						textIndex = nextIndex
						nextIndex++
						openBlocks[textIndex] = true
						writeSSE(destination, capture, "content_block_start", map[string]any{
							"type": "content_block_start", "index": textIndex, "content_block": map[string]any{"type": "text", "text": ""},
						})
					}
					writeSSE(destination, capture, "content_block_delta", map[string]any{
						"type": "content_block_delta", "index": textIndex, "delta": map[string]any{"type": "text_delta", "text": text},
					})
				}
				if calls, ok := delta["tool_calls"].([]any); ok {
					for _, rawCall := range calls {
						call, _ := rawCall.(map[string]any)
						upstreamIndex := int(numberAsInt64(call["index"]))
						blockIndex, exists := toolBlockIndex[upstreamIndex]
						function, _ := call["function"].(map[string]any)
						if !exists {
							blockIndex = nextIndex
							nextIndex++
							toolBlockIndex[upstreamIndex] = blockIndex
							openBlocks[blockIndex] = true
							writeSSE(destination, capture, "content_block_start", map[string]any{
								"type": "content_block_start", "index": blockIndex, "content_block": map[string]any{
									"type": "tool_use", "id": call["id"], "name": function["name"], "input": map[string]any{},
								},
							})
						}
						if arguments, ok := function["arguments"].(string); ok && arguments != "" {
							writeSSE(destination, capture, "content_block_delta", map[string]any{
								"type": "content_block_delta", "index": blockIndex,
								"delta": map[string]any{"type": "input_json_delta", "partial_json": arguments},
							})
						}
					}
				}
				if reason := fmt.Sprint(choice["finish_reason"]); reason != "" && reason != "<nil>" {
					finishReason = openAIStopToAnthropic(reason)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				return errorCode, err
			}
			break
		}
	}
	for index := 0; index < nextIndex; index++ {
		if openBlocks[index] {
			writeSSE(destination, capture, "content_block_stop", map[string]any{"type": "content_block_stop", "index": index})
		}
	}
	writeSSE(destination, capture, "message_delta", map[string]any{
		"type": "message_delta", "delta": map[string]any{"stop_reason": finishReason, "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	})
	writeSSE(destination, capture, "message_stop", map[string]any{"type": "message_stop"})
	return errorCode, nil
}

func requestLikeID() string {
	id, err := newID("req")
	if err != nil {
		return fmt.Sprintf("req_%d", time.Now().UnixNano())
	}
	return id
}
