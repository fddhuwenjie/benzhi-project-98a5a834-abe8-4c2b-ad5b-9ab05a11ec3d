package duplicate_measurement_state_test

import (
	"heritage-care/internal/domain"
	"heritage-care/internal/inspection"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"testing"
	"time"
)

func float64Pointer(value float64) *float64 { return &value }

func TestDuplicateMetricCannotPersistContradictoryInspection(t *testing.T) {
	store, err := storage.New("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2033, 7, 8, 9, 0, 0, 0, time.UTC)
	if err := store.AddArtifact(domain.ArtifactRecord{ArtifactID: "artifact-metric", Title: "纸本文物", Material: "paper"}); err != nil {
		t.Fatal(err)
	}
	tasks := &task.Service{Store: store, Now: func() time.Time { return base }}
	created, _, err := tasks.Create(task.CreateInput{
		ArtifactID: "artifact-metric", OwnerID: "owner-metric",
		WindowStart:    base.Add(time.Hour).Format(time.RFC3339),
		WindowEnd:      base.Add(2 * time.Hour).Format(time.RFC3339),
		IdempotencyKey: "task-metric-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	items := make([]inspection.ItemInput, 0, len(created.Checklist))
	for _, checklist := range created.Checklist {
		item := inspection.ItemInput{Code: checklist.Code, Conclusion: "normal", Observation: "逐项检查完成"}
		if checklist.Code == "environment" {
			item.Measurements = []inspection.MeasurementInput{
				{Type: "humidity", Value: float64Pointer(70), Unit: "percent_rh"},
				{Type: "humidity", Value: float64Pointer(50), Unit: "percent_rh"},
			}
			item.EvidenceRefs = []string{"evidence://first-humidity-reading"}
		}
		items = append(items, item)
	}
	service := &inspection.Service{Store: store, Tasks: tasks}
	entry, _, err := service.Record(created.TaskID, inspection.Input{
		InspectorID: "inspector-metric", ChecklistResults: items, IdempotencyKey: "inspection-metric-key",
	})
	if err == nil {
		t.Fatalf("重复湿度值被持久化为互相矛盾的巡检状态: humidity=%v anomalies=%v", entry.Humidity, entry.Anomalies)
	}
	if len(store.Inspections(created.TaskID)) != 0 {
		t.Fatalf("被拒绝的重复测量留下了巡检状态")
	}
}
