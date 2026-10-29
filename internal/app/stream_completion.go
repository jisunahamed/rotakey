package app

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// Observe only bytes successfully written to the client, after translation.
// Reading a stop event upstream alone does not prove it reached the client.
type streamCompletionWriter struct {
	http.ResponseWriter
	line         []byte
	pending      bool
	terminalSent bool
	writeFailed  bool
}

func (w *streamCompletionWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if err != nil || n != len(p) {
		w.writeFailed = true
		return n, err
	}
	for _, b := range p {
		if b != '\n' {
			if len(w.line) < 4<<20 {
				w.line = append(w.line, b)
			}
			continue
		}
		line := bytes.TrimSpace(w.line)
		if len(line) == 0 {
			w.terminalSent = w.terminalSent || w.pending
			w.pending = false
		} else if bytes.HasPrefix(line, []byte("data:")) {
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if bytes.Equal(data, []byte("[DONE]")) {
				w.pending = true
			} else {
				var event struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &event) == nil {
					switch event.Type {
					case "message_stop", "response.completed", "response.incomplete":
						w.pending = true
					}
				}
			}
		}
		w.line = w.line[:0]
	}
	return n, err
}

func (w *streamCompletionWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *streamCompletionWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
