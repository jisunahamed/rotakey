package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIntegrationGlobalCaptureOnAndOff(t *testing.T) {
	s := repairIntegrationServer(t)
	for _, enabled := range []bool{true, false} {
		_, err := s.db.Exec(context.Background(), `UPDATE app_settings SET capture_all_requests=$1`, enabled)
		if err != nil {
			t.Fatal(err)
		}
		requestID, _ := newID("capture_test")
		body := `{"model":"capture-model","messages":[{"role":"user","content":"hello"}]}`
		response := strings.Repeat("streamed data\n", 100000)
		handler := s.captureRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got, _ := io.ReadAll(r.Body)
			if string(got) != body {
				t.Fatal("request changed")
			}
			_, _ = io.WriteString(w, response)
			s.storeRequestLog(r.Context(), logInput{RequestID: requestID, Started: time.Now(), StatusCode: 200, Route: routeRuntime{Model: ModelRoute{PublicAlias: "capture-model"}}})
		}))
		req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Body.String() != response {
			t.Fatal("public bytes changed")
		}
		var input, output []byte
		var truncated bool
		if err := s.db.QueryRow(context.Background(), `SELECT request_body_cipher,response_body_cipher,body_truncated FROM request_logs WHERE request_id=$1`, requestID).Scan(&input, &output, &truncated); err != nil {
			t.Fatal(err)
		}
		if !enabled {
			if input != nil || output != nil {
				t.Fatal("off still captures bodies")
			}
			continue
		}
		plain, err := s.vault.Decrypt(input)
		if err != nil || string(plain) != body {
			t.Fatal("missing input")
		}
		plain, err = s.vault.Decrypt(output)
		if err != nil || string(plain) != response || truncated {
			t.Fatal("full output truncated")
		}
	}
}

func TestIntegrationArchiveVerifiedMoveAndFailureSafety(t *testing.T) {
	s := repairIntegrationServer(t)
	ctx := context.Background()
	// This schema exists only on disposable TEST_DATABASE_URL services.
	if _, err := s.db.Exec(ctx, `DROP SCHEMA IF EXISTS rotakey_archive CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = s.db.Exec(ctx, `DROP SCHEMA IF EXISTS rotakey_archive CASCADE`) })
	input := "বাংলা request"
	output := "full streamed response"
	inputCipher, _ := s.vault.Encrypt([]byte(input))
	outputCipher, _ := s.vault.Encrypt([]byte(output))
	insert := func(id string, cipher []byte) {
		_, err := s.db.Exec(ctx, `INSERT INTO request_logs(id,request_id,model_alias,provider_name,credential_label,endpoint,status_code,latency_ms,request_body_cipher,response_body_cipher) VALUES($1,$1,'model','provider','','chat',200,12,$2,$3)`, id, cipher, outputCipher)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("valid", inputCipher)
	result, err := archiveWithPools(ctx, s.db, s.db, "isolated-test", s.vault, nil)
	if err != nil || result.Copied != 1 || result.Deleted != 1 {
		t.Fatalf("move failed: %+v %v", result, err)
	}
	var a, b string
	if err = s.db.QueryRow(ctx, `SELECT request_body,response_body FROM rotakey_archive.requests WHERE request_id='valid'`).Scan(&a, &b); err != nil || a != input || b != output {
		t.Fatal("remote input/output changed", err)
	}
	result, err = archiveWithPools(ctx, s.db, s.db, "isolated-test", s.vault, nil)
	if err != nil || result.Copied != 0 {
		t.Fatal("rerun is not idempotent")
	}
	insert("invalid", []byte("bad encrypted body"))
	_, err = archiveWithPools(ctx, s.db, s.db, "isolated-test", s.vault, nil)
	if err == nil {
		t.Fatal("unreadable body not rejected")
	}
	var retained int
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE id='invalid'`).Scan(&retained)
	if retained != 1 {
		t.Fatal("failed decrypt deleted local data")
	}
	_, _ = s.db.Exec(ctx, `DELETE FROM request_logs WHERE id='invalid'`)
	// A row changed after copying must remain local instead of losing the new
	// information. Use a staging insert trigger on this disposable database.
	var schema string
	_ = s.db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema)
	_, err = s.db.Exec(ctx, `CREATE FUNCTION rotakey_archive.change_source() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN UPDATE `+schema+`.request_logs SET output_tokens=7 WHERE id='changed'; RETURN NEW; END$$;
CREATE TRIGGER change_source BEFORE INSERT ON rotakey_archive.requests FOR EACH ROW EXECUTE FUNCTION rotakey_archive.change_source()`)
	if err != nil {
		t.Fatal(err)
	}
	insert("changed", inputCipher)
	result, err = archiveWithPools(ctx, s.db, s.db, "isolated-test", s.vault, nil)
	if err != nil || result.Copied != 1 || result.Deleted != 0 {
		t.Fatalf("concurrent update lost: %+v %v", result, err)
	}
	_, _ = s.db.Exec(ctx, `DROP TRIGGER change_source ON rotakey_archive.requests;DELETE FROM request_logs WHERE id='changed'`)
	insert("remote-failure", inputCipher)
	_, _ = s.db.Exec(ctx, `ALTER TABLE rotakey_archive.requests ADD CONSTRAINT reject_test CHECK(request_id<>'remote-failure')`)
	_, err = archiveWithPools(ctx, s.db, s.db, "isolated-test", s.vault, nil)
	if err == nil {
		t.Fatal("remote rejected row should fail")
	}
	_ = s.db.QueryRow(ctx, `SELECT count(*) FROM request_logs WHERE id='remote-failure'`).Scan(&retained)
	if retained != 1 {
		t.Fatal("failed remote copy deleted source")
	}
}

func TestCaptureSettingsOptionalField(t *testing.T) {
	var input settingsUpdateInput
	if err := json.Unmarshal([]byte(`{"capture_all_requests":true}`), &input); err != nil || input.CaptureAllRequests == nil || !*input.CaptureAllRequests {
		t.Fatal("settings switch did not decode")
	}
	if err := json.Unmarshal([]byte(`{}`), &settingsUpdateInput{}); err != nil {
		t.Fatal(err)
	}
}
