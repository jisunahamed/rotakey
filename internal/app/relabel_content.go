package app

// The conversation's labels, spoken in the vocabulary of the wire they are
// about to travel.
//
// Each protocol names the parts of a turn differently. Chat Completions and
// Anthropic Messages call a piece of text "text", whoever wrote it; the
// Responses API types it by direction — input_text from the caller,
// output_text or refusal from the model. A request built by one client for
// one protocol never mixes them. A conversation carried through Rotakey does:
// switching models mid-chat is the thing this gateway exists to allow, so a
// history recorded against gpt-5.6-sol comes back labeled input_text inside a
// request now bound for an Anthropic model, and a history of Claude replies
// comes back labeled "text" inside a request bound for /responses.
//
// Some validators shrug at a foreign label — OpenAI reads the role and moves
// on. Others read the label and refuse the request. Both of these are
// production rejections of the same conversation the same client had been
// having one model earlier:
//
//	invalid_value        Invalid value: 'input_text'. Supported values are:
//	                     'output_text' and 'refusal'.            (Azure, /responses)
//	invalid_request      messages.7.content.0.tool_result.content.0: Input tag
//	                     'input_text' found using 'type' does not match any of
//	                     the expected tags: … 'text' …            (Anthropic)
//
// This is not repaired by learning from the rejection, the way an unknown
// vendor field is (compat_item_fields.go), because there is nothing only the
// provider knows: the protocols themselves define which labels exist on which
// wire, and the gateway's own translators already write them correctly
// (responses_translate.go). What was missing is the same knowledge applied to
// a passthrough body. So every outbound conversation is relabeled before it
// is sent — deterministically, on the first attempt, with the rewrite named
// in the replaced-parameters evidence rather than performed in silence.
//
// The relabeling is deliberately narrow. It touches only the three labels
// whose meaning is the same everywhere — input_text, output_text, text — and
// only the ones foreign to the wire at hand. Every label it does not know
// (tool_result, image, refusal, browser_state, whatever a provider adds next)
// passes through untouched, because rewriting a label whose payload shape
// might differ is how a repair becomes a corruption.

// conversationShape names the vocabulary a plan's body must speak. The wire
// endpoint decides it for the two OpenAI shapes; an Anthropic provider has
// exactly one.
func conversationShape(format, path string) string {
	if format == "anthropic" {
		return "anthropic"
	}
	if path == "/responses" {
		return "responses"
	}
	return "chat"
}

// textLabels are the three spellings of "a piece of text" across the three
// protocols. Only a label in this set is ever rewritten, and only into
// another member of it: all three carry their payload in the same "text"
// field, so the rewrite is a rename and can lose nothing.
var textLabels = map[string]bool{"text": true, "input_text": true, "output_text": true}

// dropMisplacedStreamOptions removes stream_options from a body it cannot be
// valid on. The parameter belongs to Chat Completions and only means anything
// while a stream is on — it asks for a usage frame inside one — so on the
// other two wires it is always foreign, and on Chat without stream: true it is
// dead weight some validators shrug at and Azure refuses:
//
//	400 invalid_request_error
//	The 'stream_options' parameter is only allowed when 'stream' is enabled.
//
// Deleting it can lose nothing the request asked for: with no stream there is
// no frame to include the usage in. The gateway reads include_usage for its
// own accounting before any plan is built, so that is unaffected.
func dropMisplacedStreamOptions(payload map[string]any, shape string) bool {
	if _, carries := payload["stream_options"]; !carries {
		return false
	}
	if shape == "chat" && payload["stream"] == true {
		return false
	}
	delete(payload, "stream_options")
	return true
}

// relabelConversation rewrites foreign text labels in the payload's
// conversation to the wire's own, and reports what it renamed as from→to
// pairs for the plan's replaced-parameters evidence.
//
// Every changed item and part is copied first, for the same reason
// stripItemFields copies: buildPlan clones the public request one level deep,
// so the items, their content slices and the parts inside them are the
// caller's own objects, shared by every candidate's plan. Writing through
// them would edit the request itself, and the next candidate would be planned
// from a body nobody sent.
func relabelConversation(payload map[string]any, shape string) map[string]string {
	applied := map[string]string{}
	switch shape {
	case "anthropic":
		// Anthropic spells every piece of text "text", whoever said it, and it
		// is the one protocol that nests a conversation inside a part: a
		// tool_result block carries its own content array, which is where the
		// production rejection above found its input_text.
		relabelItems(payload, "messages", func(string) string { return "text" }, true, applied)
		// system may be a plain string or an array of text blocks; the block
		// form takes the same relabeling, with no role to consult.
		if parts, ok := payload["system"].([]any); ok {
			if relabeled, changed := relabelParts(parts, "text", false, applied); changed {
				payload["system"] = relabeled
			}
		}
	case "responses":
		// The Responses API types text by direction, so the role decides what a
		// foreign label becomes: the model's own earlier replies are
		// output_text, everything else spoke to the model and is input_text.
		relabelItems(payload, "input", func(role string) string {
			if role == "assistant" {
				return "output_text"
			}
			return "input_text"
		}, false, applied)
	default:
		relabelItems(payload, "messages", func(string) string { return "text" }, false, applied)
	}
	return applied
}

// relabelItems walks one conversation array. target maps an item's role to the
// wire's spelling for text; nested says whether a part's own content array is
// walked too (only Anthropic nests one).
func relabelItems(payload map[string]any, root string, target func(role string) string, nested bool, applied map[string]string) {
	items, ok := payload[root].([]any)
	if !ok {
		return
	}
	copied := make([]any, len(items))
	changed := false
	for index, item := range items {
		object, isObject := item.(map[string]any)
		if !isObject {
			copied[index] = item
			continue
		}
		role, _ := object["role"].(string)
		parts, hasParts := object["content"].([]any)
		if role == "" || !hasParts {
			// A plain-string content is already label-free. And an item with no
			// role is not a turn — a function_call, a reasoning item — so nothing
			// in it is conversation text this pass is allowed to touch, whatever
			// arrays it carries; a reasoning item's content is one.
			copied[index] = item
			continue
		}
		relabeled, partChanged := relabelParts(parts, target(role), nested, applied)
		if !partChanged {
			copied[index] = item
			continue
		}
		replacement := cloneMap(object)
		replacement["content"] = relabeled
		copied[index] = replacement
		changed = true
	}
	if changed {
		payload[root] = copied
	}
}

func relabelParts(parts []any, to string, nested bool, applied map[string]string) ([]any, bool) {
	copied := make([]any, len(parts))
	changed := false
	for index, part := range parts {
		object, isObject := part.(map[string]any)
		if !isObject {
			copied[index] = part
			continue
		}
		label, _ := object["type"].(string)
		replacement := object
		copiedPart := false
		if textLabels[label] && label != to {
			replacement = cloneMap(object)
			copiedPart = true
			replacement["type"] = to
			applied[label] = to
			changed = true
		}
		if nested {
			if inner, ok := replacement["content"].([]any); ok {
				if relabeled, innerChanged := relabelParts(inner, to, nested, applied); innerChanged {
					if !copiedPart {
						replacement = cloneMap(object)
					}
					replacement["content"] = relabeled
					changed = true
				}
			}
		}
		copied[index] = replacement
	}
	return copied, changed
}
