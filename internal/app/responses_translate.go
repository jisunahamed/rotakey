package app

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

// chatFieldsWithResponsesEquivalent lists the Chat Completions request fields
// this translator can express on the Responses API. Anything outside the set is
// dropped and reported instead of rejected: a route whose upstream only serves
// /responses must still answer a Chat-shaped call, and a slightly degraded
// answer is always better than the HTTP 400 this replaces.
var chatFieldsWithResponsesEquivalent = []string{
	"model", "messages",
	"temperature", "top_p", "stream", "parallel_tool_calls",
	"max_tokens", "max_completion_tokens", "max_output_tokens",
	"tools", "tool_choice", "response_format", "reasoning_effort",
	"metadata", "store", "user",
}

// translateChatRequestToResponses reshapes a Chat Completions body for the
// Responses endpoint. The second return value names every top-level field that
// had to be discarded, which the dispatcher folds into the request log's removed
// parameters so an operator can explain a behavioral difference without guessing.
func translateChatRequestToResponses(source map[string]any) (map[string]any, []string, error) {
	rawMessages, exists := source["messages"]
	if !exists {
		return nil, nil, fmt.Errorf("a Chat Completions request needs a messages array")
	}
	messages, ok := rawMessages.([]any)
	if !ok {
		return nil, nil, fmt.Errorf("Chat Completions messages must be an array")
	}

	translated := map[string]any{}
	removed := make([]string, 0)
	instructions := make([]string, 0)
	input := make([]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("every Chat Completions message must be an object")
		}
		role, _ := message["role"].(string)
		switch role {
		case "system", "developer":
			// Responses carries the system prompt out of band in instructions.
			// Several system turns are joined rather than reduced to the last one so
			// a caller that splits its prompt across messages keeps every rule.
			if text := chatContentAsText(message["content"]); strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
		case "tool", "function":
			callID, _ := message["tool_call_id"].(string)
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID, "output": message["content"],
			})
		default:
			if role == "" {
				role = "user"
			}
			// Responses distinguishes what was sent to the model from what the model
			// produced, so replayed assistant turns must use output_text.
			kind := "input_text"
			if role == "assistant" {
				kind = "output_text"
			}
			if parts := chatContentToResponsesParts(message["content"], kind); len(parts) > 0 {
				input = append(input, map[string]any{"role": role, "content": parts})
			}
			// Tool calls are top-level items in Responses rather than a field on the
			// assistant turn, so they follow the visible text of the same turn.
			if calls, ok := message["tool_calls"].([]any); ok {
				for _, rawCall := range calls {
					call, _ := rawCall.(map[string]any)
					function, _ := call["function"].(map[string]any)
					callID, _ := call["id"].(string)
					input = append(input, map[string]any{
						"type": "function_call", "call_id": callID,
						"name": function["name"], "arguments": function["arguments"],
					})
				}
			}
		}
	}
	if len(instructions) > 0 {
		translated["instructions"] = strings.Join(instructions, "\n")
	}
	translated["input"] = input

	for _, field := range []string{"temperature", "top_p", "stream", "parallel_tool_calls"} {
		if value, exists := source[field]; exists {
			translated[field] = value
		}
	}
	// Both Chat spellings of the output cap mean max_output_tokens here. The
	// newer max_completion_tokens wins when a client sends more than one.
	for _, field := range []string{"max_completion_tokens", "max_output_tokens", "max_tokens"} {
		if value, exists := source[field]; exists && value != nil {
			translated["max_output_tokens"] = value
			break
		}
	}
	// These three are spelled and typed identically on both APIs, so forwarding
	// them keeps a caller's own bookkeeping intact across the translation.
	for _, field := range []string{"metadata", "store", "user"} {
		if value, exists := source[field]; exists {
			translated[field] = value
		}
	}
	// Chat's flat reasoning_effort is the Responses reasoning.effort knob under a
	// different name, and silently dropping it would change how a model answers.
	if effort, exists := source["reasoning_effort"]; exists && effort != nil {
		translated["reasoning"] = map[string]any{"effort": effort}
	}

	if tools, ok := source["tools"].([]any); ok {
		flattened := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, isObject := rawTool.(map[string]any)
			function, isFunction := tool["function"].(map[string]any)
			if !isObject || tool["type"] != "function" || !isFunction {
				// Custom and hosted tool shapes have no Chat-to-Responses mapping.
				// Dropping the single entry leaves the caller's other tools usable.
				removed = appendUniqueStrings(removed, "tools")
				continue
			}
			entry := map[string]any{
				"type": "function", "name": function["name"],
				"description": function["description"], "parameters": function["parameters"],
			}
			if strict, exists := function["strict"]; exists {
				entry["strict"] = strict
			}
			flattened = append(flattened, entry)
		}
		translated["tools"] = flattened
	}
	if choice, exists := source["tool_choice"]; exists {
		translated["tool_choice"] = choice
	}
	// This is the exact inverse of the text.format handling in
	// translateResponsesRequest, so structured-output requests survive a round
	// trip through either direction unchanged.
	if value, exists := source["response_format"]; exists && value != nil {
		format, isObject := value.(map[string]any)
		switch {
		case !isObject:
			removed = appendUniqueStrings(removed, "response_format")
		case format["type"] == "text":
			translated["text"] = map[string]any{"format": map[string]any{"type": "text"}}
		case format["type"] == "json_object":
			translated["text"] = map[string]any{"format": map[string]any{"type": "json_object"}}
		case format["type"] == "json_schema":
			schema, _ := format["json_schema"].(map[string]any)
			inner := map[string]any{"type": "json_schema", "name": schema["name"], "schema": schema["schema"]}
			if strict, exists := schema["strict"]; exists {
				inner["strict"] = strict
			}
			translated["text"] = map[string]any{"format": inner}
		default:
			removed = appendUniqueStrings(removed, "response_format")
		}
	}

	// Unknown keys are named and dropped rather than forwarded, because a strict
	// Responses implementation rejects the whole request over one stray field.
	for field, value := range source {
		if value == nil || slices.Contains(chatFieldsWithResponsesEquivalent, field) {
			continue
		}
		removed = appendUniqueStrings(removed, field)
	}
	slices.Sort(removed)
	return translated, removed, nil
}

