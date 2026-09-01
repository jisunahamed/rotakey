package app

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
)

// Repairs for a field a provider rejects from inside the conversation rather
// than from the top level of the request.
//
// The OpenAI-shaped Responses API carries the turns in input[], and a client is
// free to hang its own bookkeeping off each one. OpenAI's own validator ignores
// what it does not recognise; Azure's rejects it. Codex CLI writes
// internal_chat_message_metadata_passthrough into every input item, so every
// Codex turn against an Azure route came back:
//
//	400 unknown_parameter
//	Unknown parameter: 'input[0].internal_chat_message_metadata_passthrough.…'
//
// Neither existing repair could reach it, for two separate reasons.
// stripTopLevelParameters only deletes keys from the payload's own map, and the
// name the provider gave is a path into an object nested two levels down. And
// the adaptive strip pass reads a parameter name through a pattern that stops at
// the '[', so all it saw was "input", which is not on the allowlist and must
// never be — deleting input is deleting the request. So the attempt was final
// every time, and the credential that served it took a strike for a field the
// operator never wrote.
//
// The repair below is deliberately literal. The provider names a path; the
// gateway deletes that one field from the items of that one array and tries
// again. It never guesses at a field the provider did not name, and it never
// touches a field one of the three protocols defines.

// itemFieldStrip is one nested field a provider rejected: the array the turns
// arrive in, and the field's name inside each of that array's items.
type itemFieldStrip struct {
	Root  string
	Field string
}

// String is how the strip is written down — in Redis, in an attempt's removed
// list, and on the route's learned panel. The "[]" says the field is deleted
// from every item rather than from the one the provider happened to blame
// first, which is what the gateway actually does.
func (strip itemFieldStrip) String() string {
	return strip.Root + "[]." + strip.Field
}

var (
	// itemFieldRoots are the two arrays a caller's own turns arrive in. Anything
	// else named with an index — tools[2], include[0] — is a field the gateway
	// or the operator configured, and the answer there is to fix the
	// configuration rather than to quietly send something different.
	itemFieldRoots = map[string]bool{"input": true, "messages": true}

	// protocolItemFields are the names the three protocols give the parts of a
	// turn. The gateway has no list of every client's private extensions — that
	// is exactly why this repair exists — so the guard is inverted: whatever the
	// protocols name is structural and is never deleted, however the provider
	// spells its complaint. Without this, a provider rejecting 'input[0].content'
	// for an oversized or malformed part would have the gateway delete the
	// message text and retry with an empty turn, which reads to the caller as the
	// model ignoring what they said.
	protocolItemFields = map[string]bool{
		"role": true, "content": true, "type": true, "text": true, "name": true,
		"id": true, "call_id": true, "arguments": true, "output": true, "status": true,
		"tool_calls": true, "tool_call_id": true, "function": true, "refusal": true,
		"image_url": true, "input_text": true, "input_image": true, "input_file": true,
		"file_id": true, "file_url": true, "detail": true, "audio": true, "index": true,
		"summary": true, "encrypted_content": true, "reasoning": true, "thinking": true,
		"signature": true, "source": true, "cache_control": true, "citations": true,
	}

	// itemFieldPathPattern reads a path out of the provider's prose. It differs
	// from unsupportedParameterPattern in one way that matters: the capture keeps
	// the brackets and the dots, because here the path is the whole answer.
	itemFieldPathPattern = regexp.MustCompile(
		`(?i)(?:unrecognized request argument supplied|(?:unsupported|unknown) (?:request )?(?:argument|parameter)(?:\(s\))?)\s*:\s*['"` + "`" + `]?([A-Za-z_][A-Za-z0-9_]{0,31}\[\d{1,9}\]\.[A-Za-z0-9_.\[\]-]{1,255})`,
	)

	// itemFieldPathShape splits root[index].field out of a path, ignoring
	// whatever the provider appended after the field. A rejection often names the
	// deepest key it reached — passthrough.content_type — and the field one level
	// under the item is the one that has to go.
	itemFieldPathShape = regexp.MustCompile(
		`^([A-Za-z_][A-Za-z0-9_]{0,31})\[\d{1,9}\]\.([A-Za-z_][A-Za-z0-9_]{0,63})`,
	)
)

// parseItemFieldPath turns one path the provider named into a strip, or reports
// that it is not something this repair is allowed to act on.
func parseItemFieldPath(path string) (itemFieldStrip, bool) {
	match := itemFieldPathShape.FindStringSubmatch(strings.TrimSpace(path))
	if len(match) < 3 {
		return itemFieldStrip{}, false
	}
	strip := itemFieldStrip{Root: match[1], Field: match[2]}
	if !itemFieldRoots[strip.Root] || protocolItemFields[strip.Field] {
		return itemFieldStrip{}, false
	}
	return strip, true
}

