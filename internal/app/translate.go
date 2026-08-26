package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
)

type unsupportedFeatureError struct {
	Feature string
}

func (e unsupportedFeatureError) Error() string {
	return fmt.Sprintf("%s is not supported when translating Responses API requests", e.Feature)
}

// translateResponsesRequest converts a Responses request into a Chat request.
// Fields Chat cannot express are dropped and named in the returned slice rather
// than refused, because a caller who asked for one Responses-only convenience
// still wants an answer from the model.
func translateResponsesRequest(source map[string]any) (map[string]any, []string, error) {
	dropped := unsupportedFields(source,
		"model", "input", "instructions", "temperature", "top_p", "stream", "stream_options",
		"parallel_tool_calls", "seed", "max_output_tokens", "tools", "tool_choice", "text",
		"store", "metadata", "user", "truncation", "include", "reasoning",
	)
	// A stateful conversation cannot be replayed against a stateless Chat
	// endpoint, so the reference is dropped and named. The turns the caller sent
	// inline still reach the model.
	for _, field := range []string{"background", "conversation", "previous_response_id"} {
		if value, ok := source[field]; ok && value != nil && value != false && value != "" {
			dropped = appendUniqueStrings(dropped, field)
		}
	}
	chat := map[string]any{}
	for _, field := range []string{"temperature", "top_p", "stream", "parallel_tool_calls", "seed"} {
		if value, ok := source[field]; ok {
			chat[field] = value
		}
	}
	if options, ok := source["stream_options"]; ok {
		chat["stream_options"] = options
	}
	if maxOutput, ok := source["max_output_tokens"]; ok {
		chat["max_tokens"] = maxOutput
	}

	messages := make([]any, 0)
	if instructions, ok := source["instructions"].(string); ok && strings.TrimSpace(instructions) != "" {
		messages = append(messages, map[string]any{"role": "system", "content": instructions})
	}
	switch input := source["input"].(type) {
	case string:
		messages = append(messages, map[string]any{"role": "user", "content": input})
	case []any:
		for _, rawItem := range input {
			item, ok := rawItem.(map[string]any)
			if !ok {
				// A bare string in the input array is a user turn in every client
				// library that produces one, so it is read that way.
				if text, ok := rawItem.(string); ok {
					messages = append(messages, map[string]any{"role": "user", "content": text})
					continue
				}
				dropped = appendUniqueStrings(dropped, "input item")
				continue
			}
			itemType, _ := item["type"].(string)
			if itemType == "function_call" {
				callID, _ := item["call_id"].(string)
				if callID == "" {
					callID, _ = item["id"].(string)
				}
				messages = append(messages, map[string]any{
					"role": "assistant", "content": nil,
					"tool_calls": []any{map[string]any{
						"id": callID, "type": "function",
						"function": map[string]any{"name": item["name"], "arguments": item["arguments"]},
					}},
				})
				continue
			}
			if itemType == "function_call_output" {
				callID, _ := item["call_id"].(string)
				messages = append(messages, map[string]any{
					"role": "tool", "tool_call_id": callID, "content": item["output"],
				})
				continue
			}
			if itemType == "reasoning" {
				// Reasoning items are an OpenAI-side artifact of a previous turn and
				// have no Chat equivalent. The visible turns carry the conversation.
				continue
			}
			role, _ := item["role"].(string)
			if role == "" {
				role = "user"
			}
			content, lost := translateResponseContent(item["content"])
			dropped = appendUniqueStrings(dropped, lost...)
			messages = append(messages, map[string]any{"role": role, "content": content})
		}
	case nil:
		// A request with no input at all has nothing left to send once it is
		// dropped, so this is the one shape that cannot be repaired.
		return nil, dropped, unsupportedFeatureError{Feature: "input"}
	default:
		messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprint(input)})
	}
	if len(messages) == 0 {
		return nil, dropped, unsupportedFeatureError{Feature: "input"}
	}
	chat["messages"] = messages

	if tools, ok := source["tools"].([]any); ok {
		translated := make([]any, 0, len(tools))
		for _, rawTool := range tools {
			tool, ok := rawTool.(map[string]any)
			if !ok {
				dropped = appendUniqueStrings(dropped, "tool")
				continue
			}
			// A hosted tool such as web_search runs inside OpenAI's own
			// infrastructure and cannot be offered to another upstream. The caller's
			// own function tools still reach the model.
			if tool["type"] != "function" {
				dropped = appendUniqueStrings(dropped, "hosted tool "+fmt.Sprint(tool["type"]))
				continue
			}
			function := map[string]any{
				"name": tool["name"], "description": tool["description"], "parameters": tool["parameters"],
			}
			if strict, exists := tool["strict"]; exists {
				function["strict"] = strict
			}
			translated = append(translated, map[string]any{"type": "function", "function": function})
		}
		if len(translated) > 0 {
			chat["tools"] = translated
		}
	}
	if choice, ok := source["tool_choice"]; ok {
		chat["tool_choice"] = choice
	}
	if _, hasTools := chat["tools"]; !hasTools {
		delete(chat, "tool_choice")
	}
	if text, ok := source["text"].(map[string]any); ok {
		if format, ok := text["format"].(map[string]any); ok {
			switch format["type"] {
			case "json_object":
				chat["response_format"] = map[string]any{"type": "json_object"}
			case "json_schema":
				chat["response_format"] = map[string]any{
					"type": "json_schema",
					"json_schema": map[string]any{
						"name": format["name"], "schema": format["schema"], "strict": format["strict"],
					},
				}
			case "text", nil:
			default:
				dropped = appendUniqueStrings(dropped, "text.format")
			}
		}
	}
	return chat, dropped, nil
}