// chatContentAsText flattens Chat message content to plain text and ignores
// parts that carry none. It cannot fail, so a system turn holding something
// exotic still contributes its wording instead of failing the request.
func chatContentAsText(raw any) string {
	if text, ok := raw.(string); ok {
		return text
	}
	parts, ok := raw.([]any)
	if !ok {
		return ""
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

// chatContentToResponsesParts converts Chat content into Responses content
// parts. textType decides between input_text and output_text, which is the only
// difference between a replayed user turn and a replayed assistant turn.
func chatContentToResponsesParts(raw any, textType string) []any {
	if text, ok := raw.(string); ok {
		if text == "" {
			return nil
		}
		return []any{map[string]any{"type": textType, "text": text}}
	}
	parts, ok := raw.([]any)
	if !ok {
		return nil
	}
	translated := make([]any, 0, len(parts))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		switch part["type"] {
		case "text", "input_text", "output_text":
			translated = append(translated, map[string]any{"type": textType, "text": part["text"]})
		case "image_url", "input_image":
			// Responses takes the URL inline where Chat nests it in an object, and
			// clients mix both spellings, so either shape is accepted here.
			if image, ok := part["image_url"].(map[string]any); ok {
				translated = append(translated, map[string]any{"type": "input_image", "image_url": image["url"]})
			} else if url, ok := part["image_url"].(string); ok && url != "" {
				translated = append(translated, map[string]any{"type": "input_image", "image_url": url})
			}
		default:
			// Audio, file and refusal parts have no Responses spelling this gateway
			// can forward. Skipping the part keeps the rest of the turn intact.
		}
	}
	if len(translated) == 0 {
		return nil
	}
	return translated
}

// translateResponsesResponseToChat rebuilds a Chat Completions envelope from a
// Responses body so a Chat caller never learns that its route ran on /responses.
func translateResponsesResponseToChat(body []byte, alias string) ([]byte, int64, int64, error) {
	var source map[string]any
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, 0, 0, err
	}
	// Some gateways answer /responses with an OpenAI Chat envelope. That body is
	// already what the caller wants, so it is relabelled rather than walked for
	// Responses items it will never contain.
	if choices, ok := source["choices"].([]any); ok && len(choices) > 0 {
		encoded, input, output := replaceResponseModel(body, alias)
		return encoded, input, output, nil
	}

	content := strings.Builder{}
	toolCalls := make([]any, 0)
	items, _ := source["output"].([]any)
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		switch item["type"] {
		case "message":
			parts, _ := item["content"].([]any)
			for _, rawPart := range parts {
				part, _ := rawPart.(map[string]any)
				if part["type"] != "output_text" {
					continue
				}
				if text, ok := part["text"].(string); ok {
					content.WriteString(text)
				}
			}
		case "function_call":
			callID, _ := item["call_id"].(string)
			if callID == "" {
				callID, _ = item["id"].(string)
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": callID, "type": "function",
				"function": map[string]any{"name": item["name"], "arguments": item["arguments"]},
			})
		default:
			// Reasoning items and hosted-tool traces have no Chat field at all.
			// Dropping them costs a Chat client nothing, since it could never have
			// received them from a native Chat upstream either.
		}
	}
	// A provider that fills only the convenience output_text field still produced
	// a usable answer, and recovering it there beats returning an empty message.
	if content.Len() == 0 {
		if text, ok := source["output_text"].(string); ok {
			content.WriteString(text)
		}
	}

	input, output := extractUsage(source, "responses")
	if input == 0 && output == 0 {
		// A gateway that labels its usage block the Chat way still reported real
		// numbers, and both billing and the rate limiter depend on them.
		input, output = extractUsage(source, "chat")
	}
	id := fmt.Sprint(source["id"])
	if id == "" || id == "<nil>" {
		id = "chatcmpl_" + strings.TrimPrefix(requestLikeID(), "req_")
	}
	message := map[string]any{"role": "assistant", "content": content.String()}
	if len(toolCalls) > 0 {
		message["tool_calls"] = toolCalls
	}
	// An empty output is deliberately not an error. This translator runs after a
	// 2xx, so failing here would turn a thin-but-valid upstream answer into a
	// terminal 502, which is the failure mode this whole path exists to remove.
	chat := map[string]any{
		"id": id, "object": "chat.completion", "created": time.Now().Unix(), "model": alias,
		"choices": []any{map[string]any{
			"index": 0, "message": message,
			"finish_reason": responsesStatusToOpenAIStop(source, len(toolCalls) > 0),
		}},
		"usage": map[string]any{"prompt_tokens": input, "completion_tokens": output, "total_tokens": input + output},
	}
	encoded, err := json.Marshal(chat)
	return encoded, input, output, err
}

