package artifact_idempotency_overwrite_test

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createArtifact(t *testing.T, h http.Handler, key, title, material string) (int, map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"artifact_id": "artifact-stable", "title": title, "material": material,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(data))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var value map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("响应不是JSON: %s", rec.Body.String())
	}
	return rec.Code, value
}

func TestArtifactIdempotencyConflictDoesNotOverwriteArchive(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	handler := httpapi.New(store).Handler()
	status, first := createArtifact(t, handler, "artifact-create-key", "纸本文物", "paper")
	if status != http.StatusCreated {
		t.Fatalf("首次创建文物失败: status=%d body=%#v", status, first)
	}

	status, second := createArtifact(t, handler, "artifact-create-key", "被覆盖标题", "metal")
	stored, ok := store.Artifact("artifact-stable")
	if status != http.StatusConflict || second["code"] != "idempotency_conflict" || !ok || stored.Material != "paper" || stored.Title != "纸本文物" {
		t.Fatalf("冲突重放覆盖了文物档案: status=%d body=%#v stored=%#v", status, second, stored)
	}
}
