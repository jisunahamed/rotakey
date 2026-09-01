package app

import (
	"strings"
	"testing"
)

func TestValidatePlaygroundInputDefaults(t *testing.T) {
	input := playgroundInput{Model: "  demo/model  ", Prompt: "  hello  "}
	if err := validatePlaygroundInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Model != "demo/model" || input.Prompt != "hello" {
		t.Fatalf("trimmed input = %#v", input)
	}
	if input.Protocol != playgroundProtocolAuto || input.MaxTokens != 1024 {
		t.Fatalf("defaults = protocol %q, max tokens %d", input.Protocol, input.MaxTokens)
	}
	if input.Stream {
		t.Fatal("streaming defaulted to on; the caller must ask for it")
	}
}

func TestValidatePlaygroundInputRejectsInvalidOptions(t *testing.T) {
	temperature := 2.1
	tests := []playgroundInput{
		{Prompt: "hello"},
		{Model: "demo/model"},
		{Model: "demo/model", Prompt: "hello", Protocol: "unknown"},
		{Model: "demo/model", Prompt: "hello", Temperature: &temperature},
	}
	for _, input := range tests {
		if err := validatePlaygroundInput(&input); err == nil {
			t.Fatalf("validatePlaygroundInput(%#v) succeeded", input)
		}
	}
}

// The conversations rejected here are the ones that reach a provider as a shape
// it refuses, with an upstream error message that says nothing an operator can
// act on. Each case is a real failure the gateway does not repair on its way
// out: empty turns are dropped by the Anthropic translation rather than
// rejected, and nothing anywhere normalises alternation.
func TestValidatePlaygroundInputRejectsUnsendableConversations(t *testing.T) {
	tests := []struct {
		name  string
		turns []playgroundMessage
		want  string
	}{
		{"empty", nil, "required"},
		{
			"a turn edited down to nothing",
			[]playgroundMessage{{Role: "user", Content: "hi"}, {Role: "assistant", Content: "   "}},
			"message 2 is empty",
		},
		{
			"two user turns in a row",
			[]playgroundMessage{{Role: "user", Content: "hi"}, {Role: "user", Content: "still me"}},
			"turns must alternate",
		},
		{
			"an assistant opening",
			[]playgroundMessage{{Role: "assistant", Content: "hello"}},
			"must start with a user message",
		},
		{
			"a role no protocol accepts here",
			[]playgroundMessage{{Role: "system", Content: "be concise"}},
			"only user and assistant",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := playgroundInput{Model: "demo/model", Messages: test.turns}
			err := validatePlaygroundInput(&input)
			if err == nil {
				t.Fatalf("validatePlaygroundInput accepted %#v", test.turns)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want it to mention %q", err, test.want)
			}
		})
	}
}

func TestValidatePlaygroundInputRejectsBothFormsAtOnce(t *testing.T) {
	input := playgroundInput{
		Model:    "demo/model",
		Prompt:   "hello",
		Messages: []playgroundMessage{{Role: "user", Content: "hello"}},
	}
	if err := validatePlaygroundInput(&input); err == nil {
		t.Fatal("a prompt and a conversation were accepted together; one of them would be silently ignored")
	}
}

func TestPlaygroundPayloadUsesProtocolShape(t *testing.T) {
	input := playgroundInput{Model: "demo/model", Prompt: "hello", System: "be concise", MaxTokens: 321}
	chat := playgroundPayload(input, playgroundProtocolChat)
	if chat["messages"] == nil || chat["max_tokens"] != 321 {
		t.Fatalf("chat payload = %#v", chat)
	}
	responses := playgroundPayload(input, playgroundProtocolResponses)
	if responses["instructions"] != "be concise" || responses["max_output_tokens"] != 321 {
		t.Fatalf("responses payload = %#v", responses)
	}
	items, ok := responses["input"].([]map[string]any)
	if !ok || len(items) != 1 || items[0]["role"] != "user" || items[0]["content"] != "hello" {
		t.Fatalf("responses input = %#v", responses["input"])
	}
	if responses["messages"] != nil {
		t.Fatalf("responses payload contains messages: %#v", responses)
	}
	messages := playgroundPayload(input, playgroundProtocolMessages)
	if messages["system"] != "be concise" {
		t.Fatalf("messages payload system = %#v", messages["system"])
	}
	turns, ok := messages["messages"].([]map[string]any)
	if !ok || len(turns) != 1 || turns[0]["role"] != "user" {
		t.Fatalf("messages payload messages = %#v", messages["messages"])
	}
}