// responsesStatusToOpenAIStop maps a Responses terminal status onto a Chat
// finish_reason. Truncation outranks tool calls, because a client that retries
// on "length" needs to hear about the cut-off even when the truncated text was
// the beginning of a tool call. An unknown status becomes "stop" rather than
// null, since Chat clients routinely branch on this field.
func responsesStatusToOpenAIStop(source map[string]any, hasToolCalls bool) string {
	reason := ""
	if details, ok := source["incomplete_details"].(map[string]any); ok {
		reason, _ = details["reason"].(string)
	}
	switch {
	case strings.Contains(reason, "token"):
		return "length"
	case strings.Contains(reason, "content_filter"), strings.Contains(reason, "refusal"):
		return "content_filter"
	case hasToolCalls:
		return "tool_calls"
	default:
		return "stop"
	}
}

// translateResponsesResponseToAnthropic chains through the Chat envelope the way
// translateAnthropicResponseToResponses does. Responses and Anthropic share no
// field names, but the Chat shape in the middle already carries everything both
// sides need, so one tested mapping for tool calls and stop reasons is reused
// instead of growing a second one that could drift from it.
func translateResponsesResponseToAnthropic(body []byte, alias string) ([]byte, int64, int64, error) {
	chat, input, output, err := translateResponsesResponseToChat(body, alias)
	if err != nil {
		return nil, 0, 0, err
	}
	translated, _, _, err := translateChatResponseToAnthropic(chat, alias)
	return translated, input, output, err
}

