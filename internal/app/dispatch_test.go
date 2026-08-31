package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestTranslateUpstreamResponseCoversEveryPoolCombination is the correctness
// guard for a pooled alias that spans API formats: whichever provider serves the
// attempt, the caller must receive its own protocol's shape.
func TestTranslateUpstreamResponseCoversEveryPoolCombination(t *testing.T) {
	anthropicBody := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-upstream","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`)
	chatBody := []byte(`{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)

	cases := []struct {
		name       string
		mode       string
		plan       upstreamPlan
		body       []byte
		wantObject string
		wantType   string
	}{
		{
			name: "anthropic upstream to anthropic caller",
			mode: messageModeAnthropic, plan: upstreamPlan{Format: "anthropic"},
			body: anthropicBody, wantType: "message",
		},
		{
			name: "anthropic upstream to chat caller",
			mode: messageModeChat, plan: upstreamPlan{Format: "anthropic", Translated: true},
			body: anthropicBody, wantObject: "chat.completion",
		},
		{
			name: "anthropic upstream to responses caller",
			mode: messageModeResponses, plan: upstreamPlan{Format: "anthropic", Translated: true},
			body: anthropicBody, wantObject: "response",
		},
		{
			name: "openai upstream to anthropic caller",
			mode: messageModeAnthropic, plan: upstreamPlan{Format: "openai", Translated: true},
			body: chatBody, wantType: "message",
		},
		{
			name: "openai upstream to responses caller via translation",
			mode: messageModeResponses, plan: upstreamPlan{Format: "openai", Translated: true},
			body: chatBody, wantObject: "response",
		},
		{
			name: "openai upstream to chat caller passes through",
			mode: messageModeChat, plan: upstreamPlan{Format: "openai"},
			body: chatBody, wantObject: "chat.completion",
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			req := dispatchRequest{PublicMode: test.mode, Alias: "opus-5"}
			body, input, output, err := translateUpstreamResponse(req, test.plan, test.body)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("translated body is not JSON: %v\n%s", err, body)
			}
			if test.wantObject != "" && payload["object"] != test.wantObject {
				t.Fatalf("object = %v, want %q\n%s", payload["object"], test.wantObject, body)
			}
			if test.wantType != "" && payload["type"] != test.wantType {
				t.Fatalf("type = %v, want %q\n%s", payload["type"], test.wantType, body)
			}
			// The caller must always see the public alias, never the provider's
			// own upstream model name.
			if payload["model"] != "opus-5" {
				t.Fatalf("model = %v, want the public alias\n%s", payload["model"], body)
			}
			if input != 11 || output != 3 {
				t.Fatalf("usage = %d/%d, want 11/3", input, output)
			}
		})
	}
}

// TestRouteSupportsRequestAdmitsEveryTranslatableRoute pins the contract that a
// caller's choice of protocol is never a reason to refuse a route. Every public
// protocol can be translated into every upstream shape, so the only route that
// cannot serve is one whose upstream publishes no usable endpoint at all.
func TestRouteSupportsRequestAdmitsEveryTranslatableRoute(t *testing.T) {
	allModes := []string{messageModeChat, messageModeResponses, messageModeAnthropic}
	usable := map[string]routeRuntime{
		"openai chat only": {
			Model:    ModelRoute{SupportsChat: true},
			Provider: Provider{APIFormat: "openai"},
		},
		"openai responses only": {
			Model:    ModelRoute{SupportsResponses: true},
			Provider: Provider{APIFormat: "openai"},
		},
		"anthropic messages only": {
			Model:    ModelRoute{SupportsMessages: true},
			Provider: Provider{APIFormat: "anthropic"},
		},
	}
	for name, route := range usable {
		for _, mode := range allModes {
			if !routeSupportsRequest(route, dispatchRequest{PublicMode: mode}) {
				t.Fatalf("%s was refused for a %s caller", name, mode)
			}
		}
	}
	for name, route := range map[string]routeRuntime{
		"openai with no endpoint":    {Provider: Provider{APIFormat: "openai"}},
		"anthropic with no endpoint": {Provider: Provider{APIFormat: "anthropic"}},
	} {
		for _, mode := range allModes {
			if routeSupportsRequest(route, dispatchRequest{PublicMode: mode}) {
				t.Fatalf("%s was offered to a %s caller", name, mode)
			}
		}
	}
}

func TestWireEndpointIsKeyedByUpstreamShape(t *testing.T) {
	if got := (upstreamPlan{Path: "/responses"}).wireEndpoint(); got != "responses" {
		t.Fatalf("wire endpoint = %q", got)
	}
	for _, path := range []string{"/chat/completions", "/messages"} {
		if got := (upstreamPlan{Path: path}).wireEndpoint(); got != "chat" {
			t.Fatalf("wire endpoint for %s = %q, want chat", path, got)
		}
	}
}

// TestNativeResponsesFallsBackAfterEndpointMissing is the guard for an
// OpenAI-compatible provider that publishes a model catalog but implements only
// Chat Completions: the first /v1/responses call learns the endpoint is absent
// and every later plan for that route translates instead of repeating the 404.
func TestNativeResponsesFallsBackAfterEndpointMissing(t *testing.T) {
	route := routeRuntime{
		Model:    ModelRoute{ID: "mdl_1", SupportsChat: true, SupportsResponses: true},
		Provider: Provider{APIFormat: "openai"},
	}
	fresh := dispatchState{NativeResponsesUnavailable: map[string]bool{}}
	if !servesNativeResponses(route, fresh) {
		t.Fatal("a route configured for native Responses was translated on the first attempt")
	}
	learned := dispatchState{NativeResponsesUnavailable: map[string]bool{"mdl_1": true}}
	if servesNativeResponses(route, learned) {
		t.Fatal("a provider that answered 404 at /responses was asked a second time")
	}
	// The flag is per route, so one provider's missing endpoint must not divert
	// another provider in the same pool.
	sibling := routeRuntime{
		Model:    ModelRoute{ID: "mdl_2", SupportsChat: true, SupportsResponses: true},
		Provider: Provider{APIFormat: "openai"},
	}
	if !servesNativeResponses(sibling, learned) {
		t.Fatal("another route in the pool lost native Responses")
	}
	chatOnly := routeRuntime{
		Model:    ModelRoute{ID: "mdl_3", SupportsChat: true},
		Provider: Provider{APIFormat: "openai"},
	}
	if servesNativeResponses(chatOnly, fresh) {
		t.Fatal("a chat-only route was sent to /responses")
	}
}