// Every protocol has to carry the whole conversation, and each puts the system
// prompt somewhere different: a first message on Chat, a top-level instructions
// string on Responses, a top-level system string on Anthropic. Getting this
// wrong loses either the history or the system prompt, and both fail quietly —
// the model simply answers as though it had never been told.
func TestPlaygroundPayloadCarriesTheWholeConversation(t *testing.T) {
	input := playgroundInput{
		Model:     "demo/model",
		System:    "be concise",
		MaxTokens: 64,
		Messages: []playgroundMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "answer"},
			{Role: "user", Content: "second"},
		},
	}

	chat, _ := playgroundPayload(input, playgroundProtocolChat)["messages"].([]map[string]any)
	if len(chat) != 4 || chat[0]["role"] != "system" || chat[0]["content"] != "be concise" {
		t.Fatalf("chat messages = %#v", chat)
	}
	if chat[1]["content"] != "first" || chat[2]["content"] != "answer" || chat[3]["content"] != "second" {
		t.Fatalf("chat lost a turn: %#v", chat)
	}

	responses := playgroundPayload(input, playgroundProtocolResponses)
	items, _ := responses["input"].([]map[string]any)
	if len(items) != 3 || items[1]["role"] != "assistant" || items[2]["content"] != "second" {
		t.Fatalf("responses input = %#v", responses["input"])
	}
	if responses["instructions"] != "be concise" {
		t.Fatalf("responses lost the system prompt: %#v", responses)
	}

	anthropic := playgroundPayload(input, playgroundProtocolMessages)
	turns, _ := anthropic["messages"].([]map[string]any)
	if len(turns) != 3 || turns[0]["role"] != "user" {
		t.Fatalf("anthropic messages = %#v", anthropic["messages"])
	}
	if anthropic["system"] != "be concise" {
		t.Fatalf("anthropic lost the system prompt: %#v", anthropic)
	}
	for _, turn := range turns {
		if turn["role"] == "system" {
			t.Fatalf("the system prompt was sent as a turn, which Anthropic rejects: %#v", turns)
		}
	}
}

// A streamed Chat reply carries no usage at all unless include_usage was asked
// for, and the console prints "not reported" rather than a number it did not
// measure. Responses and Anthropic report usage in their own stream events, so
// they must not be given a Chat-only field.
func TestPlaygroundPayloadAsksForStreamedUsageWhereItIsOptional(t *testing.T) {
	input := playgroundInput{Model: "demo/model", Prompt: "hello", Stream: true, MaxTokens: 64}
	chat := playgroundPayload(input, playgroundProtocolChat)
	if chat["stream"] != true {
		t.Fatalf("chat stream = %#v", chat["stream"])
	}
	options, ok := chat["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != true {
		t.Fatalf("chat stream_options = %#v", chat["stream_options"])
	}
	for _, protocol := range []string{playgroundProtocolResponses, playgroundProtocolMessages} {
		payload := playgroundPayload(input, protocol)
		if payload["stream"] != true {
			t.Fatalf("%s stream = %#v", protocol, payload["stream"])
		}
		if payload["stream_options"] != nil {
			t.Fatalf("%s carried a Chat-only field: %#v", protocol, payload["stream_options"])
		}
	}
	quiet := playgroundPayload(playgroundInput{Model: "demo/model", Prompt: "hello"}, playgroundProtocolChat)
	if quiet["stream"] != false || quiet["stream_options"] != nil {
		t.Fatalf("non-streaming chat payload = %#v", quiet)
	}
}
