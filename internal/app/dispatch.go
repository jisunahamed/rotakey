package app

import (
	"context"
	"encoding/json"
	"time"
)

// dispatchRequest carries everything about a public request that stays constant
// while the gateway fails over between candidates.
type dispatchRequest struct {
	RequestID string
	Started   time.Time
	Endpoint  string
	// PublicMode is the protocol the caller spoke: messageModeChat,
	// messageModeResponses or messageModeAnthropic.
	PublicMode         string
	Alias              string
	Raw                []byte
	Public             map[string]any
	Stream             bool
	IncludeOpenAIUsage bool
	// MaxWait is how long the limiter may block waiting for capacity.
	MaxWait time.Duration
}

// dispatchState holds the compatibility repairs learned while serving one
// request. They are reapplied to every later attempt, including attempts against
// a different provider, because the offending field came from the caller.
type dispatchState struct {
	Removed  []string
	Replaced map[string]string
	// RemovedItemFields holds fields rejected from inside a turn rather than from
	// the top level of the request — see compat_item_fields.go. Kept apart from
	// Removed because the two are applied by different code: one deletes a key
	// from the payload, the other from every item of an array inside it.
	RemovedItemFields []itemFieldStrip
	// NativeResponsesUnavailable holds the model IDs whose provider answered 404
	// at /responses during this request. A provider that never implemented the
	// endpoint rejects every model the same way, so the retry translates to Chat
	// Completions instead of asking a second time.
	NativeResponsesUnavailable map[string]bool
	// PreferNativeResponses holds the model IDs whose provider rejected a Chat
	// Completions request by naming /responses. An observed 404 always wins over
	// this inferred preference, so a model in both maps is planned as Chat.
	PreferNativeResponses map[string]bool
}

// upstreamPlan is one candidate's translated view of the public request.
type upstreamPlan struct {
	// Payload is the decoded body that produced Encoded. Compatibility repairs
	// inspect it to decide which field an upstream rejected.
	Payload       map[string]any
	Encoded       []byte
	Path          string
	Format        string
	Translated    bool
	InputEstimate int64
	TokenCost     int64
	Removed       []string
	Replaced      map[string]string
	// SwitchedToResponses marks a plan that reached /responses only because the
	// provider asked for it, so the answer can say so in a response header.
	SwitchedToResponses bool
	// ResponsesUnavailable carries the learned 404 into the attempt, which stops
	// it from reading a Chat rejection as an invitation to try /responses again.
	ResponsesUnavailable bool
}

// attemptOutcome reports what a single upstream attempt did. Done means the
// client response is already written and the request is finished; any other
// outcome means nothing was written and the next candidate may be tried.
type attemptOutcome struct {
	Done              bool
	Record            AttemptRecord
	Status            int
	ErrorCode         string
	ErrorMessage      string
	ResponseBody      []byte
	UpstreamRequestID string
	Truncated         bool
	InputTokens       int64
	OutputTokens      int64
	// LearnedStrip and LearnedReplace carry compatibility repairs discovered on
	// this attempt. The caller folds them into dispatchState and rebuilds every
	// candidate's plan, so the repair survives a switch to another provider.
	LearnedStrip   []string
	LearnedReplace map[string]string
	// LearnedItemStrip carries a field the provider rejected from inside a turn.
	// Its zero value means nothing was learned, which is why it is a value rather
	// than a slice: at most one path is named per rejection.
	LearnedItemStrip itemFieldStrip
	// ResetSkips clears the per-request skip set, because a request-shape
	// failure is not the credential's fault.
	ResetSkips bool
	// NativeResponsesMissing reports that this candidate's provider has no
	// /responses endpoint, so every later plan for that model translates to Chat
	// Completions.
	NativeResponsesMissing bool
	// NativeResponsesPreferred reports that the provider rejected this request at
	// Chat Completions and named /responses, so every later plan for that model
	// is translated up into the Responses endpoint instead.
	NativeResponsesPreferred bool
	// Compatibility marks that this attempt consumed a compatibility retry.
	Compatibility bool
}

