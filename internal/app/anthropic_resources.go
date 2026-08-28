package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleAnthropicFileCreate(w http.ResponseWriter, r *http.Request) {
	provider, credentials, err := s.defaultAnthropicProvider(r.Context())
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", err.Error())
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.")
		return
	}
	selected, _, retry, _, err := s.selectCredentialWithDiagnostics(r.Context(), "anthropic:file", credentials, 0, map[string]bool{}, time.Duration(settings.MaxWaitMS)*time.Millisecond)
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Rate limiter is unavailable.")
		return
	}
	if selected == nil {
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
		}
		writeAnthropicError(w, r, http.StatusTooManyRequests, "rate_limit_error", "No API key has capacity for a file upload.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxFileBytes)
	target := strings.TrimRight(provider.BaseURL, "/") + "/files"
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, target, r.Body)
	applyProviderHeaders(request, provider, selected.Secret)
	request.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	forwardAnthropicHeaders(request.Header, r.Header)
	response, err := doProviderRequest(provider, request)
	if err != nil {
		s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The upstream provider could not be reached.")
		return
	}
	defer response.Body.Close()
	copyAnthropicHeaders(w.Header(), response.Header)
	body, truncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	if readErr != nil || truncated {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The file response was too large or invalid.")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}
	var state map[string]any
	if json.Unmarshal(body, &state) != nil {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The provider returned invalid file metadata.")
		return
	}
	upstreamID := fmt.Sprint(state["id"])
	if upstreamID == "" || upstreamID == "<nil>" {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The provider did not return a file ID.")
		return
	}
	publicID, err := newID("file")
	if err != nil {
		writeAnthropicError(w, r, http.StatusInternalServerError, "api_error", "File mapping identifier could not be created.")
		return
	}
	state["id"] = publicID
	encoded, _ := json.Marshal(state)
	if _, err := s.db.Exec(r.Context(), `INSERT INTO anthropic_resources (id, resource_type, upstream_id, provider_id, credential_id, state) VALUES ($1,'file',$2,$3,$4,$5)`, publicID, upstreamID, provider.ID, selected.ID, encoded); err != nil {
		writeAnthropicError(w, r, http.StatusInternalServerError, "api_error", "File mapping could not be saved.")
		return
	}
	s.markCredentialSuccess(r.Context(), selected.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(encoded)
}

func (s *Server) handleAnthropicFileList(w http.ResponseWriter, r *http.Request) {
	s.listAnthropicResources(w, r, "file")
}

func (s *Server) handleAnthropicFileGet(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "file", "", false)
}

func (s *Server) handleAnthropicFileDelete(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "file", "", true)
}

func (s *Server) handleAnthropicFileContent(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "file", "/content", false)
}

