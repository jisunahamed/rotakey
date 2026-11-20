package app

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestCapabilityIntersection(t *testing.T) {
	a := routeRuntime{Model: ModelRoute{SupportsChat: true, CapabilityProfile: map[string]string{"images": "catalog_supported", "context_window": "1000000", "tools": "native_unverified", "reasoning_levels": "low,high"}}}
	b := a
	b.Model.CapabilityProfile = map[string]string{"images": "unsupported", "context_window": "200000", "reasoning_levels": "high"}
	out := capabilityEnvelope("alias", []routeRuntime{a, b})
	if out["features"].(map[string]string)["vision_input"] != "unsupported" || out["limits"].(map[string]any)["context_window"] != int64(200000) || len(out["reasoning_effort_levels"].([]string)) != 1 {
		t.Fatal(out)
	}
	if capabilityState("native_unverified") != "unknown" || positiveCapability("invalid") != nil {
		t.Fatal("invented facts")
	}
}
func TestUsageDoesNotClaimEstimateIsMeasured(t *testing.T) {
	out := publicUsage(RequestLog{RequestID: "req_test", State: "completed", InputTokens: 12, LatencyMS: 50, QueueWaitMS: 20}, false)
	usage := out["usage"].(map[string]any)
	if usage["accuracy"] != "not_verified" || usage["total_tokens"] != int64(12) || out["timing"].(map[string]any)["processing_ms"] != int64(30) {
		t.Fatal(out)
	}
}
func TestIntegrationPublicUsageRecord(t *testing.T) {
	s := repairIntegrationServer(t)
	id := "req_public_usage"
	_, err := s.db.Exec(context.Background(), `INSERT INTO request_logs(id,request_id,model_alias,provider_name,credential_label,endpoint,status_code,latency_ms,input_tokens,output_tokens) VALUES('public_usage_test',$1,'alias','provider','hidden','chat',200,100,10,5)`, id)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/v1/requests/"+id+"/usage", nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	s.handleRequestUsage(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
