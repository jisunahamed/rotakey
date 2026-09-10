package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RepairPolicy selects an existing connection, never a caller-supplied URL or secret.
type RepairPolicy struct {
	Enabled          bool     `json:"enabled"`
	ModelID          string   `json:"model_id"`
	Mode             string   `json:"mode"`
	Permissions      []string `json:"permissions"`
	DailyTokens      int64    `json:"daily_tokens"`
	DiagnosisSeconds int      `json:"diagnosis_seconds"`
	OutputTokens     int      `json:"output_tokens"`
	RouteIDs         []string `json:"route_ids"`
	Version          int64    `json:"version"`
}

var repairTools = []string{"set_parameter", "remove_parameter", "switch_endpoint", "set_timeout", "reset_cooldown", "refresh_connection", "select_credential", "validate_credential", "set_route_enabled"}

func defaultRepairPolicy() RepairPolicy {
	return RepairPolicy{Mode: "observe", Permissions: []string{}, RouteIDs: []string{}, DailyTokens: 100000, DiagnosisSeconds: 10, OutputTokens: 2048}
}

func (p RepairPolicy) permits(action string) bool {
	if !p.Enabled || p.Mode == "observe" || !slices.Contains(repairTools, action) {
		return false
	}
	return p.Mode == "full" || (p.Mode == "auto" && slices.Contains(repairTools[:3], action)) || (p.Mode == "custom" && slices.Contains(p.Permissions, action))
}

func (p RepairPolicy) validate() error {
	if !slices.Contains([]string{"observe", "auto", "full", "custom"}, p.Mode) || p.DailyTokens < 1 || p.DailyTokens > 100000000 || p.DiagnosisSeconds < 1 || p.DiagnosisSeconds > 10 || p.OutputTokens < 128 || p.OutputTokens > 2048 {
		return errors.New("Invalid repair mode or budget")
	}
	if p.Enabled && p.ModelID == "" {
		return errors.New("Select a repair model")
	}
	for _, tool := range p.Permissions {
		if !slices.Contains(repairTools, tool) {
			return errors.New("Unknown repair permission")
		}
	}
	return nil
}

type RepairProposal struct {
	MatchValue     json.RawMessage `json:"match_value,omitempty"`
	Diagnosis      string          `json:"diagnosis"`
	Action         string          `json:"action"`
	Parameter      string          `json:"parameter,omitempty"`
	Value          json.RawMessage `json:"value,omitempty"`
	ExpectedResult string          `json:"expected_result"`
}

type RepairAttempt struct {
	TestTokens     int64           `json:"test_tokens"`
	TestDurationMS int64           `json:"test_duration_ms"`
	Proposal       RepairProposal  `json:"proposal"`
	Before         json.RawMessage `json:"before,omitempty"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason,omitempty"`
	AgentTokens    int64           `json:"agent_tokens"`
	DurationMS     int64           `json:"duration_ms"`
}

type RepairIncident struct {
	ID           string          `json:"id"`
	RequestID    string          `json:"request_id"`
	RouteID      string          `json:"route_id"`
	RouteName    string          `json:"route_name,omitempty"`
	ProviderName string          `json:"provider_name,omitempty"`
	Category     string          `json:"category"`
	Error        string          `json:"error"`
	Status       string          `json:"status"`
	Attempts     []RepairAttempt `json:"attempts"`
	CreatedAt    time.Time       `json:"created_at,omitempty"`
}

func repairCategory(status int) string {
	switch {
	case status == 401 || status == 403:
		return "credential_failure"
	case status == 429:
		return "rate_limit"
	case status == 400 || status == 422:
		return "request_incompatibility"
	case status == 0 || status >= 500:
		return "provider_outage"
	default:
		return "provider_rejection"
	}
}

func (s *Server) repairPolicy(ctx context.Context) RepairPolicy {
	p := defaultRepairPolicy()
	if s.db == nil {
		return p
	}
	var raw []byte
	var version int64
	if s.db.QueryRow(ctx, "SELECT policy, version FROM repair_policy WHERE id=1").Scan(&raw, &version) != nil {
		return defaultRepairPolicy()
	}
	if json.Unmarshal(raw, &p) != nil {
		return defaultRepairPolicy()
	}
	p.Version = version
	return p
}

