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
	// NativeResponsesUnavailable holds the model IDs whose provider answered 404
	// at /responses during this request. A provider that never implemented the
	// endpoint rejects every model the same way, so the retry translates to Chat
	// Completions instead of asking a second time.
	NativeResponsesUnavailable map[string]bool
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
	// ResetSkips clears the per-request skip set, because a request-shape
	// failure is not the credential's fault.
	ResetSkips bool
	// NativeResponsesMissing reports that this candidate's provider has no
	// /responses endpoint, so every later plan for that model translates to Chat
	// Completions.
	NativeResponsesMissing bool
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
	plan := upstreamPlan{Format: format, Replaced: map[string]string{}}
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
	case req.PublicMode == messageModeAnthropic && servesNativeResponses(route, state) && !route.Model.SupportsChat:
		// A route that publishes only Responses can still answer an Anthropic
		// caller: the request crosses through Chat's shape into Responses, and the
		// answer crosses back. Refusing here was the last protocol dead end.
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
	case req.PublicMode == messageModeResponses:
		var dropped []string
		if payload, dropped, err = translateResponsesRequest(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = dropped
		plan.Path = "/chat/completions"
		plan.Translated = true
	case !route.Model.SupportsChat && servesNativeResponses(route, state):
		// The upstream serves only Responses, so a Chat caller's request is
		// translated up into it rather than rejected as an unsupported endpoint.
		var dropped []string
		if payload, dropped, err = translateChatRequestToResponses(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Removed = dropped
		plan.Path = "/responses"
		plan.Translated = true
	default:
		plan.Path = "/chat/completions"
	}

	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, route.Model.StripParameters)...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, s.learnedCompatibilityParameters(ctx, route.Model.ID))...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, state.Removed)...)
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
			payload, plan.wireEndpoint(), plan.Translated,
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
// instead, because asking again can only fail the same way.
func servesNativeResponses(route routeRuntime, state dispatchState) bool {
	return route.Model.SupportsResponses && !state.NativeResponsesUnavailable[route.Model.ID]
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
