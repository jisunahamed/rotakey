package app

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// successfulUpstreamBody inspects the actual envelope: compatible providers can
// return an Anthropic Message even when configured as OpenAI (and vice versa).
// Unrecognized bodies are forwarded intact, including provider error envelopes
// carried in HTTP 200, rather than being replaced with an empty model answer.
func successfulUpstreamBody(req dispatchRequest, plan upstreamPlan, body []byte) ([]byte, int64, int64) {
	var envelope map[string]any
	if json.Unmarshal(body, &envelope) != nil {
		return body, 0, 0
	}
	if _, exists := envelope["error"]; exists {
		return body, 0, 0
	}
	actual := ""
	if envelope["type"] == "message" && envelope["role"] == "assistant" {
		actual = messageModeAnthropic
	} else if _, ok := envelope["choices"].([]any); ok {
		actual = messageModeChat
	} else if envelope["object"] == "response" {
		actual = messageModeResponses
	}
	if req.PublicMode == messageModeEmbeddings {
		input, output := extractUsage(envelope, "embeddings")
		return body, input, output
	}
	if actual == "" {
		return body, 0, 0
	}
	var input, output int64
	if actual == messageModeAnthropic {
		input, output = extractAnthropicUsage(envelope)
	} else {
		input, output = extractUsage(envelope, actual)
	}
	if actual == req.PublicMode {
		return body, input, output
	}
	actualPlan := plan
	actualPlan.Format = "openai"
	actualPlan.Path = "/chat/completions"
	if actual == messageModeAnthropic {
		actualPlan.Format = "anthropic"
	}
	if actual == messageModeResponses {
		actualPlan.Path = "/responses"
	}
	translated, _, _, err := translateUpstreamResponse(req, actualPlan, body)
	if err != nil {
		return body, input, output
	}
	return translated, input, output
}

// Inspect only the envelope, never demand text in a first chunk. Replay every
// inspected byte so metadata, comments, errors and late tool/text deltas survive.
func successfulStreamSource(source io.Reader, commentWriters ...io.Writer) (io.Reader, string) {
	reader := bufio.NewReaderSize(source, 64<<10)
	prefix := bytes.Buffer{}
	metadataOnly := true
	for prefix.Len() < 64<<10 {
		line, err := reader.ReadSlice('\n')
		prefix.Write(line)
		trimmed := bytes.TrimSpace(line)
		metadataOnly = metadataOnly && (len(trimmed) == 0 || bytes.HasPrefix(trimmed, []byte(":")))
		// Providers send keepalives while reasoning. Holding those comments until
		// the first text delta makes the next gateway falsely time out a live call.
		if metadataOnly && len(commentWriters) > 0 && commentWriters[0] != nil {
			if _, writeErr := io.Copy(commentWriters[0], bytes.NewReader(prefix.Bytes())); writeErr != nil {
				return &streamReadError{err: writeErr}, ""
			}
			if flusher, ok := commentWriters[0].(interface{ Flush() }); ok {
				flusher.Flush()
			}
			prefix.Reset()
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.HasPrefix(line, []byte("data:")) && len(data) > 0 {
			var envelope map[string]any
			actual := ""
			if json.Unmarshal(data, &envelope) == nil {
				kind, _ := envelope["type"].(string)
				if kind == "message_start" || kind == "content_block_delta" || kind == "content_block_start" || kind == "message_stop" {
					actual = messageModeAnthropic
				} else if strings.HasPrefix(kind, "response.") {
					actual = messageModeResponses
				} else if _, ok := envelope["choices"]; ok {
					actual = messageModeChat
				}
			}
			return io.MultiReader(bytes.NewReader(prefix.Bytes()), reader), actual
		}
		if err != nil {
			break
		}
	}
	return io.MultiReader(bytes.NewReader(prefix.Bytes()), reader), ""
}

type streamReadError struct{ err error }

func (r *streamReadError) Read([]byte) (int, error) { return 0, r.err }

// EOF is a valid end to a provider 2xx body even without a terminal marker.
// Relay chunks as they arrive, without adding gateway error events.
func relaySuccessfulStream(w http.ResponseWriter, source io.Reader, capture io.Writer) error {
	buffer := make([]byte, 32<<10)
	for {
		n, err := source.Read(buffer)
		if n > 0 {
			written, writeErr := w.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
			if capture != nil {
				_, _ = capture.Write(buffer[:n])
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (s *Server) relaySuccessfulBody(w http.ResponseWriter, r *http.Request, req dispatchRequest, plan upstreamPlan, response *http.Response, source io.Reader, reserved reservation, record AttemptRecord, upstreamID string) attemptOutcome {
	copyResponseHeaders(w, req.PublicMode, response.Header)
	s.setCompatibilityHeaders(w, plan)
	if kind := response.Header.Get("Content-Type"); kind != "" {
		w.Header().Set("Content-Type", kind)
	}
	w.WriteHeader(response.StatusCode)
	var capture *limitedCapture
	var captureWriter io.Writer
	if s.cfg.CaptureBytes > 0 {
		capture = &limitedCapture{limit: s.cfg.CaptureBytes}
		captureWriter = capture
	}
	err := relaySuccessfulStream(w, source, captureWriter)
	if err != nil {
		s.logger.Warn("provider success body relay interrupted", "request_id", req.RequestID, "error", err)
	}
	_ = s.limiter.AdjustTokens(r.Context(), reserved, 0)
	outcome := attemptOutcome{Done: true, ResponseComplete: err == nil, Record: record, Status: response.StatusCode, UpstreamRequestID: upstreamID}
	if capture != nil {
		outcome.ResponseBody = append([]byte(nil), capture.Bytes()...)
		outcome.Truncated = capture.truncated
	}
	return outcome
}
