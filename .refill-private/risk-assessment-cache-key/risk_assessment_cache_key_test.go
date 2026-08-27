package riskassessmentcachekey

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/domain"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func call(t *testing.T, handler http.Handler, method, path string, body any, idempotencyKey string) map[string]any {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code >= http.StatusMultipleChoices {
		t.Fatalf("%s %s returned %d: %s", method, path, rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func addArtifact(t *testing.T, handler http.Handler, id string) {
	t.Helper()
	call(t, handler, http.MethodPost, "/v1/artifacts", map[string]any{
		"artifact_id":         id,
		"title":               "同类纸本文物",
		"material":            "paper",
		"location":            "一号库房",
		"sensitivity_profile": "normal",
	}, "")
}

func TestRiskAssessmentCacheSeparatesRequestInputs(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(store).Handler()
	addArtifact(t, handler, "cache-low")
	addArtifact(t, handler, "cache-high")

	start := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	end := start.Add(2 * time.Hour)
	call(t, handler, http.MethodPost, "/v1/conservation-tasks", map[string]any{
		"artifact_id":  "cache-low",
		"owner_id":     "owner-low",
		"window_start": start.Format(time.RFC3339),
		"window_end":   end.Format(time.RFC3339),
		"humidity":     50,
	}, "risk-cache-low")
	high := call(t, handler, http.MethodPost, "/v1/conservation-tasks", map[string]any{
		"artifact_id":       "cache-high",
		"owner_id":          "owner-high",
		"window_start":      start.Format(time.RFC3339),
		"window_end":        end.Format(time.RFC3339),
		"humidity":          90,
		"historical_issues": []string{"mold", "mold", "mold", "mold", "mold"},
	}, "risk-cache-high")

	riskSnapshot := high["risk_snapshot"].(map[string]any)
	if riskSnapshot["level"] != string(domain.RiskHigh) {
		t.Fatalf("second task reused the first request's risk snapshot: %#v", riskSnapshot)
	}
	taskID := high["task_id"].(string)
	persisted, ok := store.GetTask(taskID)
	if !ok || persisted.RiskSnapshot.Level != domain.RiskHigh {
		t.Fatalf("high-risk request was persisted with a cached snapshot: %#v", persisted.RiskSnapshot)
	}
}
