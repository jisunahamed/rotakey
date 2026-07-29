package app

import (
	"bytes"
	"net/http"
	"reflect"
	"testing"
)

type flushingRecorder struct {
	header  http.Header
	body    bytes.Buffer
	flushes int
}

func (r *flushingRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *flushingRecorder) Write(body []byte) (int, error) {
	return r.body.Write(body)
}

func (r *flushingRecorder) WriteHeader(_ int) {}

func (r *flushingRecorder) Flush() {
	r.flushes++
}

func TestCopyStreamingResponseFlushesAndCaptures(t *testing.T) {
	destination := &flushingRecorder{}
	capture := &bytes.Buffer{}
	source := bytes.NewBufferString("data: one\n\ndata: two\n\n")
	if err := copyStreamingResponse(destination, source, capture); err != nil {
		t.Fatalf("copy stream: %v", err)
	}
	if destination.body.String() != sourceString(capture) {
		t.Fatalf("destination and capture differ: destination=%q capture=%q", destination.body.String(), capture.String())
	}
	if destination.flushes == 0 {
		t.Fatal("stream response was not flushed")
	}
}

func TestCopyStreamingResponseAllowsNoCapture(t *testing.T) {
	destination := &flushingRecorder{}
	if err := copyStreamingResponse(destination, bytes.NewBufferString("data: one\n\n"), nil); err != nil {
		t.Fatalf("copy stream without capture: %v", err)
	}
	if destination.body.Len() == 0 || destination.flushes == 0 {
		t.Fatal("stream was not delivered and flushed")
	}
}

func sourceString(buffer *bytes.Buffer) string {
	return buffer.String()
}

func TestUnsupportedCompatibilityParameters(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		payload map[string]any
		want    []string
	}{
		{
			name: "azure unrecognized argument",
			body: `{"error":{"message":"Unrecognized request argument supplied: thinking","type":"invalid_request_error","param":null,"code":"unrecognized_request_argument"}}`,
			payload: map[string]any{
				"model": "Kimi-K2.5", "thinking": map[string]any{"type": "enabled"},
			},
			want: []string{"thinking"},
		},
		{
			name: "validation unsupported parameter",
			body: `{"error":{"message":"Validation: Unsupported parameter(s): ` + "`" + `reasoning_effort` + "`" + `","code":"invalid_request"}}`,
			payload: map[string]any{
				"model": "some-model", "reasoning_effort": "high",
			},
			want: []string{"reasoning_effort"},
		},
		{
			name: "param requires unsupported signal",
			body: `{"error":{"message":"thinking has an invalid value","param":"thinking","code":"invalid_value"}}`,
			payload: map[string]any{
				"thinking": "aggressive",
			},
			want: nil,
		},
		{
			name: "protected or content fields are never learned",
			body: `{"error":{"message":"Unsupported parameter(s): messages","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"messages": []any{},
			},
			want: nil,
		},
		{
			name: "reported field must be present at the top level",
			body: `{"error":{"message":"Unsupported parameter(s): verbosity","code":"unsupported_parameter"}}`,
			payload: map[string]any{
				"options": map[string]any{"verbosity": "high"},
			},
			want: nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := unsupportedCompatibilityParameters([]byte(test.body), test.payload)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parameters = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestAppendUniqueStrings(t *testing.T) {
	got := appendUniqueStrings([]string{"thinking"}, "thinking", "verbosity")
	want := []string{"thinking", "verbosity"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
}
