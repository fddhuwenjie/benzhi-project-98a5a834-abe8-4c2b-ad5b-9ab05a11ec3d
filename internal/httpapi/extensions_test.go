package httpapi

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/domain"
	"heritage-care/internal/storage"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func request(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) (int, map[string]any) {
	t.Helper()
	data, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(data))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func mustRequest(t *testing.T, h http.Handler, method, path string, body any, headers map[string]string) map[string]any {
	t.Helper()
	status, out := request(t, h, method, path, body, headers)
	if status >= 300 {
		t.Fatalf("%s %s => %d: %#v", method, path, status, out)
	}
	return out
}

func addArtifact(t *testing.T, h http.Handler, id, material, sensitivity string) {
	t.Helper()
	mustRequest(t, h, http.MethodPost, "/v1/artifacts", map[string]any{
		"artifact_id": id, "title": "测试文物" + id, "material": material,
		"location": "一号库房", "sensitivity_profile": sensitivity,
	}, nil)
}

func futureWindow(offset time.Duration) (string, string) {
	start := time.Now().UTC().Add(offset).Truncate(time.Second)
	return start.Format(time.RFC3339), start.Add(2 * time.Hour).Format(time.RFC3339)
}

func TestTaskScheduleConflictAndIdempotency(t *testing.T) {
	store, _ := storage.New("")
	h := New(store).Handler()
	addArtifact(t, h, "schedule-a", "paper", "high")
	start, end := futureWindow(2 * time.Hour)
	body := map[string]any{"artifact_id": "schedule-a", "owner_id": "owner-a", "window_start": start, "window_end": end, "humidity": 65}
	first := mustRequest(t, h, http.MethodPost, "/v1/conservation-tasks", body, map[string]string{"Idempotency-Key": "schedule-idem"})
	second := mustRequest(t, h, http.MethodPost, "/v1/conservation-tasks", body, map[string]string{"Idempotency-Key": "schedule-idem"})
	if first["task_id"] != second["task_id"] {
		t.Fatalf("相同请求未返回原任务: %#v %#v", first, second)
	}

	changed := map[string]any{"artifact_id": "schedule-a", "owner_id": "owner-a", "window_start": start, "window_end": time.Now().UTC().Add(5 * time.Hour).Format(time.RFC3339), "humidity": 65}
	status, out := request(t, h, http.MethodPost, "/v1/conservation-tasks", changed, map[string]string{"Idempotency-Key": "schedule-idem"})
	if status != http.StatusConflict || out["code"] != "idempotency_conflict" {
		t.Fatalf("预期幂等冲突，得到 %d %#v", status, out)
	}

	overlapStart := time.Now().UTC().Add(3 * time.Hour).Format(time.RFC3339)
	overlapEnd := time.Now().UTC().Add(5 * time.Hour).Format(time.RFC3339)
	status, out = request(t, h, http.MethodPost, "/v1/conservation-tasks", map[string]any{
		"artifact_id": "schedule-a", "owner_id": "owner-b", "window_start": overlapStart, "window_end": overlapEnd,
	}, map[string]string{"Idempotency-Key": "schedule-overlap"})
	if status != http.StatusConflict || out["conflict_task_id"] != first["task_id"] {
		t.Fatalf("预期排期冲突及原任务标识，得到 %d %#v", status, out)
	}
}

func checklistResults(task map[string]any, humidity float64, appearanceAbnormal bool) []map[string]any {
	checklist := task["checklist"].([]any)
	results := make([]map[string]any, 0, len(checklist))
	for _, raw := range checklist {
		code := raw.(map[string]any)["code"].(string)
		item := map[string]any{"code": code, "conclusion": "normal", "observation": "已逐项检查，未见异常"}
		if code == "environment" {
			item["measurements"] = []map[string]any{{"type": "humidity", "value": humidity, "unit": "percent_rh"}}
			if humidity > 60 {
				item["evidence_refs"] = []string{"evidence://humidity"}
			}
		}
		if code == "appearance" && appearanceAbnormal {
			item["conclusion"] = "abnormal"
			item["observation"] = "发现裂隙"
			item["evidence_refs"] = []string{"evidence://appearance"}
		}
		results = append(results, item)
	}
	return results
}

