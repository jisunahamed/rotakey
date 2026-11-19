package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAudioEndpointCapabilities(t *testing.T) {
	for _, tc := range []struct {
		model, endpoint string
		allowed         bool
	}{
		{"whisper-large-v3", "audio/transcriptions", true},
		{"whisper-large-v3", "audio/translations", true},
		{"whisper-large-v3-turbo", "audio/transcriptions", true},
		{"whisper-large-v3-turbo", "audio/translations", false},
		{"canopylabs/orpheus-v1-english", "audio/speech", true},
		{"llama-3.3-70b-versatile", "audio/transcriptions", false},
	} {
		route := routeRuntime{Model: ModelRoute{UpstreamModel: tc.model, SupportsChat: true}, Provider: Provider{APIFormat: "openai"}}
		if got := routeSupportsRequest(route, dispatchRequest{PublicMode: messageModeAudio, Endpoint: tc.endpoint}); got != tc.allowed {
			t.Errorf("%s %s: %v", tc.model, tc.endpoint, got)
		}
		if audioModelType(tc.model, "") != "" && routeSupportsRequest(route, dispatchRequest{PublicMode: messageModeChat}) {
			t.Errorf("audio model offered as chat: %s", tc.model)
		}
	}
}

func TestAudioMultipartPlanPreservesUpload(t *testing.T) {
	_, raw, contentType := audioProbeRequest("public-alias", "audio_transcription")
	// Add repeated timestamps and a language without relying on ParseMultipartForm
	// which would silently normalize some multipart headers.
	_, params, _ := mime.ParseMediaType(contentType)
	var extended bytes.Buffer
	writer := multipart.NewWriter(&extended)
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		dst, _ := writer.CreatePart(part.Header)
		_, _ = io.Copy(dst, part)
	}
	_ = writer.WriteField("timestamp_granularities[]", "word")
	_ = writer.WriteField("timestamp_granularities[]", "segment")
	_ = writer.WriteField("language", "bn")
	_ = writer.Close()
	raw, contentType = extended.Bytes(), writer.FormDataContentType()
	payload, err := audioPayload(raw, contentType)
	if err != nil {
		t.Fatal(err)
	}
	req := dispatchRequest{PublicMode: messageModeAudio, Endpoint: "audio/transcriptions", Alias: "public-alias", Raw: raw, Public: payload, ContentType: contentType}
	plan, err := (&Server{}).buildPlan(context.Background(), req, routeRuntime{Provider: Provider{APIFormat: "openai"}, Model: ModelRoute{UpstreamModel: "whisper-large-v3-turbo"}}, dispatchState{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.TokenCost != 0 || plan.InputEstimate != 0 || plan.Path != "/audio/transcriptions" {
		t.Fatalf("wrong plan: %+v", plan)
	}
	upstream := httptest.NewRequest("POST", "/", bytes.NewReader(plan.Encoded))
	upstream.Header.Set("Content-Type", plan.ContentType)
	if err := upstream.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	defer upstream.MultipartForm.RemoveAll()
	if upstream.FormValue("model") != "whisper-large-v3-turbo" || upstream.FormValue("language") != "bn" {
		t.Fatal(upstream.MultipartForm.Value)
	}
	if !reflect.DeepEqual(upstream.MultipartForm.Value["timestamp_granularities[]"], []string{"word", "segment"}) {
		t.Fatal("lost repeated fields")
	}
	file, header, err := upstream.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	wav, _ := io.ReadAll(file)
	if header.Filename != "probe.wav" || len(wav) != 3244 || string(wav[:4]) != "RIFF" {
		t.Fatal("audio file corrupted")
	}
	// Auto repair must not re-encode the upload as JSON even when enabled.
	session := repairSession{request: req, policy: RepairPolicy{Enabled: true}}
	if got := session.prepare(routeRuntime{}, plan); !bytes.Equal(got.Encoded, plan.Encoded) {
		t.Fatal("repair changed audio body")
	}
}

func TestAudioJSONAndSuccessPassthrough(t *testing.T) {
	raw := []byte(`{"model":"alias","url":"https://example.com/audio.wav","timestamp_granularities":["word","segment"],"response_format":"verbose_json"}`)
	payload, err := audioPayload(raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	req := dispatchRequest{PublicMode: messageModeAudio, Raw: raw, Public: payload, ContentType: "application/json"}
	encoded, kind, err := encodeAudioRequest(req, "whisper-large-v3")
	if err != nil || kind != "application/json" {
		t.Fatal(err, kind)
	}
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	if result["model"] != "whisper-large-v3" || result["url"] != payload["url"] || !reflect.DeepEqual(result["timestamp_granularities"], payload["timestamp_granularities"]) {
		t.Fatal(result)
	}
	for _, body := range [][]byte{[]byte("বাংলা লেখা\n"), []byte(`{"text":"hi","segments":[{"start":0.2}]}`), {0, 1, 255, 127}} {
		got, in, out := successfulUpstreamBody(req, upstreamPlan{}, body)
		if !bytes.Equal(got, body) || in != 0 || out != 0 {
			t.Fatal("audio response changed")
		}
	}
	if _, err := audioPayload([]byte(`null`), "application/json"); err == nil {
		t.Fatal("accepted null JSON")
	}
	if _, err := audioPayload([]byte("broken"), "multipart/form-data"); err == nil {
		t.Fatal("accepted missing boundary")
	}
}

func TestAudioModelProfileAndValidation(t *testing.T) {
	input := modelInput{PublicAlias: "transcribe", UpstreamModel: "whisper-large-v3-turbo", SupportsChat: true, SupportsResponses: true, SupportsMessages: true}
	profile := modelCapabilityProfile(Provider{APIFormat: "openai"}, &input, "catalog")
	if input.SupportsChat || input.SupportsResponses || input.SupportsMessages || profile["audio/transcriptions"] != "native" || profile["audio/translations"] != "" {
		t.Fatal(input, profile)
	}
	if err := validateModelInput(&input); err != nil {
		t.Fatal(err)
	}
	if profile["verification"] != "catalog" {
		t.Fatal("catalog mislabeled as inference probe")
	}
}

func TestIntegrationAudioQueueAndBinaryResponse(t *testing.T) {
	var running, peak atomic.Int32
	s, id, _, _ := queueFixture(t, 2, func(w http.ResponseWriter, r *http.Request) {
		n := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		if r.URL.Path != "/audio/speech" {
			t.Error(r.URL.Path)
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload["model"] != "canopylabs/orpheus-v1-english" || payload["voice"] != "troy" {
			t.Error(payload)
		}
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write([]byte{'R', 'I', 'F', 'F', 0, 255, 128})
	})
	_, err := s.db.Exec(context.Background(), `UPDATE model_routes SET upstream_model='canopylabs/orpheus-v1-english',supports_chat=false,supports_responses=false,supports_messages=false WHERE provider_id=$1`, id)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.MaxAudioBytes = 26 << 20
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/v1/audio/speech", strings.NewReader(`{"model":"`+id+`m","input":"Hello","voice":"troy"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			s.handleAudio(w, r)
			if w.Code != 200 || w.Header().Get("Content-Type") != "audio/wav" || !bytes.Equal(w.Body.Bytes(), []byte{'R', 'I', 'F', 'F', 0, 255, 128}) {
				t.Errorf("code %d type %s body %q", w.Code, w.Header().Get("Content-Type"), w.Body.Bytes())
			}
		}()
	}
	wg.Wait()
	if peak.Load() > 2 || peak.Load() == 0 {
		t.Fatalf("peak=%d", peak.Load())
	}
}
