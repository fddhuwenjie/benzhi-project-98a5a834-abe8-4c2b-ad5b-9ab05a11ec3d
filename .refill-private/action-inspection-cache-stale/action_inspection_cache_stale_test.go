package actioninspectioncachestale

import (
	"bytes"
	"encoding/json"
	"heritage-care/internal/action"
	"heritage-care/internal/domain"
	"heritage-care/internal/httpapi"
	"heritage-care/internal/inspection"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func inspectionItems(checklist []domain.ChecklistItem, humidity float64, appearanceAbnormal bool, evidence string) []inspection.ItemInput {
	items := make([]inspection.ItemInput, 0, len(checklist))
	for _, checklistItem := range checklist {
		item := inspection.ItemInput{
			Code:        checklistItem.Code,
			Conclusion:  "normal",
			Observation: "现场检查完成，未见异常",
		}
		switch checklistItem.Code {
		case "environment":
			value := humidity
			item.Measurements = []inspection.MeasurementInput{{Type: "humidity", Value: &value, Unit: "percent_rh"}}
			if !appearanceAbnormal {
				item.EvidenceRefs = []string{evidence}
			}
		case "appearance":
			if appearanceAbnormal {
				item.Conclusion = "abnormal"
				item.Observation = "发现裂纹"
				item.EvidenceRefs = []string{evidence}
			}
		}
		items = append(items, item)
	}
	return items
}

func postAction(t *testing.T, handler http.Handler, taskID, idempotencyKey, anomalyCode string, revision int) *httptest.ResponseRecorder {
	t.Helper()
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(action.CreateInput{
		AnomalyCode: anomalyCode,
		AssigneeID:  "keeper-action",
		Revision:    revision,
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/actions/"+taskID, body)
	req.Header.Set("Idempotency-Key", idempotencyKey)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestActionUsesCorrectedInspectionAfterCacheWarmup(t *testing.T) {
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	server := httpapi.New(store)
	server.Tasks.Now = func() time.Time { return now }
	if err := store.AddArtifact(domain.ArtifactRecord{
		ArtifactID: "artifact-cache", Title: "缓存失效复现文物", Material: "paper",
		SensitivityProfile: "normal", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	created, _, err := server.Tasks.Create(task.CreateInput{
		ArtifactID: "artifact-cache", OwnerID: "keeper-owner",
		WindowStart:    now.Add(time.Hour).Format(time.RFC3339),
		WindowEnd:      now.Add(4 * time.Hour).Format(time.RFC3339),
		IdempotencyKey: "task-cache-seed",
	})
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := server.Inspections.Record(created.TaskID, inspection.Input{
		InspectorID: "inspector-one", Revision: created.Revision,
		IdempotencyKey:   "inspection-before-correction",
		ChecklistResults: inspectionItems(created.Checklist, 80, false, "evidence://humidity-before"),
	})
	if err != nil {
		t.Fatal(err)
	}

	priming := postAction(t, server.Handler(), created.TaskID, "invalid-action-primes-cache", "appearance", 2)
	if priming.Code != http.StatusBadRequest {
		t.Fatalf("预热请求应因旧巡检不含appearance而失败，得到 status=%d body=%s", priming.Code, priming.Body.String())
	}
	corrected, _, err := server.Inspections.Record(created.TaskID, inspection.Input{
		InspectorID: "inspector-two", Revision: 2,
		IdempotencyKey:         "inspection-correction",
		SupersedesInspectionID: first.InspectionID,
		CorrectionReason:       "复核照片后更正异常类型",
		ChecklistResults:       inspectionItems(created.Checklist, 50, true, "evidence://appearance-after"),
	})
	if err != nil {
		t.Fatal(err)
	}

	response := postAction(t, server.Handler(), created.TaskID, "action-after-correction", "appearance", 3)
	if response.Code != http.StatusCreated {
		t.Fatalf("更正后的当前巡检应允许appearance处置，得到 status=%d body=%s", response.Code, response.Body.String())
	}
	var got domain.ActionRecommendation
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.SourceInspectionID != corrected.InspectionID {
		t.Fatalf("处置仍引用旧巡检: got=%s want=%s", got.SourceInspectionID, corrected.InspectionID)
	}
}
