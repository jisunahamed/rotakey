package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const messageModeAudio = "audio"

// Endpoint capabilities belong to the upstream model, never to its public alias.
// Store them in the existing capability profile so export/import retains them.
func audioModelType(model, declared string) string {
	switch strings.ToLower(strings.TrimSpace(declared)) {
	case "audio_transcription", "audio_speech":
		return strings.ToLower(strings.TrimSpace(declared))
	}
	model = strings.ToLower(model)
	if model == "whisper-large-v3" || model == "whisper-large-v3-turbo" || model == "whisper-1" {
		return "audio_transcription"
	}
	if strings.HasPrefix(model, "canopylabs/orpheus-") {
		return "audio_speech"
	}
	return ""
}

func audioModelEndpoints(model, declared string) []string {
	switch audioModelType(model, declared) {
	case "audio_speech":
		return []string{"audio/speech"}
	case "audio_transcription":
		if model == "whisper-large-v3-turbo" {
			return []string{"audio/transcriptions"}
		}
		return []string{"audio/transcriptions", "audio/translations"}
	}
	return nil
}

func audioRouteSupports(route routeRuntime, endpoint string) bool {
	if route.Provider.APIFormat == "anthropic" {
		return false
	}
	for _, supported := range audioModelEndpoints(route.Model.UpstreamModel, route.Model.CapabilityProfile["model_type"]) {
		if endpoint == supported {
			return true
		}
	}
	return false
}

// Read multipart in memory under the audio request limit; never spill private
// audio to a temporary file. Keep every file/header/repeated field on retries.
func audioPayload(raw []byte, contentType string) (map[string]any, error) {
	kind, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, fmt.Errorf("use multipart/form-data for audio uploads or application/json for an audio URL or speech request")
	}
	if kind == "application/json" {
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err != nil || payload == nil {
			return nil, fmt.Errorf("request body must be a JSON object")
		}
		return payload, nil
	}
	if kind != "multipart/form-data" || params["boundary"] == "" {
		return nil, fmt.Errorf("audio uploads require multipart/form-data with a boundary")
	}
	payload := map[string]any{}
	reader := multipart.NewReader(bytes.NewReader(raw), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid multipart audio upload")
		}
		if part.FormName() == "file" {
			payload["file"] = true
		}
		if part.FileName() == "" && part.FormName() != "file" {
			value, err := io.ReadAll(io.LimitReader(part, (64<<10)+1))
			if err != nil || len(value) > 64<<10 {
				return nil, fmt.Errorf("audio form field is too large")
			}
			name := part.FormName()
			if _, exists := payload[name]; exists && name == "model" {
				return nil, fmt.Errorf("send exactly one model field")
			}
			payload[name] = string(value)
		}
		if err := part.Close(); err != nil {
			return nil, fmt.Errorf("invalid multipart audio upload")
		}
	}
	return payload, nil
}

func encodeAudioRequest(req dispatchRequest, model string) ([]byte, string, error) {
	kind, params, err := mime.ParseMediaType(req.ContentType)
	if err != nil {
		return nil, "", err
	}
	if kind == "application/json" {
		payload := cloneRequest(req.Public)
		payload["model"] = model
		encoded, err := json.Marshal(payload)
		return encoded, "application/json", err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	reader := multipart.NewReader(bytes.NewReader(req.Raw), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}
		dst, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, "", err
		}
		if part.FormName() == "model" {
			_, err = io.WriteString(dst, model)
		} else {
			_, err = io.Copy(dst, part)
		}
		if err != nil {
			return nil, "", err
		}
		if err = part.Close(); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func (s *Server) handleAudio(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	id := requestIDFromContext(r.Context())
	endpoint := strings.TrimPrefix(r.URL.Path, "/v1/")
	s.beginActiveRequest(id, endpoint, started)
	defer s.activeRequests.Delete(id)
	req := dispatchRequest{RequestID: id, Started: started, Endpoint: endpoint, PublicMode: messageModeAudio, ContentType: r.Header.Get("Content-Type")}
	reject := func(status int, code, message string) {
		s.rejectPool(w, r, req, routeRuntime{Model: ModelRoute{PublicAlias: req.Alias}}, status, code, message)
	}
	var err error
	limit := s.cfg.MaxAudioBytes
	if limit <= 0 {
		limit = 26 << 20
	}
	req.Raw, err = readRequestBody(w, r, limit)
	if err != nil {
		reject(http.StatusRequestEntityTooLarge, "request_too_large", "Audio request exceeds the configured audio upload limit.")
		return
	}
	req.Public, err = audioPayload(req.Raw, req.ContentType)
	if err != nil {
		reject(http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	req.Alias, _ = req.Public["model"].(string)
	if req.Alias == "" {
		reject(http.StatusBadRequest, "model_required", "A public model alias is required.")
		return
	}
	if endpoint != "audio/speech" && req.Public["file"] == nil && req.Public["url"] == nil {
		reject(http.StatusBadRequest, "audio_required", "Provide an audio file or URL.")
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		reject(http.StatusServiceUnavailable, "settings_unavailable", "Gateway settings are unavailable.")
		return
	}
	routes, err := s.resolveRoutes(r.Context(), req.Alias, settings.RoutingMode)
	if err != nil {
		reject(http.StatusNotFound, "model_not_found", "The requested model alias is not enabled.")
		return
	}
	usable := make([]routeRuntime, 0, len(routes))
	for _, route := range routes {
		if audioRouteSupports(route, endpoint) {
			usable = append(usable, route)
		}
	}
	if len(usable) == 0 {
		reject(http.StatusBadRequest, "unsupported_endpoint", "This model does not support "+endpoint+". Whisper Turbo supports transcription only; use whisper-large-v3 for translation.")
		return
	}
	s.updateActiveRequest(id, func(log *RequestLog) { log.ModelAlias = req.Alias; log.ProviderName = usable[0].Provider.Name })
	s.servePooled(w, r, req, usable, "")
}

func isAudioPath(path string) bool {
	return strings.HasSuffix(path, "/audio/transcriptions") || strings.HasSuffix(path, "/audio/translations") || strings.HasSuffix(path, "/audio/speech")
}

// A short valid WAV is sufficient to test the upload protocol. Silence is not
// evidence of transcription accuracy, so this probe checks availability only.
func audioProbeRequest(model, kind string) (string, []byte, string) {
	if kind == "audio_speech" {
		voice := "troy"
		if strings.Contains(model, "arabic") {
			voice = "fahad"
		}
		body, _ := json.Marshal(map[string]any{"model": model, "input": "Hello.", "voice": voice, "response_format": "wav"})
		return "/audio/speech", body, "application/json"
	}
	wav := make([]byte, 44+3200)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 16000)
	binary.LittleEndian.PutUint32(wav[28:], 32000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 3200)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", model)
	_ = writer.WriteField("response_format", "json")
	file, _ := writer.CreateFormFile("file", "probe.wav")
	_, _ = file.Write(wav)
	_ = writer.Close()
	return "/audio/transcriptions", body.Bytes(), writer.FormDataContentType()
}