func (s *Server) handleAnthropicBatchCreate(w http.ResponseWriter, r *http.Request) {
	raw, err := readRequestBody(w, r, s.cfg.MaxBatchBytes)
	if err != nil {
		writeAnthropicError(w, r, http.StatusRequestEntityTooLarge, "request_too_large", "Batch request exceeds 256 MB.")
		return
	}
	payload, err := decodeJSONMap(raw)
	if err != nil {
		writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "Batch body is not valid JSON.")
		return
	}
	requests, ok := payload["requests"].([]any)
	if !ok || len(requests) == 0 {
		writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "A batch needs at least one request.")
		return
	}
	var provider Provider
	aliases := map[string]string{}
	modelCosts := map[string]modelReservationCost{}
	forcedCredential := ""
	for _, rawRequest := range requests {
		item, _ := rawRequest.(map[string]any)
		params, _ := item["params"].(map[string]any)
		alias, _ := params["model"].(string)
		route, routeErr := s.loadRoute(r.Context(), alias)
		if routeErr != nil || !route.Model.SupportsMessages {
			writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "Every batch item must use an enabled Messages model alias.")
			return
		}
		if route.Provider.APIFormat != "anthropic" {
			writeAnthropicError(w, r, http.StatusBadRequest, "unsupported_feature", "Message Batches require native Anthropic provider routes.")
			return
		}
		if provider.ID != "" && provider.ID != route.Provider.ID {
			writeAnthropicError(w, r, http.StatusBadRequest, "resource_affinity_conflict", "Every batch item must resolve to the same Anthropic provider.")
			return
		}
		if provider.ID == "" {
			provider = route.Provider
		}
		aliases[route.Model.UpstreamModel] = alias
		params["model"] = route.Model.UpstreamModel
		if numberAsInt64(params["max_tokens"]) <= 0 {
			params["max_tokens"] = route.Model.DefaultMaxOutputTokens
		}
		itemTokenCost := estimateInputTokens(mustJSON(params), route.Model.Tokenizer) + numberAsInt64(params["max_tokens"])
		cost := modelCosts[route.Model.ID]
		cost.Requests++
		cost.Tokens += itemTokenCost
		if itemTokenCost > cost.TPR {
			cost.TPR = itemTokenCost
		}
		modelCosts[route.Model.ID] = cost
		pinned, affinityErr := s.resolveMessageResourceAffinity(r.Context(), params, provider.ID)
		if affinityErr != nil {
			writeAnthropicError(w, r, http.StatusBadRequest, "resource_affinity_conflict", affinityErr.Error())
			return
		}
		if pinned != "" && forcedCredential != "" && pinned != forcedCredential {
			writeAnthropicError(w, r, http.StatusBadRequest, "resource_affinity_conflict", "Batch file references are pinned to different API keys.")
			return
		}
		if pinned != "" {
			forcedCredential = pinned
		}
	}
	modelIDs := make([]string, 0, len(modelCosts))
	for modelID := range modelCosts {
		modelIDs = append(modelIDs, modelID)
	}
	credentials, err := s.loadCredentialsForModels(r.Context(), provider.ID, modelIDs)
	if err == nil && forcedCredential != "" {
		credentials = filterCredentials(credentials, forcedCredential)
	}
	if err != nil || len(credentials) == 0 {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "This provider has no enabled API key.")
		return
	}
	// selectBatchCredential skips unusable keys without recording why, so a pool
	// that is wholly rejected or spent is named here instead of being reported as
	// "no capacity", which would invite a retry that cannot succeed.
	if blocked := credentialBlockDecisions(credentials); blocked != nil {
		reason := soleBlockingReason(blocked)
		if message, terminal := unavailablePoolMessage(reason, provider.Name); terminal {
			writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", message)
			return
		}
		if reason == "balance_exhausted" {
			writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error",
				"Every API key on "+provider.Name+" has spent its balance. Add balance to one of them to resume.")
			return
		}
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.")
		return
	}
	selected, _, retry, err := s.selectBatchCredential(r.Context(), provider.ID, credentials, modelCosts, time.Duration(settings.MaxWaitMS)*time.Millisecond)
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Rate limiter is unavailable.")
		return
	}
	if selected == nil {
		if retry > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(max(1, int(retry.Seconds()))))
		}
		writeAnthropicError(w, r, http.StatusTooManyRequests, "rate_limit_error", "No API key has capacity for this batch.")
		return
	}
	encoded, _ := json.Marshal(payload)
	request, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(provider.BaseURL, "/")+"/messages/batches", bytes.NewReader(encoded))
	applyProviderHeaders(request, provider, selected.Secret)
	forwardAnthropicHeaders(request.Header, r.Header)
	response, err := doProviderRequest(provider, request)
	if err != nil {
		s.markCredentialFailure(r.Context(), selected.ID, 0, 0)
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The upstream provider could not be reached.")
		return
	}
	defer response.Body.Close()
	copyAnthropicHeaders(w.Header(), response.Header)
	body, truncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	if readErr != nil || truncated {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The batch response was too large or invalid.")
		return
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		s.markCredentialFailure(r.Context(), selected.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(body)
		return
	}
	var state map[string]any
	if json.Unmarshal(body, &state) != nil {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The provider returned invalid batch metadata.")
		return
	}
	upstreamID := fmt.Sprint(state["id"])
	if upstreamID == "" || upstreamID == "<nil>" {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The provider did not return a batch ID.")
		return
	}
	publicID, err := newID("batch")
	if err != nil {
		writeAnthropicError(w, r, http.StatusInternalServerError, "api_error", "Batch mapping identifier could not be created.")
		return
	}
	state["id"] = publicID
	stateJSON, _ := json.Marshal(state)
	aliasesJSON, _ := json.Marshal(aliases)
	if _, err := s.db.Exec(r.Context(), `INSERT INTO anthropic_resources (id, resource_type, upstream_id, provider_id, credential_id, state, model_aliases) VALUES ($1,'batch',$2,$3,$4,$5,$6)`, publicID, upstreamID, provider.ID, selected.ID, stateJSON, aliasesJSON); err != nil {
		writeAnthropicError(w, r, http.StatusInternalServerError, "api_error", "Batch mapping could not be saved.")
		return
	}
	s.markCredentialSuccess(r.Context(), selected.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(stateJSON)
}

func (s *Server) handleAnthropicBatchList(w http.ResponseWriter, r *http.Request) {
	s.listAnthropicResources(w, r, "batch")
}
func (s *Server) handleAnthropicBatchGet(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "batch", "", false)
}
func (s *Server) handleAnthropicBatchDelete(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "batch", "", true)
}
func (s *Server) handleAnthropicBatchCancel(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "batch", "/cancel", false)
}
func (s *Server) handleAnthropicBatchResults(w http.ResponseWriter, r *http.Request) {
	s.proxyAnthropicResource(w, r, "batch", "/results", false)
}