func (s *Server) repairRoute(ctx context.Context, id string) (routeRuntime, error) {
	return scanRoute(s.db.QueryRow(ctx, `SELECT `+routeColumns+` FROM model_routes m JOIN providers p ON p.id=m.provider_id WHERE m.id=$1 AND `+routeFilter, id))
}

func (s *Server) handleRepairPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, map[string]any{"policy": s.repairPolicy(r.Context()), "tools": repairTools, "service_restart_supported": false})
		return
	}
	var p RepairPolicy
	if decodeJSON(w, r, 32<<10, &p) != nil {
		return
	}
	if err := p.validate(); err != nil {
		writeError(w, 400, "invalid_repair_policy", err.Error())
		return
	}
	if p.Enabled {
		if _, err := s.repairRoute(r.Context(), p.ModelID); err != nil {
			writeError(w, 400, "invalid_repair_model", "Choose an enabled model route.")
			return
		}
	}
	for _, id := range p.RouteIDs {
		if _, err := s.repairRoute(r.Context(), id); err != nil {
			writeError(w, 400, "invalid_repair_route", "Rollout routes must be enabled model routes.")
			return
		}
	}
	version := p.Version
	p.Version = 0
	raw, _ := json.Marshal(p)
	var next int64
	if err := s.db.QueryRow(r.Context(), `UPDATE repair_policy SET policy=$1,version=version+1,updated_at=now() WHERE id=1 AND version=$2 RETURNING version`, raw, version).Scan(&next); err != nil {
		writeError(w, 409, "repair_policy_conflict", "Policy changed; reload before saving.")
		return
	}
	p.Version = next
	writeJSON(w, 200, p)
}

func (s *Server) handleRepairIncidents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(r.Context(), `
		SELECT i.evidence, i.created_at, COALESCE(m.public_alias,''), COALESCE(p.name,'')
		FROM repair_incidents i
		LEFT JOIN model_routes m ON m.id=i.route_id
		LEFT JOIN providers p ON p.id=m.provider_id
		WHERE ($1='' OR i.request_id=$1)
		ORDER BY i.created_at DESC LIMIT 100
	`, r.URL.Query().Get("request_id"))
	if err != nil {
		writeError(w, 503, "repair_unavailable", "Repair history is unavailable.")
		return
	}
	defer rows.Close()
	items := []RepairIncident{}
	for rows.Next() {
		var raw json.RawMessage
		var item RepairIncident
		var createdAt time.Time
		var routeName, providerName string
		if rows.Scan(&raw, &createdAt, &routeName, &providerName) == nil && json.Unmarshal(raw, &item) == nil {
			// Stored evidence predates display metadata. Keep database-owned values
			// after decoding so a stale JSON field can never impersonate them.
			item.CreatedAt, item.RouteName, item.ProviderName = createdAt, routeName, providerName
			items = append(items, item)
		}
	}
	if rows.Err() != nil {
		writeError(w, 503, "repair_unavailable", "Repair history is unavailable.")
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) storeRepair(ctx context.Context, incident *RepairIncident) {
	if s.db == nil {
		return
	}
	raw, _ := json.Marshal(incident)
	_, err := s.db.Exec(ctx, `INSERT INTO repair_incidents(id,request_id,route_id,status,evidence) VALUES($1,$2,$3,$4,$5) ON CONFLICT(id) DO UPDATE SET status=excluded.status,evidence=excluded.evidence`, incident.ID, incident.RequestID, incident.RouteID, incident.Status, raw)
	if err != nil && s.logger != nil {
		s.logger.Warn("repair incident could not be stored", "request_id", incident.RequestID)
	}
}

