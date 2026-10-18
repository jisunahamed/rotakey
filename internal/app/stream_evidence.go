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
	line          []byte
	input, output int64
	hasUsage      bool
	errorCode     string
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
	}
	if response, ok := chunk["response"].(map[string]any); ok {
		if response["error"] != nil {
			r.errorCode = "upstream_stream_error"
		}
		chunk = response
	}
	if usage, ok := chunk["usage"].(map[string]any); ok {
		r.hasUsage = true
		r.input = max(numberAsInt64(usage["prompt_tokens"]), numberAsInt64(usage["input_tokens"]))
		r.output = max(numberAsInt64(usage["completion_tokens"]), numberAsInt64(usage["output_tokens"]))
	}
}
