package legacy_evidence_loss_test

import (
	"heritage-care/internal/domain"
	"heritage-care/internal/inspection"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"testing"
	"time"
)

func TestLegacyMeasurementEvidenceSurvivesNormalization(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2032, 5, 6, 9, 0, 0, 0, time.UTC)
	if err := store.AddArtifact(domain.ArtifactRecord{ArtifactID: "artifact-evidence", Title: "纸本文物", Material: "paper"}); err != nil {
		t.Fatal(err)
	}
	tasks := &task.Service{Store: store, Now: func() time.Time { return base }}
	created, _, err := tasks.Create(task.CreateInput{
		ArtifactID: "artifact-evidence", OwnerID: "owner-evidence",
		WindowStart:    base.Add(time.Hour).Format(time.RFC3339),
		WindowEnd:      base.Add(2 * time.Hour).Format(time.RFC3339),
		IdempotencyKey: "task-evidence-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	humidity := 70.0
	service := &inspection.Service{Store: store, Tasks: tasks}
	entry, _, err := service.Record(created.TaskID, inspection.Input{
		InspectorID: "inspector-evidence", Humidity: &humidity,
		Observations: "例行检查未见外观问题", EvidenceRefs: []string{"evidence://humidity-reading"},
		IdempotencyKey: "inspection-evidence-key",
	})
	if err != nil {
		t.Fatalf("旧版巡检请求被意外拒绝: %v", err)
	}
	if len(entry.EvidenceRefs) != 1 || entry.EvidenceRefs[0] != "evidence://humidity-reading" {
		t.Fatalf("测量异常证据在归一化后丢失: entry=%#v", entry)
	}
	for _, result := range entry.Results {
		if result.Code == "environment" && len(result.EvidenceRefs) == 0 {
			t.Fatalf("环境异常结果未关联已提交证据: result=%#v", result)
		}
	}
}