// Only scalar generation options can be changed. Conversation and tool fields
// never enter this executor, even in full administration mode.
func validateRepairProposal(p RepairProposal, payload map[string]any) error {
	if len(p.Diagnosis) > 2000 || len(p.ExpectedResult) > 1000 {
		return errors.New("Diagnosis is too long")
	}
	switch p.Action {
	case "set_parameter":
		var n float64
		if strings.TrimSpace(string(p.Value)) == "null" || json.Unmarshal(p.Value, &n) != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return errors.New("Expected a finite number")
		}
		switch p.Parameter {
		case "max_tokens", "max_completion_tokens", "max_output_tokens":
			if _, ok := payload[p.Parameter]; !ok {
				return errors.New("Token parameter must match the current wire")
			}
			if n < 1 || n > 32768 || n != math.Trunc(n) {
				return errors.New("Token cap outside repair bounds")
			}
		case "temperature":
			if n < 0 || n > 2 {
				return errors.New("Temperature outside bounds")
			}
		case "top_p":
			if n < 0 || n > 1 {
				return errors.New("top_p outside bounds")
			}
		case "frequency_penalty", "presence_penalty":
			if n < -2 || n > 2 {
				return errors.New("Penalty outside bounds")
			}
		default:
			return errors.New("Parameter cannot be changed by repair")
		}
	case "remove_parameter":
		if !slices.Contains([]string{"temperature", "top_p", "frequency_penalty", "presence_penalty", "seed"}, p.Parameter) {
			return errors.New("Parameter cannot be removed by repair")
		}
	case "switch_endpoint":
		var endpoint string
		if json.Unmarshal(p.Value, &endpoint) != nil || !slices.Contains([]string{"chat", "responses"}, endpoint) {
			return errors.New("Unknown endpoint")
		}
	case "set_timeout":
		var seconds int
		if json.Unmarshal(p.Value, &seconds) != nil || seconds < 1 || seconds > 900 {
			return errors.New("Timeout outside bounds")
		}
	case "set_route_enabled":
		var enabled bool
		if strings.TrimSpace(string(p.Value)) == "null" || json.Unmarshal(p.Value, &enabled) != nil {
			return errors.New("Expected route enabled boolean")
		}
	case "select_credential":
		var id string
		if json.Unmarshal(p.Value, &id) != nil || id == "" {
			return errors.New("Expected credential reference")
		}
	case "reset_cooldown", "refresh_connection", "validate_credential":
	default:
		return errors.New("Unknown repair action")
	}
	return nil
}

func applyRepairProposal(payload map[string]any, p RepairProposal) bool {
	before, _ := json.Marshal(payload)
	switch p.Action {
	case "set_parameter":
		var value any
		if json.Unmarshal(p.Value, &value) != nil {
			return false
		}
		payload[p.Parameter] = value
	case "remove_parameter":
		delete(payload, p.Parameter)
	default:
		return false
	}
	after, _ := json.Marshal(payload)
	return string(before) != string(after)
}

var tokenConstraintPattern = regexp.MustCompile(`(?i)\b(max_tokens|max_completion_tokens|max_output_tokens)\b.{0,35}?(greater than or equal to|greater than|at least|less than or equal to|less than|at most|>=|<=)\s*([0-9]+)\b`)

func tokenConstraintRepair(message string, payload map[string]any) (RepairProposal, bool) {
	m := tokenConstraintPattern.FindStringSubmatch(message)
	if len(m) == 0 {
		return RepairProposal{}, false
	}
	n, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil || n > 32768 {
		return RepairProposal{}, false
	}
	op := strings.ToLower(m[2])
	current := numberAsInt64(payload[m[1]])
	if op == "greater than" {
		n++
	}
	if op == "less than" {
		n--
	}
	lower := strings.Contains(op, "greater") || op == "at least" || op == ">="
	if (lower && current >= n) || (!lower && current <= n) {
		return RepairProposal{}, false
	}
	p := RepairProposal{Diagnosis: "Provider rejected the output token bound.", Action: "set_parameter", Parameter: m[1], Value: json.RawMessage(strconv.FormatInt(n, 10)), ExpectedResult: "Provider accepts the output token cap."}
	return p, validateRepairProposal(p, payload) == nil
}

func repairRuleKey(route routeRuntime, endpoint string) string {
	// Versions are part of the key: an operator update retires old facts without a scan.
	return fmt.Sprintf("repair:rule:%s:%s:%x", route.Model.ID, endpoint, sha256.Sum256([]byte(route.Model.UpdatedAt.String()+route.Provider.UpdatedAt.String())))
}

func (s *Server) learnedRepairs(ctx context.Context, route routeRuntime, endpoint string) []RepairProposal {
	if s.redis == nil {
		return nil
	}
	raw, err := s.redis.Get(ctx, repairRuleKey(route, endpoint)).Bytes()
	if err != nil {
		return nil
	}
	var proposals []RepairProposal
	_ = json.Unmarshal(raw, &proposals)
	return proposals
}