// translateResponseContent converts one Responses item's content parts and names
// the part types that could not cross, which the caller folds into the request's
// dropped-field list.
func translateResponseContent(raw any) (any, []string) {
	dropped := make([]string, 0)
	if raw == nil {
		return "", dropped
	}
	if text, ok := raw.(string); ok {
		return text, dropped
	}
	parts, ok := raw.([]any)
	if !ok {
		return fmt.Sprint(raw), dropped
	}
	translated := make([]any, 0, len(parts))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			if text, ok := rawPart.(string); ok {
				translated = append(translated, map[string]any{"type": "text", "text": text})
				continue
			}
			dropped = appendUniqueStrings(dropped, "input content part")
			continue
		}
		switch part["type"] {
		case "input_text", "output_text", "text":
			translated = append(translated, map[string]any{"type": "text", "text": part["text"]})
		case "input_image":
			imageURL := part["image_url"]
			translated = append(translated, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}})
		default:
			// A file or audio part cannot cross to Chat, but whatever text rides
			// along with it can, so the turn keeps as much meaning as possible.
			if text, ok := part["text"].(string); ok && text != "" {
				translated = append(translated, map[string]any{"type": "text", "text": text})
			}
			dropped = appendUniqueStrings(dropped, fmt.Sprint(part["type"]))
		}
	}
	if len(translated) == 0 {
		return "", dropped
	}
	if len(translated) == 1 {
		if part, ok := translated[0].(map[string]any); ok && part["type"] == "text" {
			return part["text"], dropped
		}
	}
	return translated, dropped
}

func translateChatResponse(body []byte, publicAlias string) ([]byte, int64, int64, error) {
	var chat map[string]any
	if err := json.Unmarshal(body, &chat); err != nil {
		return nil, 0, 0, err
	}
	responseID := fmt.Sprint(chat["id"])
	if responseID == "" || responseID == "<nil>" {
		generated, _ := newID("resp")
		responseID = generated
	}
	output := make([]any, 0)
	if choices, ok := chat["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if message, ok := choice["message"].(map[string]any); ok {
				messageID, _ := newID("msg")
				content := make([]any, 0)
				if text, ok := message["content"].(string); ok && text != "" {
					content = append(content, map[string]any{
						"type": "output_text", "text": text, "annotations": []any{},
					})
				}
				if len(content) > 0 {
					output = append(output, map[string]any{
						"id": messageID, "type": "message", "role": "assistant",
						"status": "completed", "content": content,
					})
				}
				if calls, ok := message["tool_calls"].([]any); ok {
					for _, rawCall := range calls {
						call, _ := rawCall.(map[string]any)
						function, _ := call["function"].(map[string]any)
						output = append(output, map[string]any{
							"id": call["id"], "call_id": call["id"], "type": "function_call",
							"name": function["name"], "arguments": function["arguments"], "status": "completed",
						})
					}
				}
			}
		}
	}
	inputTokens, outputTokens := extractUsage(chat, "chat")
	response := map[string]any{
		"id": responseID, "object": "response", "created_at": time.Now().Unix(),
		"status": "completed", "model": publicAlias, "output": output,
		"parallel_tool_calls": true,
		"usage": map[string]any{
			"input_tokens": inputTokens, "output_tokens": outputTokens,
			"total_tokens": inputTokens + outputTokens,
		},
	}
	translated, err := json.Marshal(response)
	return translated, inputTokens, outputTokens, err
}