func TestInspectionCoverageAndCorrection(t *testing.T) {
	store, _ := storage.New("")
	h := New(store).Handler()
	addArtifact(t, h, "inspect-a", "paper", "high")
	start, end := futureWindow(2 * time.Hour)
	taskValue := mustRequest(t, h, http.MethodPost, "/v1/conservation-tasks", map[string]any{
		"artifact_id": "inspect-a", "owner_id": "owner-i", "window_start": start, "window_end": end,
		"temperature": 0, "humidity": 65, "illuminance": 500, "historical_issues": []string{"mold", "mold", "mold"},
	}, map[string]string{"Idempotency-Key": "inspect-task"})
	taskID := taskValue["task_id"].(string)
	all := checklistResults(taskValue, 75, true)
	status, out := request(t, h, http.MethodPost, "/v1/inspections/"+taskID, map[string]any{
		"inspector_id": "inspector-a", "checklist_results": all[:len(all)-1],
	}, map[string]string{"Idempotency-Key": "inspect-missing"})
	if status != http.StatusBadRequest || out["code"] != "checklist_incomplete" {
		t.Fatalf("预期清单缺项错误，得到 %d %#v", status, out)
	}
	unchanged, _ := store.GetTask(taskID)
	if unchanged.Revision != 1 || len(store.Inspections(taskID)) != 0 {
		t.Fatalf("失败巡检产生了半成品: task=%#v inspections=%#v", unchanged, store.Inspections(taskID))
	}

	first := mustRequest(t, h, http.MethodPost, "/v1/inspections/"+taskID, map[string]any{
		"inspector_id": "inspector-a", "checklist_results": all,
	}, map[string]string{"Idempotency-Key": "inspect-first"})
	repeated := mustRequest(t, h, http.MethodPost, "/v1/inspections/"+taskID, map[string]any{
		"inspector_id": "inspector-a", "checklist_results": all,
	}, map[string]string{"Idempotency-Key": "inspect-first"})
	if repeated["inspection_id"] != first["inspection_id"] {
		t.Fatalf("重复巡检请求未返回原版本: %#v", repeated)
	}
	correctedItems := checklistResults(taskValue, 55, false)
	second := mustRequest(t, h, http.MethodPost, "/v1/inspections/"+taskID, map[string]any{
		"inspector_id": "inspector-a", "checklist_results": correctedItems,
		"supersedes_inspection_id": first["inspection_id"], "correction_reason": "湿度录入错误", "revision": 2,
	}, map[string]string{"Idempotency-Key": "inspect-correction"})
	if second["supersedes_inspection_id"] != first["inspection_id"] {
		t.Fatalf("更正关系未保留: %#v", second)
	}
	history := store.Inspections(taskID)
	if len(history) != 2 || history[0].Active || !history[1].Active || history[0].SupersededBy != history[1].InspectionID {
		t.Fatalf("巡检版本链错误: %#v", history)
	}
}