// translateResponsesStreamToChat rewrites a Responses SSE stream as Chat
// Completions chunks. The returned code names an upstream stream fault when there
// was one, matching the contract of the other stream translators: an empty string
// means the stream completed and the client already holds a full answer.
func translateResponsesStreamToChat(source io.Reader, destination io.Writer, alias string, capture *limitedCapture, includeUsage bool) (string, error) {
	reader := bufio.NewReaderSize(source, 128<<10)
	id := "chatcmpl_" + strings.TrimPrefix(requestLikeID(), "req_")
	created := time.Now().Unix()
	// Responses identifies a tool call by item id; Chat identifies it by a dense
	// index within the choice. The map keeps one stable index per call so argument
	// deltas that arrive interleaved still land on the right call.
	toolIndex := map[string]int{}
	nextToolIndex := 0
	roleSent := false
	sawContent := false
	sawTerminal := false
	errorCode := ""
	inputTokens, outputTokens := int64(0), int64(0)

	emit := func(delta map[string]any, finish any) {
		chunk := map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": alias,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		encoded, _ := json.Marshal(chunk)
		writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
	}
	emitRole := func() {
		if roleSent {
			return
		}
		roleSent = true
		emit(map[string]any{"role": "assistant"}, nil)
	}
	emitError := func(code, message string) {
		errorCode = code
		payload := map[string]any{"error": map[string]any{"type": code, "code": code, "message": message}}
		encoded, _ := json.Marshal(payload)
		writeRaw(destination, capture, append(append([]byte("data: "), encoded...), []byte("\n\n")...))
	}
	finish := func(reason string) {
		emitRole()
		emit(map[string]any{}, reason)
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
	}

	for {
		line, err := reader.ReadString('\n')
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				if !sawTerminal {
					sawTerminal = true
					finish(chatStopReasonFor(nextToolIndex > 0))
				}
				return errorCode, nil
			}
			var event map[string]any
			if json.Unmarshal([]byte(data), &event) != nil {
				emitError("upstream_stream_invalid", "The upstream returned malformed streaming JSON.")
				writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
				return errorCode, fmt.Errorf("malformed upstream SSE JSON")
			}
			switch event["type"] {
			case "response.output_text.delta":
				if text, ok := event["delta"].(string); ok && text != "" {
					emitRole()
					sawContent = true
					emit(map[string]any{"content": text}, nil)
				}
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				// Chat has no standard reasoning field, but the OpenAI-compatible
				// ecosystem settled on reasoning_content, and clients that ignore it
				// are unaffected.
				if text, ok := event["delta"].(string); ok && text != "" {
					emitRole()
					sawContent = true
					emit(map[string]any{"reasoning_content": text}, nil)
				}
			case "response.output_item.added":
				item, _ := event["item"].(map[string]any)
				if item["type"] != "function_call" {
					continue
				}
				key := responsesItemKey(item, event)
				if _, exists := toolIndex[key]; exists {
					continue
				}
				index := nextToolIndex
				nextToolIndex++
				toolIndex[key] = index
				callID, _ := item["call_id"].(string)
				if callID == "" {
					callID, _ = item["id"].(string)
				}
				emitRole()
				sawContent = true
				emit(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "id": callID, "type": "function",
					"function": map[string]any{"name": item["name"], "arguments": ""},
				}}}, nil)
			case "response.function_call_arguments.delta":
				arguments, _ := event["delta"].(string)
				if arguments == "" {
					continue
				}
				key := responsesItemKey(nil, event)
				index, exists := toolIndex[key]
				if !exists {
					// Some providers stream argument deltas without ever announcing the
					// item. Opening the call here keeps its arguments rather than
					// discarding a tool the caller is waiting on.
					index = nextToolIndex
					nextToolIndex++
					toolIndex[key] = index
					emitRole()
					emit(map[string]any{"tool_calls": []any{map[string]any{
						"index": index, "id": key, "type": "function",
						"function": map[string]any{"name": "", "arguments": ""},
					}}}, nil)
				}
				emitRole()
				sawContent = true
				emit(map[string]any{"tool_calls": []any{map[string]any{
					"index": index, "function": map[string]any{"arguments": arguments},
				}}}, nil)
			case "response.completed", "response.incomplete":
				if response, ok := event["response"].(map[string]any); ok {
					if in, out := extractUsage(response, "responses"); in > 0 || out > 0 {
						inputTokens, outputTokens = in, out
					}
					if !sawContent {
						emitError("upstream_stream_empty", "The upstream completed without any text or tool output.")
					}
					sawTerminal = true
					finish(responsesStatusToOpenAIStop(response, nextToolIndex > 0))
					return errorCode, nil
				}
				sawTerminal = true
				finish(chatStopReasonFor(nextToolIndex > 0))
				return errorCode, nil
			case "response.failed", "error":
				message := "The upstream reported an error while streaming."
				code := "upstream_stream_error"
				envelope, _ := event["error"].(map[string]any)
				if response, ok := event["response"].(map[string]any); ok {
					if inner, ok := response["error"].(map[string]any); ok {
						envelope = inner
					}
				}
				if envelope != nil {
					if value := fmt.Sprint(envelope["code"]); value != "" && value != "<nil>" {
						code = value
					} else if value := fmt.Sprint(envelope["type"]); value != "" && value != "<nil>" {
						code = value
					}
					if value, ok := envelope["message"].(string); ok && value != "" {
						message = value
					}
				}
				emitError(code, message)
				writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
				return errorCode, nil
			}
		}
		if err != nil {
			if err != io.EOF {
				return errorCode, err
			}
			if sawTerminal {
				return errorCode, nil
			}
			if !sawContent && errorCode == "" {
				emitError("stream_interrupted", "The upstream stream ended before completion.")
				writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
				return errorCode, io.ErrUnexpectedEOF
			}
			// Content already reached the client, so the stream is closed cleanly
			// rather than followed by an error the client cannot act on.
			finish(chatStopReasonFor(nextToolIndex > 0))
			return errorCode, nil
		}
	}
}

