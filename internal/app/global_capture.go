package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

type globalCaptureKey struct{}
type globalCapture struct {
	request, response bytes.Buffer
	status            int
	logged            bool
}
type captureBody struct {
	io.Reader
	io.Closer
}
type captureResponse struct {
	http.ResponseWriter
	capture *globalCapture
}

func (w *captureResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *captureResponse) WriteHeader(code int) {
	if w.capture.status == 0 {
		w.capture.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *captureResponse) Write(body []byte) (int, error) {
	if w.capture.status == 0 {
		w.capture.status = 200
	}
	n, err := w.ResponseWriter.Write(body)
	_, _ = w.capture.response.Write(body[:n])
	return n, err
}
func (w *captureResponse) Flush() {
	if w.capture.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Capture at the public boundary, including translated streams, errors and
// cancellations. Forward immediately and do not capture auth headers.
func (s *Server) captureRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.db == nil {
			next.ServeHTTP(w, r)
			return
		}
		settings, _, err := s.settings(r.Context())
		if err != nil || !settings.CaptureAllRequests {
			next.ServeHTTP(w, r)
			return
		}
		capture := &globalCapture{}
		started := time.Now()
		r = r.WithContext(context.WithValue(r.Context(), globalCaptureKey{}, capture))
		if r.Body != nil {
			r.Body = &captureBody{Reader: io.TeeReader(r.Body, &capture.request), Closer: r.Body}
		}
		next.ServeHTTP(&captureResponse{ResponseWriter: w, capture: capture}, r)
		if !capture.logged {
			var body struct {
				Model string `json:"model"`
			}
			_ = json.Unmarshal(capture.request.Bytes(), &body)
			s.storeRequestLog(r.Context(), logInput{RequestID: requestIDFromContext(r.Context()), Started: started, Endpoint: r.URL.Path, StatusCode: capture.status, Route: routeRuntime{Model: ModelRoute{PublicAlias: body.Model}}})
		}
	})
}