// buildPlan translates the caller's request into the shape one candidate's
// provider expects. Model-wise pools can mix Anthropic and OpenAI providers
// behind the same public alias, so every attempt gets its own translation.
func (s *Server) buildPlan(ctx context.Context, req dispatchRequest, route routeRuntime, state dispatchState) (upstreamPlan, error) {
	format := route.Provider.APIFormat
	if format == "" {
		format = "openai"
	}
	plan := upstreamPlan{
		Format:               format,
		Replaced:             map[string]string{},
		ResponsesUnavailable: state.NativeResponsesUnavailable[route.Model.ID],
	}
	payload := cloneMap(req.Public)
	var err error

	switch {
	case format == "anthropic" && req.PublicMode == messageModeAnthropic:
		plan.Path = "/messages"
	case format == "anthropic":
		chat := payload
		if req.PublicMode == messageModeResponses {
			var lost []string
			if chat, lost, err = translateResponsesRequest(payload); err != nil {
				return upstreamPlan{}, err
			}
			plan.Removed = lost
		}
		var dropped []string
		if payload, dropped, err = translateChatRequestToAnthropic(chat); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = appendUniqueStrings(plan.Removed, dropped...)
		plan.Path = "/messages"
		plan.Translated = true
	case req.PublicMode == messageModeAnthropic && servesNativeResponses(route, state) &&
		(!route.Model.SupportsChat || prefersNativeResponses(route, state)):
		// A route that publishes only Responses can still answer an Anthropic
		// caller: the request crosses through Chat's shape into Responses, and the
		// answer crosses back. Refusing here was the last protocol dead end. A
		// chat-capable route arrives here once its provider has asked for
		// /responses, because the Chat endpoint rejects the request outright.
		chat, dropped, chatErr := translateAnthropicRequestToChat(payload)
		if chatErr != nil {
			return upstreamPlan{}, chatErr
		}
		responses, lost, responsesErr := translateChatRequestToResponses(chat)
		if responsesErr != nil {
			return upstreamPlan{}, responsesErr
		}
		payload = responses
		plan.Removed = appendUniqueStrings(dropped, lost...)
		plan.Path = "/responses"
		plan.Translated = true
		// Reaching this arm with a chat-capable route means the learned
		// preference, not the route's own shape, chose the endpoint.
		plan.SwitchedToResponses = route.Model.SupportsChat
	case req.PublicMode == messageModeAnthropic:
		var dropped []string
		if payload, dropped, err = translateAnthropicRequestToChat(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = dropped
		plan.Path = "/chat/completions"
		plan.Translated = true
	case req.PublicMode == messageModeResponses && servesNativeResponses(route, state):
		plan.Path = "/responses"
		// A route that never claimed Responses is here only because the provider
		// asked for the endpoint, which spares this caller the Chat translation.
		plan.SwitchedToResponses = !route.Model.SupportsResponses
	case req.PublicMode == messageModeResponses:
		var dropped []string
		if payload, dropped, err = translateResponsesRequest(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = dropped
		plan.Path = "/chat/completions"
		plan.Translated = true
	case servesNativeResponses(route, state) && (!route.Model.SupportsChat || prefersNativeResponses(route, state)):
		// The upstream serves only Responses, or it has rejected this model's Chat
		// requests and named /responses, so a Chat caller's request is translated
		// up into it rather than rejected as an unsupported endpoint.
		var dropped []string
		if payload, dropped, err = translateChatRequestToResponses(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = dropped
		plan.Path = "/responses"
		plan.Translated = true
		plan.SwitchedToResponses = route.Model.SupportsChat
	default:
		plan.Path = "/chat/completions"
	}

	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, route.Model.StripParameters)...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, s.learnedCompatibilityParameters(ctx, route.Model.ID))...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, state.Removed)...)
	// Nested strips run after the translations above, because a translated plan
	// builds its own turn objects and the caller's extension never reaches them —
	// there is nothing to delete, and stripItemFields says so by removing nothing.
	plan.Removed = appendUniqueStrings(plan.Removed, stripItemFields(payload, s.learnedItemFieldStrips(ctx, route.Model.ID))...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripItemFields(payload, state.RemovedItemFields)...)
	learned := s.learnedCompatibilityReplacements(ctx, route.Model.ID, plan.wireEndpoint())
	if learned == nil {
		// A Redis outage returns no learned repairs at all, and writing this
		// request's own repairs into that nil map would panic mid-flight.
		learned = map[string]string{}
	}
	for from, to := range state.Replaced {
		learned[from] = to
	}
	plan.Replaced = applyCompatibilityReplacements(payload, learned)

	if format == "anthropic" {
		if numberAsInt64(payload["max_tokens"]) <= 0 {
			payload["max_tokens"] = route.Model.DefaultMaxOutputTokens
		}
		plan.InputEstimate = estimateInputTokens(req.Raw, route.Model.Tokenizer)
		plan.TokenCost = plan.InputEstimate + numberAsInt64(payload["max_tokens"])
	} else {
		input, output := prepareTokenReservation(
			payload, plan.wireEndpoint(),
			route.Model.DefaultMaxOutputTokens, route.Model.Tokenizer, req.Raw,
		)
		plan.InputEstimate = input
		plan.TokenCost = input + output
	}
	payload["model"] = upstreamModelForProvider(route.Provider, route.Model.UpstreamModel)
	plan.Payload = payload
	if plan.Encoded, err = json.Marshal(payload); err != nil {
		return upstreamPlan{}, err
	}
	return plan, nil
}

// servesNativeResponses reports whether a request may be sent to the provider's
// own /responses endpoint. A route configured for native Responses against a
// provider that has since answered 404 there is translated to Chat Completions
// instead, because asking again can only fail the same way. A provider that
// rejected Chat Completions by naming /responses counts as well: its own error
// is better evidence than the route's unverified configuration.
func servesNativeResponses(route routeRuntime, state dispatchState) bool {
	if state.NativeResponsesUnavailable[route.Model.ID] {
		return false
	}
	return route.Model.SupportsResponses || state.PreferNativeResponses[route.Model.ID]
}

// prefersNativeResponses reports whether the provider asked for /responses for
// this model. An observed 404 outranks the request, so a model in both maps is
// planned as Chat Completions.
func prefersNativeResponses(route routeRuntime, state dispatchState) bool {
	return state.PreferNativeResponses[route.Model.ID] && !state.NativeResponsesUnavailable[route.Model.ID]
}

// wireEndpoint names the upstream endpoint for compatibility learning, which is
// keyed per endpoint shape rather than per public protocol.
func (p upstreamPlan) wireEndpoint() string {
	if p.Path == "/responses" {
		return "responses"
	}
	return "chat"
}

// routeSupportsRequest reports whether a route can serve the caller's protocol.
// Every protocol can now be translated into every other, so the only route that
// cannot serve is one whose upstream publishes no usable endpoint at all. A
// caller's choice of protocol is no longer a reason to refuse.
func routeSupportsRequest(route routeRuntime, req dispatchRequest) bool {
	if route.Provider.APIFormat == "anthropic" {
		return route.Model.SupportsMessages || route.Model.SupportsChat || route.Model.SupportsResponses
	}
	return route.Model.SupportsChat || route.Model.SupportsResponses
}
