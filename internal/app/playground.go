package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	playgroundProtocolAuto      = "auto"
	playgroundProtocolChat      = "chat"
	playgroundProtocolResponses = "responses"
	playgroundProtocolMessages  = "messages"

	playgroundMaxTurns    = 200
	playgroundMaxContent  = 200_000
	playgroundMaxSystem   = 100_000
	playgroundDefaultCap  = 1024
	playgroundMaxTokenCap = 1_000_000
)

// playgroundMessage is one turn of the console's conversation. Content is plain
// text: the playground sends what the operator typed and renders what came back,
// and neither end has a reason to build content parts.
type playgroundMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type playgroundInput struct {
	Model string `json:"model"`
	// Prompt is the single-turn form. Messages is the conversation form. Exactly
	// one of them arrives; Prompt is kept because it is the whole API that the
	// console had before multi-turn, and callers still using it must keep working.
	Prompt      string              `json:"prompt"`
	Messages    []playgroundMessage `json:"messages"`
	System      string              `json:"system"`
	Protocol    string              `json:"protocol"`
	Stream      bool                `json:"stream"`
	MaxTokens   int                 `json:"max_tokens"`
	Temperature *float64            `json:"temperature"`
}

// turns reads the conversation in one shape whichever form the caller sent, so
// every payload builder below has a single case to handle.
func (input playgroundInput) turns() []playgroundMessage {
	if len(input.Messages) > 0 {
		return input.Messages
	}
	if input.Prompt == "" {
		return nil
	}
	return []playgroundMessage{{Role: "user", Content: input.Prompt}}
}

