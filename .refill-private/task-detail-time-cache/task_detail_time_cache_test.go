package task_detail_time_cache_test

import (
	"encoding/json"
	"heritage-care/internal/domain"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func taskScheduleStatus(t *testing.T, handler http.Handler, taskID string) string {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/v1/conservation-tasks/"+taskID, nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("任务详情返回状态码 %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Progress struct {
			ScheduleStatus string `json:"schedule_status"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("解析任务详情失败: %v", err)
	}
	return response.Progress.ScheduleStatus
}

func TestTaskDetailCacheExpiresAtWindowBoundary(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2032, 6, 10, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	seed := domain.ConservationTask{
		TaskID: "cache-window-task", ArtifactID: "artifact-cache", OwnerID: "owner-cache",
		WindowStart: start, WindowEnd: end, Status: domain.StatusPendingInspection,
		Revision: 1, CreatedAt: start.Add(-2 * time.Hour),
	}
	if _, _, err := store.CreateTaskAtomic(seed, "cache-window-idem", storage.Digest(seed)); err != nil {
		t.Fatalf("写入任务失败: %v", err)
	}

	server := httpapi.New(store)
	now := start.Add(-time.Hour)
	server.Tasks.Now = func() time.Time { return now }
	handler := server.Handler()
	if got := taskScheduleStatus(t, handler, seed.TaskID); got != "not_started" {
		t.Fatalf("窗口开始前状态错误: %s", got)
	}

	now = end.Add(time.Second)
	if got := taskScheduleStatus(t, handler, seed.TaskID); got != "overdue" {
		t.Fatalf("跨过窗口结束后缓存仍返回旧状态: %s", got)
	}
}