// parseItemFieldStrip reads back what rememberItemFieldStrip wrote. Redis is
// shared state that outlives a deploy, so a stored value is treated as
// untrusted input and re-checked against the same two guards.
func parseItemFieldStrip(value string) (itemFieldStrip, bool) {
	root, field, found := strings.Cut(strings.TrimSpace(value), "[].")
	if !found {
		return itemFieldStrip{}, false
	}
	return parseItemFieldPath(root + "[0]." + field)
}

// unsupportedItemField reads a nested field's path out of an upstream 400.
//
// The strip is only returned when the request being repaired actually carries
// that field. Learning one that changes nothing would burn the retry budget on
// an identical request and then cache the useless repair for a day.
func unsupportedItemField(body []byte, payload map[string]any) (itemFieldStrip, bool) {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Param   any    `json:"param"`
			Code    any    `json:"code"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return itemFieldStrip{}, false
	}
	if !unsupportedParameterCode(envelope.Error.Code, envelope.Error.Type) &&
		len(compatibilityParameterMatches(envelope.Error.Message)) == 0 {
		return itemFieldStrip{}, false
	}

	// param first: it is the machine-readable field and it carries the path
	// whole, where the message may have been shortened by the provider.
	paths := make([]string, 0, 2)
	if param, ok := envelope.Error.Param.(string); ok {
		paths = append(paths, param)
	}
	for _, match := range itemFieldPathPattern.FindAllStringSubmatch(envelope.Error.Message, -1) {
		paths = append(paths, match[1])
	}
	for _, path := range paths {
		strip, ok := parseItemFieldPath(path)
		if !ok || !payloadCarriesItemField(payload, strip) {
			continue
		}
		return strip, true
	}
	return itemFieldStrip{}, false
}

func payloadCarriesItemField(payload map[string]any, strip itemFieldStrip) bool {
	items, ok := payload[strip.Root].([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject {
			continue
		}
		if _, carries := object[strip.Field]; carries {
			return true
		}
	}
	return false
}

// stripItemFields deletes each named field from every item of its array and
// reports the paths it actually removed something from.
//
// Every changed item is copied first, and that is not tidiness. buildPlan clones
// the public request with cloneMap, which is one level deep: every candidate's
// payload shares the caller's own turn objects. Deleting a key in place would
// edit the request itself, so the next candidate — a different provider, perhaps
// a different protocol — would be planned from a body the caller never sent, and
// the attempt evidence would describe a request that was never on the wire.
func stripItemFields(payload map[string]any, strips []itemFieldStrip) []string {
	removed := make([]string, 0, len(strips))
	for _, strip := range strips {
		items, ok := payload[strip.Root].([]any)
		if !ok {
			continue
		}
		copied := make([]any, len(items))
		changed := false
		for index, item := range items {
			object, isObject := item.(map[string]any)
			if !isObject {
				copied[index] = item
				continue
			}
			if _, carries := object[strip.Field]; !carries {
				copied[index] = item
				continue
			}
			replacement := cloneMap(object)
			delete(replacement, strip.Field)
			copied[index] = replacement
			changed = true
		}
		if !changed {
			continue
		}
		payload[strip.Root] = copied
		removed = append(removed, strip.String())
	}
	return removed
}

func compatibilityItemStripKey(modelID string) string {
	return "compatibility:strip-item:" + modelID
}

// learnedItemFieldStrips reports the nested fields this route's provider has
// already rejected, so the next request goes out without them.
func (s *Server) learnedItemFieldStrips(ctx context.Context, modelID string) []itemFieldStrip {
	values, err := s.redis.SMembers(ctx, compatibilityItemStripKey(modelID)).Result()
	if err != nil {
		return nil
	}
	strips := make([]itemFieldStrip, 0, len(values))
	for _, value := range values {
		if strip, ok := parseItemFieldStrip(value); ok {
			strips = append(strips, strip)
		}
	}
	// A set iterates in a different order every time. Sorting keeps the removed
	// list on an attempt row, and the learned panel that renders it, from
	// reshuffling under the operator between two identical requests.
	slices.SortFunc(strips, func(a, b itemFieldStrip) int { return strings.Compare(a.String(), b.String()) })
	return strips
}

func (s *Server) rememberItemFieldStrip(ctx context.Context, modelID string, strip itemFieldStrip) {
	key := compatibilityItemStripKey(modelID)
	pipe := s.redis.TxPipeline()
	pipe.SAdd(ctx, key, strip.String())
	pipe.Expire(ctx, key, adaptiveCompatibilityTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		s.logger.Warn("nested field compatibility cache write failed", "model_id", modelID, "error", err)
	}
}