func TestActionRejectionResubmissionAndAudit(t *testing.T) {
	store, _ := storage.New("")
	h := New(store).Handler()
	addArtifact(t, h, "action-a", "metal", "normal")
	start, end := futureWindow(3 * time.Hour)
	taskValue := mustRequest(t, h, http.MethodPost, "/v1/conservation-tasks", map[string]any{
		"artifact_id": "action-a", "owner_id": "owner-action", "window_start": start, "window_end": end,
	}, map[string]string{"Idempotency-Key": "action-task"})
	taskID := taskValue["task_id"].(string)
	inspectionValue := mustRequest(t, h, http.MethodPost, "/v1/inspections/"+taskID, map[string]any{
		"inspector_id": "inspector-action", "checklist_results": checklistResults(taskValue, 75, true),
	}, map[string]string{"Idempotency-Key": "action-inspection"})
	anomalies := inspectionValue["anomalies"].([]any)
	if len(anomalies) != 2 {
		t.Fatalf("预期两个异常，得到 %#v", anomalies)
	}
	actions := map[string]map[string]any{}
	for idx, rawCode := range anomalies {
		code := rawCode.(string)
		value := mustRequest(t, h, http.MethodPost, "/v1/actions/"+taskID, map[string]any{
			"anomaly_code": code, "assignee_id": "assignee-" + code,
		}, map[string]string{"Idempotency-Key": "action-create-" + code})
		repeated := mustRequest(t, h, http.MethodPost, "/v1/actions/"+taskID, map[string]any{
			"anomaly_code": code, "assignee_id": "assignee-" + code,
		}, map[string]string{"Idempotency-Key": "action-create-" + code})
		if repeated["recommendation_id"] != value["recommendation_id"] {
			t.Fatalf("重复处置请求未返回原建议: %#v", repeated)
		}
		actions[code] = value
		if idx == 0 {
			detail := mustRequest(t, h, http.MethodGet, "/v1/conservation-tasks/"+taskID, nil, nil)
			progress := detail["progress"].(map[string]any)
			if progress["action_coverage"] != float64(1) || len(progress["uncovered_anomalies"].([]any)) != 1 {
				t.Fatalf("首项处置后的进度错误: %#v", progress)
			}
		}
	}

	firstCode := anomalies[0].(string)
	firstAction := actions[firstCode]
	firstID := firstAction["recommendation_id"].(string)
	taskState, _ := store.GetTask(taskID)
	submitted := mustRequest(t, h, http.MethodPost, "/v1/actions/"+firstID, map[string]any{
		"submitter_id": "assignee-" + firstCode, "result_text": "已完成首次处置",
		"evidence_refs": []string{"evidence://before-review"}, "revision": taskState.Revision, "action_revision": 1,
	}, map[string]string{"Idempotency-Key": "submit-first"})
	taskState, _ = store.GetTask(taskID)
	rejected := mustRequest(t, h, http.MethodPatch, "/v1/actions/"+firstID+"/review", map[string]any{
		"decision": "rejected", "reviewer_id": "reviewer-a", "comment": "缺少整改后照片",
		"missing_evidence_items": []string{"整改后照片"}, "evidence_complete": false,
		"revision": taskState.Revision, "action_revision": submitted["revision"],
	}, nil)
	taskState, _ = store.GetTask(taskID)
	resubmitted := mustRequest(t, h, http.MethodPost, "/v1/actions/"+firstID, map[string]any{
		"submitter_id": "assignee-" + firstCode, "result_text": "已补充整改后照片",
		"evidence_refs": []string{"evidence://after-remediation"}, "revision": taskState.Revision, "action_revision": rejected["revision"],
	}, map[string]string{"Idempotency-Key": "submit-second"})
	taskState, _ = store.GetTask(taskID)
	approved := mustRequest(t, h, http.MethodPatch, "/v1/actions/"+firstID+"/review", map[string]any{
		"decision": "approved", "reviewer_id": "reviewer-a", "evidence_complete": true,
		"revision": taskState.Revision, "action_revision": resubmitted["revision"],
	}, nil)
	if len(approved["submissions"].([]any)) != 2 || len(approved["reviews"].([]any)) != 2 {
		t.Fatalf("驳回重提历史不完整: %#v", approved)
	}

	secondCode := anomalies[1].(string)
	secondID := actions[secondCode]["recommendation_id"].(string)
	taskState, _ = store.GetTask(taskID)
	secondSubmitted := mustRequest(t, h, http.MethodPost, "/v1/actions/"+secondID, map[string]any{
		"submitter_id": "assignee-" + secondCode, "result_text": "已完成处置",
		"evidence_refs": []string{"evidence://second-action"}, "revision": taskState.Revision, "action_revision": 1,
	}, map[string]string{"Idempotency-Key": "submit-third"})
	taskState, _ = store.GetTask(taskID)
	mustRequest(t, h, http.MethodPatch, "/v1/actions/"+secondID+"/review", map[string]any{
		"decision": "approved", "reviewer_id": "reviewer-b", "evidence_complete": true,
		"revision": taskState.Revision, "action_revision": secondSubmitted["revision"],
	}, nil)
	taskState, _ = store.GetTask(taskID)
	if taskState.Status != domain.StatusReviewed {
		t.Fatalf("全部批准后状态错误: %#v", taskState)
	}
	mustRequest(t, h, http.MethodPatch, "/v1/conservation-tasks/"+taskID+"/close", map[string]any{
		"revision": taskState.Revision, "actor_id": "supervisor-a",
	}, nil)
	audit := mustRequest(t, h, http.MethodGet, "/v1/audits/"+taskID, nil, nil)
	if audit["verification_status"] != "passed" {
		t.Fatalf("审计校验未通过: %#v", audit)
	}
	events := audit["events"].([]any)
	foundRejected := false
	for _, event := range events {
		if event == "action_review_rejected" {
			foundRejected = true
		}
	}
	if !foundRejected {
		t.Fatalf("审计时间线缺少驳回事件: %#v", events)
	}
}
