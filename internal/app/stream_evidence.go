package app

import (
	"encoding/json"
	"io"
	"strings"
)

// Observe upstream SSE without buffering the response or changing its bytes.
// Usage and mid-stream errors must reach accounting even when capture is off.
type streamEvidenceReader struct {
	source        io.Reader
	secret        []byte
	line          []byte
	input, output int64
	hasUsage      bool
	errorCode     string
	errorMessage  string
}

func (r *streamEvidenceReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			r.observe(r.line)
			r.line = r.line[:0]
		} else if len(r.line) < 4<<20 {
			r.line = append(r.line, b)
		}
	}
	if err == io.EOF && len(r.line) > 0 {
		r.observe(r.line)
		r.line = nil
	}
	return n, err
}

func (r *streamEvidenceReader) observe(line []byte) {
	text := strings.TrimSpace(string(line))
	if !strings.HasPrefix(text, "data:") {
		return
	}
	var chunk map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(text, "data:"))), &chunk) != nil {
		return
	}
	if chunk["error"] != nil {
		r.errorCode = "upstream_stream_error"
		r.errorMessage = upstreamErrorMessage([]byte(strings.TrimSpace(strings.TrimPrefix(text, "data:"))), r.secret)
	}
	if response, ok := chunk["response"].(map[string]any); ok {
		if response["error"] != nil {
			r.errorCode = "upstream_stream_error"
			body, _ := json.Marshal(response)
			r.errorMessage = upstreamErrorMessage(body, r.secret)
		}
		chunk = response
	}
	if message, ok := chunk["message"].(map[string]any); ok {
		chunk = message
	}
	if usage, ok := chunk["usage"].(map[string]any); ok {
		r.hasUsage = true
		if usage["prompt_tokens"] != nil || usage["input_tokens"] != nil {
			r.input = max(numberAsInt64(usage["prompt_tokens"]), numberAsInt64(usage["input_tokens"])) + numberAsInt64(usage["cache_creation_input_tokens"]) + numberAsInt64(usage["cache_read_input_tokens"])
		}
		if usage["completion_tokens"] != nil || usage["output_tokens"] != nil {
			r.output = max(numberAsInt64(usage["completion_tokens"]), numberAsInt64(usage["output_tokens"]))
		}
	}
}
