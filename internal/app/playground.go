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
)

type playgroundInput struct {
	Model       string   `json:"model"`
	Prompt      string   `json:"prompt"`
	System      string   `json:"system"`
	Protocol    string   `json:"protocol"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature *float64 `json:"temperature"`
}

func validatePlaygroundInput(input *playgroundInput) error {
	input.Model = strings.TrimSpace(input.Model)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.System = strings.TrimSpace(input.System)
	input.Protocol = strings.ToLower(strings.TrimSpace(input.Protocol))
	if input.Protocol == "" {
		input.Protocol = playgroundProtocolAuto
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = 1024
	}
	if input.Model == "" || len(input.Model) > 128 {
		return fmt.Errorf("model alias is required")
	}
	if input.Prompt == "" || len(input.Prompt) > 200_000 {
		return fmt.Errorf("prompt is required and must be at most 200000 characters")
	}
	if len(input.System) > 100_000 {
		return fmt.Errorf("system prompt must be at most 100000 characters")
	}
	if input.MaxTokens < 1 || input.MaxTokens > 1_000_000 {
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

func playgroundPayload(input playgroundInput, protocol string) map[string]any {
	payload := map[string]any{"model": input.Model, "stream": false}
	switch protocol {
	case playgroundProtocolResponses:
		payload["input"] = input.Prompt
		payload["max_output_tokens"] = input.MaxTokens
		if input.System != "" {
			payload["instructions"] = input.System
		}
	default:
		messages := make([]map[string]any, 0, 2)
		if input.System != "" && protocol != playgroundProtocolMessages {
			messages = append(messages, map[string]any{"role": "system", "content": input.System})
		}
		messages = append(messages, map[string]any{"role": "user", "content": input.Prompt})
		payload["messages"] = messages
		if protocol == playgroundProtocolMessages {
			payload["max_tokens"] = input.MaxTokens
			if input.System != "" {
				payload["system"] = input.System
			}
		} else {
			payload["max_tokens"] = input.MaxTokens
		}
	}
	if input.Temperature != nil {
		payload["temperature"] = *input.Temperature
	}
	return payload
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
	s.audit(r.Context(), adminIDFromContext(r.Context()), "playground.run", "model", input.Model, map[string]any{"protocol": protocol})

	switch protocol {
	case playgroundProtocolResponses:
		s.handleGatewayRequest(w, request, playgroundProtocolResponses)
	case playgroundProtocolMessages:
		s.handleAnthropicMessages(w, request)
	default:
		s.handleGatewayRequest(w, request, playgroundProtocolChat)
	}
}
