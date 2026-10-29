package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type cancelOnDeliveryWriter struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
	fail   bool
}

func (w *cancelOnDeliveryWriter) Write(p []byte) (int, error) {
	if w.fail {
		w.cancel()
		return 0, context.Canceled
	}
	n, err := w.ResponseRecorder.Write(p)
	w.cancel()
	return n, err
}

func TestIntegrationCompletedResponseSurvivesLateClientCancellation(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint("interrupted=", interrupted), func(t *testing.T) {
			s, id, _, _ := queueFixture(t, 1, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, queueAnswer) })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			routes, err := s.resolveRoutes(ctx, id+"m", routingModeModel)
			if err != nil {
				t.Fatal(err)
			}
			payload := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}, "max_tokens": 8}
			raw, _ := json.Marshal(payload)
			rid, _ := newID("req")
			req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(string(raw))).WithContext(ctx)
			writer := &cancelOnDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, fail: interrupted}
			s.servePooled(writer, req, dispatchRequest{RequestID: rid, Started: time.Now(), PublicMode: messageModeChat, Alias: id + "m", Public: payload, Raw: raw}, routes, "")
			var code string
			var status int
			if err := s.db.QueryRow(context.Background(), `SELECT status_code,COALESCE(error_code,'') FROM request_logs WHERE request_id=$1`, rid).Scan(&status, &code); err != nil {
				t.Fatal(err)
			}
			if status != 200 || (interrupted && code != "cancelled") || (!interrupted && code != "") {
				t.Fatalf("interrupted=%v status=%d code=%s", interrupted, status, code)
			}
			if !interrupted && !strings.Contains(writer.Body.String(), "answer") {
				t.Fatal("completed response body missing")
			}
		})
	}
}

type cancelAtEOFReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *cancelAtEOFReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func TestCompletedStreamRecordsDeliveryBeforeAccounting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	server, candidate, plan := chatAttempt(t, "https://example.com")
	reader := &cancelAtEOFReader{Reader: strings.NewReader(body), cancel: cancel}
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	outcome := server.streamAttempt(recorder, req, dispatchRequest{Stream: true, PublicMode: messageModeChat}, candidate, plan, response, reservation{}, AttemptRecord{}, "")
	if ctx.Err() == nil || !outcome.ResponseComplete || outcome.ErrorCode != "" || recorder.Body.String() != body {
		t.Fatalf("completed stream misclassified: complete=%v error=%s ctx=%v", outcome.ResponseComplete, outcome.ErrorCode, ctx.Err())
	}
}

func TestInterruptedStreamDoesNotClaimCompleteDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"partial answer\"}}]}\n\n"
	server, candidate, plan := chatAttempt(t, "https://example.com")
	response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	writer := &cancelOnDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, fail: true}
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil).WithContext(ctx)
	outcome := server.streamAttempt(writer, req, dispatchRequest{Stream: true, PublicMode: messageModeChat}, candidate, plan, response, reservation{}, AttemptRecord{}, "")
	if !outcome.Done || outcome.ResponseComplete {
		t.Fatalf("interrupted stream claimed completion: %+v", outcome)
	}
}

type failAfterReader struct{ io.Reader }

func (r failAfterReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		return n, context.Canceled
	}
	return n, err
}

func TestDeliveredStopEventSurvivesDisconnectBeforeUpstreamEOF(t *testing.T) {
	for _, body := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\ndata: [DONE]\n\n",
		"data: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"content\":[]}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n",
	} {
		server, candidate, plan := chatAttempt(t, "https://example.com")
		_, mode := successfulStreamSource(strings.NewReader(body))
		response := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(failAfterReader{strings.NewReader(body)})}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/v1/messages", nil)
		outcome := server.streamAttempt(recorder, req, dispatchRequest{Stream: true, PublicMode: mode}, candidate, plan, response, reservation{}, AttemptRecord{}, "")
		if !outcome.ResponseComplete || outcome.ErrorCode != "" || recorder.Body.String() != body {
			t.Fatalf("delivered terminal event became failure: %+v %q", outcome, recorder.Body.String())
		}
	}
}

func TestStopEventMustBeFullyDelivered(t *testing.T) {
	w := &streamCompletionWriter{ResponseWriter: httptest.NewRecorder()}
	for _, piece := range []string{"data: {\"type\":\"message_", "stop\"}\n"} {
		_, _ = io.WriteString(w, piece)
		if w.terminalSent {
			t.Fatal("incomplete SSE frame marked delivered")
		}
	}
	_, _ = io.WriteString(w, "\n")
	if !w.terminalSent {
		t.Fatal("fully delivered SSE frame not recognized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := &streamCompletionWriter{ResponseWriter: &cancelOnDeliveryWriter{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, fail: true}}
	_, err := io.WriteString(failed, "data: [DONE]\n\n")
	if err == nil || failed.terminalSent || !failed.writeFailed || ctx.Err() == nil {
		t.Fatal("failed write marked delivered")
	}
}
