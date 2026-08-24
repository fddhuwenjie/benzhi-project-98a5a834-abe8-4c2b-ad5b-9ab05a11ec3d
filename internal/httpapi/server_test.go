package httpapi

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func call(t *testing.T, h http.Handler, method, path string, v any, headers map[string]string) map[string]any {
	t.Helper()
	b, _ := json.Marshal(v)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	for k, x := range headers {
		r.Header.Set(k, x)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code >= 300 {
		t.Fatalf("%s %s => %d: %s", method, path, w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out
}
func TestConservationWorkflow(t *testing.T) {
	st, _ := storage.New("")
	h := New(st).Handler()
	call(t, h, "POST", "/v1/artifacts", map[string]any{"artifact_id": "a1", "title": "古籍", "material": "paper", "location": "库房"}, nil)
	start := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	end := time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339)
	task := call(t, h, "POST", "/v1/conservation-tasks", map[string]any{"artifact_id": "a1", "owner_id": "u1", "window_start": start, "window_end": end}, map[string]string{"Idempotency-Key": "task-1"})
	tid := task["task_id"].(string)
	call(t, h, "POST", "/v1/inspections/"+tid, map[string]any{"inspector_id": "i1", "temperature": 22, "humidity": 70, "illuminance": 200, "observations": "发现霉斑", "evidence_refs": []string{"photo://1"}}, map[string]string{"Idempotency-Key": "insp-1"})
	act := call(t, h, "POST", "/v1/actions/"+tid, map[string]any{"assignee_id": "u2"}, map[string]string{"Idempotency-Key": "act-1"})
	aid := act["recommendation_id"].(string)
	call(t, h, "PATCH", "/v1/actions/"+aid+"/review", map[string]any{"result_text": "已完成隔离", "reviewer_id": "r1", "evidence_complete": true}, nil)
	out := call(t, h, "PATCH", "/v1/conservation-tasks/"+tid+"/close", map[string]any{"revision": 4}, nil)
	if out["task_id"] != tid {
		t.Fatalf("unexpected audit %#v", out)
	}
}
