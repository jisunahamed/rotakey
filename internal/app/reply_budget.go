package app

import (
	"context"
	"encoding/json"
	"strconv"
)

// The reply budget, managed so that "the model ran out of room" stops being
// the caller's problem.
//
// A thinking model spends part of its output budget inside its own head, and
// when the budget is small the whole of it can go there: the reply comes back
// with no visible text, no tool call, and a finish reason saying the length
// ran out. Since 3.0.4 that translates honestly instead of failing — but an
// honest empty answer is still not an answer, and every route here defaults
// to 1,024 output tokens, which a reasoning model can exhaust before its
// first visible word. The operator's ask is blunt: coding clients get
// answers, not budget arithmetic.
//
// So a starved reply is treated like any other repairable rejection. The
// attempt is not written to the caller; the budget is escalated and the same
// request retried, inside the ordinary compatibility budget of two retries.
// A cap that then produced a real answer is remembered for a day and used as
// the floor whenever a caller does not set a cap of their own. Within one
// request the escalation overrides even an explicit caller cap — the cap was
// honoured on the first attempt and produced nothing, and the operator has
// chosen answers over ceilings. Across requests, a caller's own cap is
// respected: the remembered floor only fills the gap where the route default
// would have.

// starvedReply reports whether an upstream 2xx body is a reply that spent its
// whole output budget without producing anything visible. Each wire spells it
// differently; all three require the length/budget stop, so an empty reply
// that finished normally — a model with nothing to say — is not retried.
func starvedReply(format, wire string, body []byte) bool {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	if format == "anthropic" {
		if payload["stop_reason"] != "max_tokens" {
			return false
		}
		blocks, ok := payload["content"].([]any)
		if !ok {
			return false
		}
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if block["type"] == "tool_use" {
				return false
			}
			if text, carries := block["text"].(string); carries && text != "" {
				return false
			}
		}
		return true
	}
	if wire == "responses" {
		if payload["status"] != "incomplete" {
			return false
		}
		details, _ := payload["incomplete_details"].(map[string]any)
		if details["reason"] != "max_output_tokens" {
			return false
		}
		items, _ := payload["output"].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			if item["type"] == "function_call" {
				return false
			}
			if parts, ok := item["content"].([]any); ok {
				for _, rawPart := range parts {
					part, _ := rawPart.(map[string]any)
					if text, carries := part["text"].(string); carries && text != "" {
						return false
					}
				}
			}
		}
		return true
	}
	choices, _ := payload["choices"].([]any)
	if len(choices) == 0 {
		return false
	}
	choice, _ := choices[0].(map[string]any)
	if choice["finish_reason"] != "length" {
		return false
	}
	message, _ := choice["message"].(map[string]any)
	if calls, ok := message["tool_calls"].([]any); ok && len(calls) > 0 {
		return false
	}
	text, _ := message["content"].(string)
	return text == ""
}

// currentOutputCap reads the cap the plan actually sent, whichever spelling
// the wire uses. buildPlan always writes one, so zero means the body is not
// one this pass planned and nothing should be escalated from it.
func currentOutputCap(payload map[string]any, format, wire string) int64 {
	if format == "anthropic" {
		return numberAsInt64(payload["max_tokens"])
	}
	if wire == "responses" {
		return numberAsInt64(payload["max_output_tokens"])
	}
	if value := numberAsInt64(payload["max_completion_tokens"]); value > 0 {
		return value
	}
	return numberAsInt64(payload["max_tokens"])
}

// escalateReplyBudget picks the next cap to try. Four times the last one,
// never below 4,096 and never above 32,768: the multiplier keeps one route's
// escalation proportional to what its operator considered normal, the floor
// makes the first escalation meaningful against the 1,024 default, and the
// ceiling stays under every current model's output maximum so the retry
// cannot trade a starved reply for a cap-too-large rejection.
func escalateReplyBudget(current int64) int64 {
	escalated := current * 4
	if escalated < 4096 {
		escalated = 4096
	}
	if escalated > 32768 {
		escalated = 32768
	}
	return escalated
}

// applyReplyFloor raises the plan's cap to the floor, in the wire's own
// spelling, and reports whether it changed anything. It only ever raises: a
// floor below the cap already present is no instruction at all.
func applyReplyFloor(payload map[string]any, format, wire string, floor int64) bool {
	if floor <= 0 {
		return false
	}
	field := "max_tokens"
	if format != "anthropic" {
		if wire == "responses" {
			field = "max_output_tokens"
		} else if numberAsInt64(payload["max_completion_tokens"]) > 0 {
			field = "max_completion_tokens"
		}
	}
	if numberAsInt64(payload[field]) >= floor {
		return false
	}
	payload[field] = floor
	return true
}

// payloadCarriesOutputCap reports whether the caller set any output cap of
// their own, before buildPlan fills a default in. The learned floor respects
// an explicit cap; only the in-request escalation may override one.
func payloadCarriesOutputCap(payload map[string]any) bool {
	return numberAsInt64(payload["max_tokens"]) > 0 ||
		numberAsInt64(payload["max_completion_tokens"]) > 0 ||
		numberAsInt64(payload["max_output_tokens"]) > 0
}

// replyUsage reads what the starved attempt actually consumed, so the limiter
// is settled with the truth: the tokens were billed even though the reply
// never reached the caller.
func replyUsage(format, wire string, body []byte) (int64, int64) {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return 0, 0
	}
	if format == "anthropic" {
		return extractAnthropicUsage(payload)
	}
	if wire == "responses" {
		return extractUsage(payload, "responses")
	}
	return extractUsage(payload, "chat")
}

func replyFloorKey(modelID string) string {
	return "compatibility:reply-floor:" + modelID
}

// learnedReplyFloor reports the smallest cap known to produce a visible reply
// on this route, or zero when nothing has been learned.
func (s *Server) learnedReplyFloor(ctx context.Context, modelID string) int64 {
	value, err := s.redis.Get(ctx, replyFloorKey(modelID)).Result()
	if err != nil {
		return 0
	}
	floor, parseErr := strconv.ParseInt(value, 10, 64)
	if parseErr != nil || floor <= 0 || floor > 32768 {
		// Redis outlives deploys; a value outside what escalateReplyBudget can
		// produce was not written by this code and is not acted on.
		return 0
	}
	return floor
}

// rememberReplyFloor records a cap only once it has produced an answer — the
// same proved-not-inferred rule the endpoint preference follows. Remembering
// the escalation at the moment of failure would cache a guess.
func (s *Server) rememberReplyFloor(ctx context.Context, modelID string, floor int64) {
	if floor <= s.learnedReplyFloor(ctx, modelID) {
		return
	}
	if err := s.redis.Set(ctx, replyFloorKey(modelID), strconv.FormatInt(floor, 10), adaptiveCompatibilityTTL).Err(); err != nil {
		s.logger.Warn("reply floor cache write failed", "model_id", modelID, "error", err)
	}
}
