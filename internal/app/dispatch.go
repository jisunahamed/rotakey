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
			if chat, err = translateResponsesRequest(payload); err != nil {
				return upstreamPlan{}, err
			}
		}
		if payload, err = translateChatRequestToAnthropic(chat); err != nil {
			return upstreamPlan{}, err
		}
		plan.Path = "/messages"
		plan.Translated = true
	case req.PublicMode == messageModeAnthropic:
		if payload, err = translateAnthropicRequestToChat(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Path = "/chat/completions"
		plan.Translated = true
	case req.PublicMode == messageModeResponses && route.Model.SupportsResponses:
		plan.Path = "/responses"
	case req.PublicMode == messageModeResponses:
		if payload, err = translateResponsesRequest(payload); err != nil {
			return upstreamPlan{}, err
		}
		plan.Path = "/chat/completions"
		plan.Translated = true
	default:
		plan.Path = "/chat/completions"
	}

	plan.Removed = stripTopLevelParameters(payload, route.Model.StripParameters)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, s.learnedCompatibilityParameters(ctx, route.Model.ID))...)
	plan.Removed = appendUniqueStrings(plan.Removed, stripTopLevelParameters(payload, state.Removed)...)
	learned := s.learnedCompatibilityReplacements(ctx, route.Model.ID, plan.wireEndpoint())
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

// wireEndpoint names the upstream endpoint for compatibility learning, which is
// keyed per endpoint shape rather than per public protocol.
func (p upstreamPlan) wireEndpoint() string {
	if p.Path == "/responses" {
		return "responses"
	}
	return "chat"
}

// supportsEndpoint reports whether a route can serve the caller's protocol at
// all, so unusable providers leave the pool before any request is sent.
func routeSupportsRequest(route routeRuntime, req dispatchRequest) bool {
	switch req.PublicMode {
	case messageModeAnthropic:
		return route.Model.SupportsMessages
	case messageModeResponses:
		if route.Provider.APIFormat == "anthropic" {
			return route.Model.SupportsResponses || route.Model.SupportsChat
		}
		return route.Model.SupportsResponses || route.Model.SupportsChat
	default:
		return route.Model.SupportsChat
	}
}
