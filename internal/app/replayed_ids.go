package app

import (
	"context"
	"encoding/json"
	"regexp"
)

// Repairs for a provider that demands the half of a conversation the caller
// no longer has.
//
// A Responses reply from a reasoning model is a pair: a reasoning item and
// the message it produced, linked by their ids. A stateless client like Codex
// replays both on the next turn — but it can only replay the reasoning item
// when the provider returned its encrypted form, and not every provider
// does. So the client replays what it has, the message alone, and a strict
// validator reads the message's id, remembers the pairing, and refuses:
//
//	400 invalid_request_error
//	Item 'msg_…' of type 'message' was provided without its required
//	'reasoning' item: 'rs_…'.
//
// The id is the whole problem. A replayed item carries one only as an echo of
// the reply it came from; nothing in the request needs it, and without it the
// item is plain history the validator takes as it is. So when a provider
// rejects the pairing, every replayed item in the conversation goes back
// without its id, and the lesson is remembered for a day like the other
// repairs. It is learned rather than unconditional because those ids are
// harmless almost everywhere, and a rewrite that fires only where it has seen
// the rejection cannot surprise the providers that never do.
//
// "Every replayed item" is deliberate, and it is the second version of this
// sentence. The first read "every replayed message", on the reasoning that a
// function_call's pairing is real — and it is, but it lives in call_id, not
// in the item id. The validator demands a reasoning pair for whichever
// id-bearing item it reads next, so detaching the messages alone just moved
// the same rejection one item down:
//
//	Item 'fc_…' of type 'function_call' was provided without its required
//	'reasoning' item: 'rs_…'.
//
// The two items whose ids survive are the two whose ids carry meaning of
// their own: an item_reference IS an id, and a reasoning item's id is the
// pairing — a client replaying reasoning items intact never sees this
// rejection, and one that cannot replay them has nothing to pair anyway.

var orphanedPairingPattern = regexp.MustCompile(
	`(?i)was provided without its required ['"` + "`" + `]?reasoning['"` + "`" + `]? item`)

// replayedIDCarrier reports whether an input item is one whose id is an echo
// this repair may remove: a turn with a role, or a tool call and its output,
// whose real pairing is call_id.
func replayedIDCarrier(object map[string]any) bool {
	if role, _ := object["role"].(string); role != "" {
		return true
	}
	kind, _ := object["type"].(string)
	return kind == "function_call" || kind == "function_call_output"
}

// orphanedReasoningPairing reads the rejection above out of an upstream 400,
// and confirms the request actually carries a replayed message id to remove.
func orphanedReasoningPairing(body []byte, payload map[string]any) bool {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	if !orphanedPairingPattern.MatchString(envelope.Error.Message) {
		return false
	}
	return payloadCarriesReplayedIDs(payload)
}

func payloadCarriesReplayedIDs(payload map[string]any) bool {
	items, ok := payload["input"].([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject || !replayedIDCarrier(object) {
			continue
		}
		if id, _ := object["id"].(string); id != "" {
			return true
		}
	}
	return false
}

// stripReplayedItemIDs removes the id from every replayed turn, tool call and
// tool output in input[] and reports whether anything changed. call_id, name
// and arguments stay — that pairing is real and is not what the validator was
// demanding. Changed items are copied first, for the same reason every pass
// over these arrays copies — the items are the caller's own objects, shared
// by every candidate's plan.
func stripReplayedItemIDs(payload map[string]any) bool {
	items, ok := payload["input"].([]any)
	if !ok {
		return false
	}
	copied := make([]any, len(items))
	changed := false
	for index, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject || !replayedIDCarrier(object) {
			copied[index] = item
			continue
		}
		if id, _ := object["id"].(string); id == "" {
			copied[index] = item
			continue
		}
		replacement := cloneMap(object)
		delete(replacement, "id")
		copied[index] = replacement
		changed = true
	}
	if changed {
		payload["input"] = copied
	}
	return changed
}

func detachReplayedKey(modelID string) string {
	return "compatibility:detach-replayed:" + modelID
}

func (s *Server) learnedDetachReplayedIDs(ctx context.Context, modelID string) bool {
	value, err := s.redis.Get(ctx, detachReplayedKey(modelID)).Result()
	return err == nil && value == "1"
}

func (s *Server) rememberDetachReplayedIDs(ctx context.Context, modelID string) {
	if err := s.redis.Set(ctx, detachReplayedKey(modelID), "1", adaptiveCompatibilityTTL).Err(); err != nil {
		s.logger.Warn("replayed id compatibility cache write failed", "model_id", modelID, "error", err)
	}
}
