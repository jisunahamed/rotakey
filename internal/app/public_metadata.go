package app

import (
	"net/http"
	"strconv"
	"strings"
)

func capabilityState(value string) string {
	switch value {
	case "catalog_supported", "native", "translated", "gateway_normalized", "supported":
		return "supported"
	case "unsupported", "off", "unsupported_cross_protocol":
		return "unsupported"
	default:
		return "unknown"
	}
}

func positiveCapability(value string) any {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return nil
	}
	return n
}

func modelCapabilities(route routeRuntime) map[string]any {
	m := route.Model
	p := m.CapabilityProfile
	features := map[string]string{}
	for name, key := range map[string]string{"vision_input": "images", "tool_calling": "tools", "parallel_tool_calls": "parallel_tool_calls", "thinking": "thinking", "streaming": "streaming", "structured_output": "structured_outputs", "json_output": "json_output", "web_search": "web_search", "audio_input": "audio_input", "audio_output": "audio_output"} {
		features[name] = capabilityState(p[key])
	}
	audio := audioModelType(m.UpstreamModel, p["model_type"])
	if audio == "audio_transcription" {
		features["audio_input"] = "supported"
	}
	if audio == "audio_speech" {
		features["audio_output"] = "supported"
	}
	endpoints := []string{}
	for _, mode := range []string{messageModeChat, messageModeResponses, messageModeAnthropic, messageModeEmbeddings} {
		if routeSupportsRequest(route, dispatchRequest{PublicMode: mode}) {
			path := map[string]string{messageModeChat: "/v1/chat/completions", messageModeResponses: "/v1/responses", messageModeAnthropic: "/v1/messages", messageModeEmbeddings: "/v1/embeddings"}[mode]
			endpoints = append(endpoints, path)
		}
	}
	for _, endpoint := range audioModelEndpoints(m.UpstreamModel, p["model_type"]) {
		if audioRouteSupports(route, endpoint) {
			endpoints = append(endpoints, "/v1/"+endpoint)
		}
	}
	return map[string]any{"provider": route.Provider.Name, "capability_profile": p, "features": features, "context_window": positiveCapability(p["context_window"]), "max_output_tokens": positiveCapability(p["max_output_tokens"]), "default_max_output_tokens": m.DefaultMaxOutputTokens, "reasoning_effort_levels": modelReasoningLevels(m), "endpoints": endpoints, "verification": m.CapabilityStatus, "source": valueOr(p["capability_source"], valueOr(p["verification"], "unknown")), "checked_at": m.CapabilitiesCheckedAt}
}

// The top-level view is a conservative intersection: never promise a feature
// or limit that an eligible fallback route cannot provide.
func capabilityEnvelope(alias string, routes []routeRuntime) map[string]any {
	variants := make([]map[string]any, 0, len(routes))
	for _, r := range routes {
		variants = append(variants, modelCapabilities(r))
	}
	features := map[string]string{}
	for _, v := range variants {
		for name, state := range v["features"].(map[string]string) {
			previous, seen := features[name]
			if !seen {
				features[name] = state
			} else if state == "unsupported" || previous == "unsupported" {
				features[name] = "unsupported"
			} else if state == "unknown" || previous == "unknown" {
				features[name] = "unknown"
			}
		}
	}
	limits := map[string]any{}
	for _, field := range []string{"context_window", "max_output_tokens"} {
		var minimum int64
		known := true
		for _, v := range variants {
			n, ok := v[field].(int64)
			if !ok {
				known = false
				break
			}
			if minimum == 0 || n < minimum {
				minimum = n
			}
		}
		if known && minimum > 0 {
			limits[field] = minimum
		} else {
			limits[field] = nil
		}
	}
	intersection := func(field string) []string {
		if len(variants) == 0 {
			return []string{}
		}
		out := []string{}
		for _, candidate := range variants[0][field].([]string) {
			every := true
			for _, v := range variants[1:] {
				found := false
				for _, item := range v[field].([]string) {
					if item == candidate {
						found = true
					}
				}
				if !found {
					every = false
				}
			}
			if every {
				out = append(out, candidate)
			}
		}
		return out
	}
	return map[string]any{"object": "model.capabilities", "model": alias, "aggregation": "all_eligible_routes", "features": features, "limits": limits, "reasoning_effort_levels": intersection("reasoning_effort_levels"), "endpoints": intersection("endpoints"), "routes": variants}
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	alias := strings.TrimSuffix(r.PathValue("id"), "/capabilities")
	alias = resolveAnthropicDiscoveryModelID(alias)
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeError(w, 503, "settings_unavailable", "Gateway settings are unavailable.")
		return
	}
	routes, err := s.resolveRoutes(r.Context(), alias, settings.RoutingMode)
	if err != nil {
		writeError(w, 404, "model_not_found", "The requested model alias is not enabled.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, capabilityEnvelope(alias, routes))
}

func publicUsage(log RequestLog, pending bool) map[string]any {
	state := log.State
	if state == "" {
		state = "running"
	}
	if !pending && log.ErrorCode != "" && state == "completed" {
		state = "failed"
	}
	return map[string]any{"object": "request.usage", "request_id": log.RequestID, "model": log.ModelAlias, "endpoint": log.Endpoint, "state": state, "final": !pending, "http_status": log.StatusCode, "usage": map[string]any{"input_tokens": log.InputTokens, "output_tokens": log.OutputTokens, "total_tokens": log.InputTokens + log.OutputTokens, "source": "gateway_recorded", "accuracy": "not_verified", "note": "Input may include a gateway estimate; zero does not prove provider usage was zero. Prefer native response usage for provider-reported accounting."}, "timing": map[string]any{"total_ms": log.LatencyMS, "queue_wait_ms": log.QueueWaitMS, "processing_ms": max(int64(0), log.LatencyMS-log.QueueWaitMS)}, "error_code": log.ErrorCode, "created_at": log.CreatedAt}
}

func (s *Server) handleRequestUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Cache-Control", "no-store")
	if active, ok := s.activeRequests.Load(id); ok {
		if log, ok := active.(RequestLog); ok {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusAccepted, publicUsage(log, true))
			return
		}
	}
	log, err := scanRequestLog(s.db.QueryRow(r.Context(), `SELECT `+requestLogColumns+` FROM request_logs WHERE request_id=$1`, id))
	if err != nil {
		if isNotFound(err) {
			writeError(w, 404, "request_not_found", "Request usage is not yet recorded or has expired.")
		} else {
			writeError(w, 503, "usage_unavailable", "Request usage is unavailable.")
		}
		return
	}
	writeJSON(w, 200, publicUsage(log, false))
}