func translateChatStream(source io.Reader, destination io.Writer, publicAlias string, capture *limitedCapture) error {
	reader := bufio.NewReaderSize(source, 64<<10)
	responseID, _ := newID("resp")
	sequence := 0
	writeSSEWithSeq(destination, capture, "response.created", sequence, map[string]any{
		"type": "response.created",
		"response": map[string]any{
			"id": responseID, "object": "response", "created_at": time.Now().Unix(),
			"status": "in_progress", "model": publicAlias, "output": []any{},
		},
	})
	sequence++
	type toolState struct {
		ID, Name, Arguments string
		OutputIndex         int
	}
	tools := map[int]*toolState{}
	output := make([]any, 0)
	textStarted := false
	textValue := ""
	textOutputIndex := -1
	nextOutputIndex := 0
	finished := false
	terminalChoice := false
	emitFailure := func(code, message string) {
		writeSSEWithSeq(destination, capture, "response.failed", sequence, map[string]any{
			"type": "response.failed",
			"response": map[string]any{
				"id": responseID, "object": "response", "created_at": time.Now().Unix(),
				"status": "failed", "model": publicAlias,
				"error": map[string]any{"code": code, "message": message},
			},
		})
		writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
	}
	finalize := func() {
		if textStarted {
			writeSSEWithSeq(destination, capture, "response.output_text.done", sequence, map[string]any{
				"type": "response.output_text.done", "item_id": "msg_" + responseID,
				"output_index": textOutputIndex, "content_index": 0, "text": textValue,
			})
			sequence++
			part := map[string]any{"type": "output_text", "text": textValue, "annotations": []any{}}
			writeSSEWithSeq(destination, capture, "response.content_part.done", sequence, map[string]any{
				"type": "response.content_part.done", "item_id": "msg_" + responseID,
				"output_index": textOutputIndex, "content_index": 0, "part": part,
			})
			sequence++
			item := map[string]any{"id": "msg_" + responseID, "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
			writeSSEWithSeq(destination, capture, "response.output_item.done", sequence, map[string]any{
				"type": "response.output_item.done", "output_index": textOutputIndex, "item": item,
			})
			sequence++
			output = append(output, item)
		}
		indexes := make([]int, 0, len(tools))
		for index := range tools {
			indexes = append(indexes, index)
		}
		slices.Sort(indexes)
		for _, index := range indexes {
			tool := tools[index]
			writeSSEWithSeq(destination, capture, "response.function_call_arguments.done", sequence, map[string]any{
				"type": "response.function_call_arguments.done", "item_id": tool.ID,
				"output_index": tool.OutputIndex, "arguments": tool.Arguments,
			})
			sequence++
			item := map[string]any{"id": tool.ID, "call_id": tool.ID, "type": "function_call", "name": tool.Name, "arguments": tool.Arguments, "status": "completed"}
			writeSSEWithSeq(destination, capture, "response.output_item.done", sequence, map[string]any{
				"type": "response.output_item.done", "output_index": tool.OutputIndex, "item": item,
			})
			sequence++
			output = append(output, item)
		}
		writeSSEWithSeq(destination, capture, "response.completed", sequence, map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": responseID, "object": "response", "created_at": time.Now().Unix(),
				"status": "completed", "model": publicAlias, "output": output,
			},
		})
		writeRaw(destination, capture, []byte("data: [DONE]\n\n"))
	}
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 && strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				finished = true
				finalize()
				return nil
			}
			var chunk map[string]any
			if json.Unmarshal([]byte(data), &chunk) != nil {
				emitFailure("upstream_stream_invalid", "The upstream returned malformed streaming JSON.")
				return fmt.Errorf("malformed upstream SSE JSON")
			}
			if upstreamError, ok := chunk["error"]; ok && upstreamError != nil {
				emitFailure("upstream_stream_error", "The upstream reported an error while streaming.")
				return fmt.Errorf("upstream stream error: %v", upstreamError)
			}
			if choices, ok := chunk["choices"].([]any); ok && len(choices) > 0 {
				choice, _ := choices[0].(map[string]any)
				if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
					// Some OpenAI-compatible providers close the SSE connection after
					// this terminal chunk without a separate data: [DONE] marker.
					terminalChoice = true
				}
				delta, _ := choice["delta"].(map[string]any)
				if content, ok := delta["content"].(string); ok && content != "" {
					if !textStarted {
						textStarted = true
						textOutputIndex = nextOutputIndex
						nextOutputIndex++
						writeSSEWithSeq(destination, capture, "response.output_item.added", sequence, map[string]any{
							"type": "response.output_item.added", "output_index": textOutputIndex,
							"item": map[string]any{"id": "msg_" + responseID, "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}},
						})
						sequence++
						writeSSEWithSeq(destination, capture, "response.content_part.added", sequence, map[string]any{
							"type": "response.content_part.added", "item_id": "msg_" + responseID,
							"output_index": textOutputIndex, "content_index": 0,
							"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
						})
						sequence++
					}
					textValue += content
					writeSSEWithSeq(destination, capture, "response.output_text.delta", sequence, map[string]any{
						"type": "response.output_text.delta", "item_id": "msg_" + responseID,
						"output_index": textOutputIndex, "content_index": 0, "delta": content,
					})
					sequence++
				}
				if calls, ok := delta["tool_calls"].([]any); ok {
					for _, rawCall := range calls {
						call, _ := rawCall.(map[string]any)
						index := int(numberAsInt64(call["index"]))
						tool := tools[index]
						function, _ := call["function"].(map[string]any)
						if tool == nil {
							id, _ := call["id"].(string)
							if id == "" {
								id, _ = newID("call")
							}
							tool = &toolState{ID: id, OutputIndex: nextOutputIndex}
							nextOutputIndex++
							tools[index] = tool
							if name, ok := function["name"].(string); ok {
								tool.Name = name
							}
							writeSSEWithSeq(destination, capture, "response.output_item.added", sequence, map[string]any{
								"type": "response.output_item.added", "output_index": tool.OutputIndex,
								"item": map[string]any{"id": tool.ID, "call_id": tool.ID, "type": "function_call", "name": tool.Name, "arguments": "", "status": "in_progress"},
							})
							sequence++
						}
						if name, ok := function["name"].(string); ok && name != "" {
							tool.Name = name
						}
						if arguments, ok := function["arguments"].(string); ok && arguments != "" {
							tool.Arguments += arguments
							writeSSEWithSeq(destination, capture, "response.function_call_arguments.delta", sequence, map[string]any{
								"type": "response.function_call_arguments.delta", "item_id": tool.ID,
								"output_index": tool.OutputIndex, "delta": arguments,
							})
							sequence++
						}
					}
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				if finished {
					return nil
				}
				if terminalChoice {
					finalize()
					return nil
				}
				emitFailure("stream_interrupted", "The upstream stream ended before completion.")
				return io.ErrUnexpectedEOF
			}
			return err
		}
	}
}