// TestPreferredResponsesOutranksRouteConfiguration covers the opposite repair: a
// provider that rejects Chat Completions and names /responses has said more about
// the model than the route's own booleans ever did, so the preference is allowed
// to send a chat-only route to the endpoint it never claimed to support.
func TestPreferredResponsesOutranksRouteConfiguration(t *testing.T) {
	chatOnly := routeRuntime{
		Model:    ModelRoute{ID: "mdl_1", SupportsChat: true},
		Provider: Provider{APIFormat: "openai"},
	}
	preferred := dispatchState{PreferNativeResponses: map[string]bool{"mdl_1": true}}
	if !servesNativeResponses(chatOnly, preferred) || !prefersNativeResponses(chatOnly, preferred) {
		t.Fatal("a provider's own instruction to use /responses was ignored")
	}
	// An observed 404 is evidence; a preference read out of prose is a guess. The
	// pair must settle on Chat Completions rather than trade the request back and
	// forth between two endpoints.
	both := dispatchState{
		PreferNativeResponses:      map[string]bool{"mdl_1": true},
		NativeResponsesUnavailable: map[string]bool{"mdl_1": true},
	}
	if servesNativeResponses(chatOnly, both) || prefersNativeResponses(chatOnly, both) {
		t.Fatal("a 404 at /responses lost to a preference inferred from an error message")
	}
	// The preference is per route, like the 404 it competes with.
	sibling := routeRuntime{
		Model:    ModelRoute{ID: "mdl_2", SupportsChat: true},
		Provider: Provider{APIFormat: "openai"},
	}
	if servesNativeResponses(sibling, preferred) {
		t.Fatal("one route's preference diverted another route in the pool")
	}
	// A route that already publishes native Responses is not "switched" by a
	// preference: it was going there anyway.
	native := routeRuntime{
		Model:    ModelRoute{ID: "mdl_3", SupportsChat: true, SupportsResponses: true},
		Provider: Provider{APIFormat: "openai"},
	}
	if !servesNativeResponses(native, dispatchState{}) {
		t.Fatal("a native Responses route needed a preference to reach /responses")
	}
}

func TestResponsesMissingKeyIsScopedToTheRoute(t *testing.T) {
	if got := responsesMissingKey("mdl_1"); got != "compatibility:no-responses:mdl_1" {
		t.Fatalf("responses cache key = %q", got)
	}
	if responsesMissingKey("mdl_1") == responsesMissingKey("mdl_2") {
		t.Fatal("two routes share one responses cache key")
	}
	if got := responsesPreferredKey("mdl_1"); got != "compatibility:prefer-responses:mdl_1" {
		t.Fatalf("preferred responses cache key = %q", got)
	}
	// The two facts are opposites, so they must never collide on one key.
	if responsesPreferredKey("mdl_1") == responsesMissingKey("mdl_1") {
		t.Fatal("the missing and preferred endpoint facts share one cache key")
	}
}

func TestRouteModelIDsListsThePool(t *testing.T) {
	ids := routeModelIDs([]routeRuntime{
		{Model: ModelRoute{ID: "mdl_1"}},
		{Model: ModelRoute{ID: "mdl_2"}},
	})
	if len(ids) != 2 || ids[0] != "mdl_1" || ids[1] != "mdl_2" {
		t.Fatalf("pool model IDs = %v", ids)
	}
	if got := routeModelIDs(nil); len(got) != 0 {
		t.Fatalf("empty pool = %v", got)
	}
}

// TestUpstreamFailureMessageExplainsABarePathNotFound guards the operator-facing
// text for a provider that answers a plain-text "404 page not found": there is no
// JSON message to forward, so without this the console could only ever show
// "upstream_error" with no hint at what to change.
func TestUpstreamFailureMessageExplainsABarePathNotFound(t *testing.T) {
	// The provider's own words always win.
	if got := upstreamFailureMessage(http.StatusNotFound, "/responses", "model not found"); got != "model not found" {
		t.Fatalf("provider message was replaced: %q", got)
	}
	// A silent non-404 has nothing endpoint-specific to say.
	if got := upstreamFailureMessage(http.StatusForbidden, "/chat/completions", ""); got != "" {
		t.Fatalf("non-404 invented a message: %q", got)
	}
	responses := upstreamFailureMessage(http.StatusNotFound, "/responses", "")
	if !strings.Contains(responses, "Responses endpoint") {
		t.Fatalf("responses 404 message = %q", responses)
	}
	messages := upstreamFailureMessage(http.StatusNotFound, "/messages", "")
	if !strings.Contains(messages, "Messages endpoint") {
		t.Fatalf("messages 404 message = %q", messages)
	}
	chat := upstreamFailureMessage(http.StatusNotFound, "/chat/completions", "")
	if !strings.Contains(chat, "/chat/completions") {
		t.Fatalf("chat 404 message = %q", chat)
	}
}
