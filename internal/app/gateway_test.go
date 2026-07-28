package app

import (
	"bytes"
	"net/http"
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