func writeSSEWithSeq(destination io.Writer, capture *limitedCapture, event string, seq int, payload any) {
	if object, ok := payload.(map[string]any); ok {
		object["sequence_number"] = seq
	}
	data, _ := json.Marshal(payload)
	writeRaw(destination, capture, []byte("event: "+event+"\ndata: "))
	writeRaw(destination, capture, data)
	writeRaw(destination, capture, []byte("\n\n"))
}

func writeSSE(destination io.Writer, capture *limitedCapture, event string, payload any) {
	writeSSEWithSeq(destination, capture, event, 0, payload)
}

func writeRaw(destination io.Writer, capture *limitedCapture, data []byte) {
	_, _ = destination.Write(data)
	if capture != nil {
		_, _ = capture.Write(data)
	}
	if flusher, ok := destination.(interface{ Flush() }); ok {
		flusher.Flush()
	}
}

func extractUsage(payload map[string]any, endpoint string) (int64, int64) {
	usage, _ := payload["usage"].(map[string]any)
	if usage == nil {
		return 0, 0
	}
	if endpoint == "responses" {
		return numberAsInt64(usage["input_tokens"]), numberAsInt64(usage["output_tokens"])
	}
	return numberAsInt64(usage["prompt_tokens"]), numberAsInt64(usage["completion_tokens"])
}

func numberAsInt64(value any) int64 {
	switch number := value.(type) {
	case float64:
		return int64(number)
	case json.Number:
		result, _ := number.Int64()
		return result
	case int64:
		return number
	case int:
		return int64(number)
	default:
		return 0
	}
}

func replaceResponseModel(body []byte, publicAlias string) ([]byte, int64, int64) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body, 0, 0
	}
	payload["model"] = publicAlias
	input, output := extractUsage(payload, "chat")
	if payload["object"] == "response" {
		input, output = extractUsage(payload, "responses")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body, input, output
	}
	return encoded, input, output
}

type limitedCapture struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (c *limitedCapture) Write(data []byte) (int, error) {
	original := len(data)
	if int64(c.buffer.Len()) >= c.limit {
		c.truncated = true
		return original, nil
	}
	remaining := c.limit - int64(c.buffer.Len())
	if int64(len(data)) > remaining {
		data = data[:remaining]
		c.truncated = true
	}
	_, _ = c.buffer.Write(data)
	return original, nil
}

func (c *limitedCapture) Bytes() []byte {
	return c.buffer.Bytes()
}
