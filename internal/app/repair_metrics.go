package app

import (
	"net/http"
	"time"
)

func (s *Server) handleRepairMetrics(w http.ResponseWriter, r *http.Request) {
	var requests, successes, incidents, recovered, agentTokens, diagnosisMS, testTokens, testMS int64
	if err := s.db.QueryRow(r.Context(), `SELECT count(*),count(*) FILTER (WHERE status_code>=200 AND status_code<300 AND coalesce(error_code,'')='') FROM request_logs WHERE created_at>=now()-interval '24 hours'`).Scan(&requests, &successes); err != nil {
		writeError(w, 503, "repair_metrics_unavailable", "Request metrics are unavailable.")
		return
	}
	if err := s.db.QueryRow(r.Context(), `SELECT count(*), count(*) FILTER (WHERE status LIKE 'verified%' OR status='recovered_elsewhere') FROM repair_incidents WHERE created_at>=now()-interval '24 hours'`).Scan(&incidents, &recovered); err != nil {
		writeError(w, 503, "repair_metrics_unavailable", "Repair metrics are unavailable.")
		return
	}
	if err := s.db.QueryRow(r.Context(), `SELECT coalesce(sum((a->>'agent_tokens')::bigint),0),coalesce(sum((a->>'duration_ms')::bigint),0),coalesce(sum((a->>'test_tokens')::bigint),0),coalesce(sum((a->>'test_duration_ms')::bigint),0) FROM repair_incidents i CROSS JOIN LATERAL jsonb_array_elements(i.evidence->'attempts') a WHERE i.created_at>=now()-interval '24 hours'`).Scan(&agentTokens, &diagnosisMS, &testTokens, &testMS); err != nil {
		writeError(w, 503, "repair_metrics_unavailable", "Diagnosis metrics are unavailable.")
		return
	}
	var dailyUsed int64
	if s.redis != nil {
		dailyUsed, _ = s.redis.Get(r.Context(), "repair:tokens:"+time.Now().UTC().Format("2006-01-02")).Int64()
	}
	writeJSON(w, 200, map[string]any{"window": "24h", "requests": requests, "successful_requests": successes, "incidents": incidents, "recovered_incidents": recovered, "agent_tokens": agentTokens, "diagnosis_ms": diagnosisMS, "daily_budget_used": dailyUsed, "test_tokens": testTokens, "test_ms": testMS})
}