// responsesItemKey names the streaming item an event belongs to, preferring the
// call id so an argument delta and its announcement agree even when a provider
// only populates one of the two identifiers.
func responsesItemKey(item map[string]any, event map[string]any) string {
	for _, candidate := range []any{item["call_id"], event["call_id"], item["id"], event["item_id"]} {
		if value, ok := candidate.(string); ok && value != "" {
			return value
		}
	}
	return fmt.Sprint(event["output_index"])
}

func chatStopReasonFor(hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_calls"
	}
	return "stop"
}

// translateResponsesStreamToAnthropic crosses through the Chat stream shape, the
// same way translateAnthropicStreamToResponses does in the other direction. The
// Chat-to-Anthropic stream mapping already handles block indexing and stop
// reasons, so reusing it avoids a second implementation that could drift.
func translateResponsesStreamToAnthropic(source io.Reader, destination io.Writer, alias string, capture *limitedCapture) (string, error) {
	reader, writer := io.Pipe()
	type result struct {
		code string
		err  error
	}
	completed := make(chan result, 1)
	go func() {
		code, err := translateResponsesStreamToChat(source, writer, alias, nil, true)
		_ = writer.CloseWithError(err)
		completed <- result{code: code, err: err}
	}()
	code, err := translateOpenAIStreamToAnthropic(reader, destination, alias, capture)
	_, _ = io.Copy(io.Discard, reader)
	upstream := <-completed
	if err != nil {
		return upstream.code, err
	}
	if upstream.err != nil {
		return upstream.code, upstream.err
	}
	if upstream.code != "" {
		return upstream.code, nil
	}
	return code, nil
}