func (s *Server) defaultAnthropicProvider(ctx context.Context) (Provider, []credentialRuntime, error) {
	settings, _, err := s.settings(ctx)
	if err != nil || settings.DefaultAnthropicProviderID == "" {
		return Provider{}, nil, fmt.Errorf("configure a default Anthropic resource provider in System settings")
	}
	provider, err := scanProvider(s.db.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id=$1 AND enabled=TRUE AND api_format='anthropic'`, settings.DefaultAnthropicProviderID))
	if err != nil {
		return Provider{}, nil, fmt.Errorf("the default Anthropic resource provider is unavailable")
	}
	credentials, err := s.loadCredentials(ctx, provider.ID, "")
	if err != nil || len(credentials) == 0 {
		return Provider{}, nil, fmt.Errorf("the default Anthropic provider has no enabled API key")
	}
	// The pool now carries rejected and spent keys so the reason can be named. The
	// callers of this turn the error text into their own message, so saying which
	// of the two it is here is the whole benefit.
	if blocked := credentialBlockDecisions(credentials); blocked != nil {
		switch soleBlockingReason(blocked) {
		case "quarantined":
			return Provider{}, nil, fmt.Errorf("every API key on the default Anthropic provider was rejected by the provider")
		case "disabled":
			return Provider{}, nil, fmt.Errorf("every API key on the default Anthropic provider is turned off")
		case "balance_exhausted":
			return Provider{}, nil, fmt.Errorf("every API key on the default Anthropic provider has spent its balance")
		}
	}
	return provider, credentials, nil
}

func (s *Server) listAnthropicResources(w http.ResponseWriter, r *http.Request, kind string) {
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		_, _ = fmt.Sscan(value, &limit)
		if limit < 1 {
			limit = 1
		}
		if limit > 1000 {
			limit = 1000
		}
	}
	afterID := strings.TrimSpace(r.URL.Query().Get("after_id"))
	beforeID := strings.TrimSpace(r.URL.Query().Get("before_id"))
	if afterID != "" && beforeID != "" {
		writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "Use only one of after_id or before_id.")
		return
	}

	type listedResource struct {
		id        string
		state     []byte
		createdAt time.Time
	}
	ascending := false
	var rows interface {
		Next() bool
		Scan(...any) error
		Close()
		Err() error
	}
	var err error
	cursorID := afterID
	if cursorID == "" {
		cursorID = beforeID
	}
	if cursorID == "" {
		rows, err = s.db.Query(r.Context(), `
			SELECT id, state, created_at FROM anthropic_resources
			WHERE resource_type=$1 ORDER BY created_at DESC, id DESC LIMIT $2
		`, kind, limit+1)
	} else {
		var cursorCreated time.Time
		if scanErr := s.db.QueryRow(r.Context(), `SELECT created_at FROM anthropic_resources WHERE id=$1 AND resource_type=$2`, cursorID, kind).Scan(&cursorCreated); scanErr != nil {
			writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "The pagination cursor is invalid.")
			return
		}
		if afterID != "" {
			rows, err = s.db.Query(r.Context(), `
				SELECT id, state, created_at FROM anthropic_resources
				WHERE resource_type=$1 AND (created_at, id) < ($2, $3)
				ORDER BY created_at DESC, id DESC LIMIT $4
			`, kind, cursorCreated, cursorID, limit+1)
		} else {
			ascending = true
			rows, err = s.db.Query(r.Context(), `
				SELECT id, state, created_at FROM anthropic_resources
				WHERE resource_type=$1 AND (created_at, id) > ($2, $3)
				ORDER BY created_at ASC, id ASC LIMIT $4
			`, kind, cursorCreated, cursorID, limit+1)
		}
	}
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Resources could not be loaded.")
		return
	}
	defer rows.Close()
	listed := []listedResource{}
	for rows.Next() {
		var item listedResource
		if rows.Scan(&item.id, &item.state, &item.createdAt) == nil {
			listed = append(listed, item)
		}
	}
	if rows.Err() != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Resources could not be loaded.")
		return
	}
	hasMore := len(listed) > limit
	if hasMore {
		listed = listed[:limit]
	}
	if ascending {
		slices.Reverse(listed)
	}
	data := make([]any, 0, len(listed))
	for _, item := range listed {
		var state map[string]any
		if json.Unmarshal(item.state, &state) == nil {
			state["id"] = item.id
			data = append(data, state)
		}
	}
	var firstID, lastID any
	if len(data) > 0 {
		firstID = data[0].(map[string]any)["id"]
		lastID = data[len(data)-1].(map[string]any)["id"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "has_more": hasMore, "first_id": firstID, "last_id": lastID})
}

func (s *Server) proxyAnthropicResource(w http.ResponseWriter, r *http.Request, kind, suffix string, remove bool) {
	resource, provider, credential, aliases, err := s.loadAnthropicResource(r.Context(), r.PathValue("id"), kind)
	if err != nil {
		writeAnthropicError(w, r, http.StatusNotFound, "not_found_error", "The resource was not found.")
		return
	}
	settings, _, err := s.settings(r.Context())
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Gateway settings are unavailable.")
		return
	}
	selected, _, retry, _, err := s.selectCredentialWithDiagnostics(r.Context(), "anthropic:"+kind, []credentialRuntime{credential}, 0, map[string]bool{}, time.Duration(settings.MaxWaitMS)*time.Millisecond)
	if err != nil {
		writeAnthropicError(w, r, http.StatusServiceUnavailable, "api_error", "Rate limiter is unavailable.")
		return
	}
	if selected == nil {
		if retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int(math.Ceil(retry.Seconds())))))
		}
		writeAnthropicError(w, r, http.StatusTooManyRequests, "rate_limit_error", "The resource's API key is at capacity.")
		return
	}
	basePath := "/files/"
	if kind == "batch" {
		basePath = "/messages/batches/"
	}
	target := strings.TrimRight(provider.BaseURL, "/") + basePath + url.PathEscape(resource.UpstreamID) + suffix
	method := r.Method
	request, _ := http.NewRequestWithContext(r.Context(), method, target, nil)
	applyProviderHeaders(request, provider, credential.Secret)
	forwardAnthropicHeaders(request.Header, r.Header)
	response, err := doProviderRequest(provider, request)
	if err != nil {
		s.markCredentialFailure(r.Context(), credential.ID, 0, 0)
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The upstream provider could not be reached.")
		return
	}
	defer response.Body.Close()
	copyAnthropicHeaders(w.Header(), response.Header)
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		s.markCredentialSuccess(r.Context(), credential.ID)
	} else {
		s.markCredentialFailure(r.Context(), credential.ID, response.StatusCode, parseRetryAfter(response.Header.Get("Retry-After")))
	}
	if suffix == "/content" {
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	if suffix == "/results" {
		s.rewriteBatchResults(w, response, aliases)
		return
	}
	body, truncated, readErr := boundedBody(response.Body, s.cfg.MaxResponseBytes)
	if readErr != nil || truncated {
		writeAnthropicError(w, r, http.StatusBadGateway, "api_error", "The resource response was too large or invalid.")
		return
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		body = rewriteResourceJSON(body, resource.ID, resource.UpstreamID, aliases)
		if remove {
			_, _ = s.db.Exec(r.Context(), `DELETE FROM anthropic_resources WHERE id=$1`, resource.ID)
		} else {
			_, _ = s.db.Exec(r.Context(), `UPDATE anthropic_resources SET state=$2, updated_at=NOW() WHERE id=$1`, resource.ID, body)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func (s *Server) loadAnthropicResource(ctx context.Context, id, kind string) (AnthropicResource, Provider, credentialRuntime, map[string]string, error) {
	var resource AnthropicResource
	var state, aliasesRaw []byte
	err := s.db.QueryRow(ctx, `SELECT id, resource_type, upstream_id, provider_id, credential_id, state, model_aliases, created_at, updated_at FROM anthropic_resources WHERE id=$1 AND resource_type=$2`, id, kind).Scan(&resource.ID, &resource.ResourceType, &resource.UpstreamID, &resource.ProviderID, &resource.CredentialID, &state, &aliasesRaw, &resource.CreatedAt, &resource.UpdatedAt)
	if err != nil {
		return resource, Provider{}, credentialRuntime{}, nil, err
	}
	provider, err := scanProvider(s.db.QueryRow(ctx, `SELECT `+providerColumns+` FROM providers WHERE id=$1 AND enabled=TRUE`, resource.ProviderID))
	if err != nil {
		return resource, Provider{}, credentialRuntime{}, nil, err
	}
	credentials, err := s.loadCredentials(ctx, provider.ID, "")
	if err != nil {
		return resource, Provider{}, credentialRuntime{}, nil, err
	}
	filtered := filterCredentials(credentials, resource.CredentialID)
	if len(filtered) == 0 {
		return resource, Provider{}, credentialRuntime{}, nil, fmt.Errorf("pinned credential unavailable")
	}
	// A pinned key is used even when it is quarantined or spent. It is the only key
	// the upstream will accept for this file or batch, so no other key is a
	// substitute: trying it and passing the provider's own answer back tells the
	// caller more than refusing before the request is sent.
	aliases := map[string]string{}
	_ = json.Unmarshal(aliasesRaw, &aliases)
	resource.State, resource.ModelAliases = state, aliasesRaw
	return resource, provider, filtered[0], aliases, nil
}

func (s *Server) rewriteBatchResults(w http.ResponseWriter, response *http.Response, aliases map[string]string) {
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(w, response.Body)
		return
	}
	reader := bufio.NewScanner(response.Body)
	buffer := make([]byte, 64<<10)
	reader.Buffer(buffer, 32<<20)
	for reader.Scan() {
		line := rewriteResourceJSON(reader.Bytes(), "", "", aliases)
		_, _ = w.Write(append(line, '\n'))
	}
}

func rewriteResourceJSON(body []byte, publicID, upstreamID string, aliases map[string]string) []byte {
	var payload any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	var rewrite func(any)
	rewrite = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if key == "id" && publicID != "" && child == upstreamID {
					typed[key] = publicID
				}
				if key == "model" {
					if alias, ok := aliases[fmt.Sprint(child)]; ok {
						typed[key] = alias
					}
				}
				rewrite(child)
			}
		case []any:
			for _, child := range typed {
				rewrite(child)
			}
		}
	}
	rewrite(payload)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func doProviderRequest(provider Provider, request *http.Request) (*http.Response, error) {
	client, err := upstreamClient(provider)
	if err != nil {
		return nil, err
	}
	return client.Do(request)
}
func filterCredentials(values []credentialRuntime, id string) []credentialRuntime {
	result := []credentialRuntime{}
	for _, value := range values {
		if value.ID == id {
			result = append(result, value)
		}
	}
	return result
}
func mustJSON(value any) []byte { body, _ := json.Marshal(value); return body }
