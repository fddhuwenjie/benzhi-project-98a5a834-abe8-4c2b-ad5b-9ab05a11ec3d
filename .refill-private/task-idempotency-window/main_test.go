package task_idempotency_window_test

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func post(t *testing.T, h http.Handler, path, key string, body any) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("响应不是JSON: %s", rec.Body.String())
	}
	return rec.Code, value
}

func TestTaskIdempotencyReplayAfterWindowStarts(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(store)
	now := time.Date(2031, 3, 4, 10, 0, 0, 0, time.UTC)
	server.Tasks.Now = func() time.Time { return now }
	handler := server.Handler()

	status, _ := post(t, handler, "/v1/artifacts", "", map[string]any{
		"artifact_id": "artifact-replay", "title": "测试文物", "material": "paper",
	})
	if status != http.StatusCreated {
		t.Fatalf("创建文物失败: status=%d", status)
	}
	body := map[string]any{
		"artifact_id": "artifact-replay", "owner_id": "owner-replay",
		"window_start": now.Add(time.Hour).Format(time.RFC3339),
		"window_end":   now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	status, first := post(t, handler, "/v1/conservation-tasks", "task-replay-key", body)
	if status != http.StatusCreated {
		t.Fatalf("首次创建任务失败: status=%d body=%#v", status, first)
	}

	now = now.Add(90 * time.Minute)
	status, replay := post(t, handler, "/v1/conservation-tasks", "task-replay-key", body)
	if status != http.StatusOK || replay["task_id"] != first["task_id"] {
		t.Fatalf("窗口开始后的同请求幂等重放未返回原任务: status=%d body=%#v", status, replay)
	}
}