func validatePlaygroundInput(input *playgroundInput) error {
	input.Model = strings.TrimSpace(input.Model)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.System = strings.TrimSpace(input.System)
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	for index := range input.Messages {
		input.Messages[index].Role = strings.ToLower(strings.TrimSpace(input.Messages[index].Role))
		input.Messages[index].Content = strings.TrimSpace(input.Messages[index].Content)
	}
	if input.Protocol == "" {
		input.Protocol = playgroundProtocolAuto
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = playgroundDefaultCap
	}
	if input.Model == "" || len(input.Model) > 128 {
		return fmt.Errorf("model alias is required")
	}
	if err := validatePlaygroundTurns(*input); err != nil {
		return err
	}
	if len(input.System) > playgroundMaxSystem {
		return fmt.Errorf("system prompt must be at most %d characters", playgroundMaxSystem)
	}
	if input.MaxTokens < 1 || input.MaxTokens > playgroundMaxTokenCap {
		return fmt.Errorf("max tokens are invalid")
	}
	if input.Temperature != nil && (*input.Temperature < 0 || *input.Temperature > 2) {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	switch input.Protocol {
	case playgroundProtocolAuto, playgroundProtocolChat, playgroundProtocolResponses, playgroundProtocolMessages:
		return nil
	default:
		return fmt.Errorf("protocol must be auto, chat, responses, or messages")
	}
}

// validatePlaygroundTurns rejects the conversations that produce an upstream 400
// nobody can read. Two of these are not defensive politeness:
// translateChatRequestToAnthropic silently drops an empty turn, and nothing in
// the gateway repairs alternation — so a turn edited down to nothing, or two
// user turns in a row, reaches the provider as a shape it refuses.
func validatePlaygroundTurns(input playgroundInput) error {
	if input.Prompt != "" && len(input.Messages) > 0 {
		return fmt.Errorf("send either a prompt or a conversation, not both")
	}
	turns := input.turns()
	if len(turns) == 0 {
		return fmt.Errorf("a prompt or at least one message is required")
	}
	if len(turns) > playgroundMaxTurns {
		return fmt.Errorf("a conversation may hold at most %d messages", playgroundMaxTurns)
	}
	total := 0
	for index, turn := range turns {
		if turn.Role != "user" && turn.Role != "assistant" {
			return fmt.Errorf("message %d has role %q; only user and assistant are allowed", index+1, turn.Role)
		}
		if turn.Content == "" {
			return fmt.Errorf("message %d is empty", index+1)
		}
		if index == 0 && turn.Role != "user" {
			return fmt.Errorf("the conversation must start with a user message")
		}
		if index > 0 && turn.Role == turns[index-1].Role {
			return fmt.Errorf("message %d repeats the %s role; turns must alternate", index+1, turn.Role)
		}
		total += len(turn.Content)
	}
	if total > playgroundMaxContent {
		return fmt.Errorf("the conversation must be at most %d characters", playgroundMaxContent)
	}
	return nil
}

func playgroundPayload(input playgroundInput, protocol string) map[string]any {
	payload := map[string]any{"model": input.Model, "stream": input.Stream}
	turns := input.turns()
	switch protocol {
	case playgroundProtocolResponses:
		// The array form of input, not the bare string: it is the only one that can
		// carry more than one turn, and translate.go already reads it on every path
		// that translates a Responses request down into another shape.
		payload["input"] = playgroundTurnList(turns)
		payload["max_output_tokens"] = input.MaxTokens
		if input.System != "" {
			payload["instructions"] = input.System
		}
	case playgroundProtocolMessages:
		// Anthropic keeps the system prompt out of the turn list, so it stays a
		// top-level field rather than a first message.
		payload["messages"] = playgroundTurnList(turns)
		payload["max_tokens"] = input.MaxTokens
		if input.System != "" {
			payload["system"] = input.System
		}
	default:
		messages := make([]map[string]any, 0, len(turns)+1)
		if input.System != "" {
			messages = append(messages, map[string]any{"role": "system", "content": input.System})
		}
		payload["messages"] = append(messages, playgroundTurnList(turns)...)
		payload["max_tokens"] = input.MaxTokens
		if input.Stream {
			// Without this a streamed Chat reply reports no token counts at all, so
			// the evidence strip would have to print an estimate as if it were
			// measured. The field is adaptively repairable: a provider that refuses
			// it gets it stripped and the request retried.
			payload["stream_options"] = map[string]any{"include_usage": true}
		}
	}
	if input.Temperature != nil {
		payload["temperature"] = *input.Temperature
	}
	return payload
}

func playgroundTurnList(turns []playgroundMessage) []map[string]any {
	items := make([]map[string]any, 0, len(turns))
	for _, turn := range turns {
		items = append(items, map[string]any{"role": turn.Role, "content": turn.Content})
	}
	return items
}

func (s *Server) playgroundProtocol(r *http.Request, input playgroundInput) (string, error) {
	if input.Protocol != playgroundProtocolAuto {
		return input.Protocol, nil
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		return "", fmt.Errorf("gateway settings are unavailable")
	}
	routes, err := s.resolveRoutes(r.Context(), input.Model, settings.RoutingMode)
	if err != nil || len(routes) == 0 {
		return "", fmt.Errorf("the requested model alias is not enabled")
	}
	for _, route := range routes {
		if route.Model.SupportsChat {
			return playgroundProtocolChat, nil
		}
	}
	for _, route := range routes {
		if route.Model.SupportsResponses {
			return playgroundProtocolResponses, nil
		}
	}
	for _, route := range routes {
		if route.Model.SupportsMessages {
			return playgroundProtocolMessages, nil
		}
	}
	return "", fmt.Errorf("the requested model has no enabled inference protocol")
}

func (s *Server) handlePlaygroundRun(w http.ResponseWriter, r *http.Request) {
	var input playgroundInput
	if decodeJSON(w, r, 512<<10, &input) != nil {
		return
	}
	if err := validatePlaygroundInput(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_playground_request", err.Error())
		return
	}
	protocol, err := s.playgroundProtocol(r, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "playground_route_unavailable", err.Error())
		return
	}
	raw, err := json.Marshal(playgroundPayload(input, protocol))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "playground_request_failed", "Playground request could not be prepared.")
		return
	}

	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(raw))
	request.ContentLength = int64(len(raw))
	request.Header = r.Header.Clone()
	request.Header.Set("Content-Type", "application/json")
	w.Header().Set("X-Rotakey-Playground-Protocol", protocol)
	// The gateway's own request id, named separately because copyUpstreamHeaders
	// deliberately lets a provider's Request-Id win on the two OpenAI-shaped
	// paths. Without this header the console cannot look up the log record that
	// explains which key answered, so the reply would arrive with no evidence.
	w.Header().Set("X-Rotakey-Request-Id", requestIDFromContext(r.Context()))
	s.audit(r.Context(), adminIDFromContext(r.Context()), "playground.run", "model", input.Model, map[string]any{
		"protocol": protocol, "stream": input.Stream, "turns": len(input.turns()),
	})

	switch protocol {
	case playgroundProtocolResponses:
		s.handleGatewayRequest(w, request, playgroundProtocolResponses)
	case playgroundProtocolMessages:
		s.handleAnthropicMessages(w, request)
	default:
		s.handleGatewayRequest(w, request, playgroundProtocolChat)
	}
}
