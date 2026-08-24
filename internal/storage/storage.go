package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"heritage-care/internal/domain"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type IdempotencyRecord struct {
	EntityType  string `json:"entity_type"`
	EntityID    string `json:"entity_id"`
	Fingerprint string `json:"fingerprint"`
}

type snapshot struct {
	Artifacts          map[string]domain.ArtifactRecord       `json:"artifacts"`
	Tasks              map[string]domain.ConservationTask     `json:"tasks"`
	Inspections        map[string]domain.InspectionEntry      `json:"inspections"`
	Actions            map[string]domain.ActionRecommendation `json:"actions"`
	Idempotency        map[string]string                      `json:"idempotency,omitempty"`
	IdempotencyRecords map[string]IdempotencyRecord           `json:"idempotency_records"`
	Events             map[string][]domain.AuditEvent         `json:"events"`
	Audits             map[string]domain.AuditSummary         `json:"audits"`
}

type Store struct {
	mu   sync.RWMutex
	dir  string
	data snapshot
}

type WorkflowSnapshot struct {
	Tasks             []domain.ConservationTask
	ActiveInspections map[string]domain.InspectionEntry
	Actions           map[string][]domain.ActionRecommendation
}

func emptySnapshot() snapshot {
	return snapshot{
		Artifacts:          map[string]domain.ArtifactRecord{},
		Tasks:              map[string]domain.ConservationTask{},
		Inspections:        map[string]domain.InspectionEntry{},
		Actions:            map[string]domain.ActionRecommendation{},
		Idempotency:        map[string]string{},
		IdempotencyRecords: map[string]IdempotencyRecord{},
		Events:             map[string][]domain.AuditEvent{},
		Audits:             map[string]domain.AuditSummary{},
	}
}

func New(dir string) (*Store, error) {
	s := &Store{dir: dir, data: emptySnapshot()}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, snapshotFilename))
	if err == nil {
		if err = json.Unmarshal(b, &s.data); err != nil {
			return nil, errors.New("snapshot损坏")
		}
		s.initMaps()
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) initMaps() {
	if s.data.Artifacts == nil {
		s.data.Artifacts = map[string]domain.ArtifactRecord{}
	}
	if s.data.Tasks == nil {
		s.data.Tasks = map[string]domain.ConservationTask{}
	}
	if s.data.Inspections == nil {
		s.data.Inspections = map[string]domain.InspectionEntry{}
	}
	if s.data.Actions == nil {
		s.data.Actions = map[string]domain.ActionRecommendation{}
	}
	if s.data.Idempotency == nil {
		s.data.Idempotency = map[string]string{}
	}
	if s.data.IdempotencyRecords == nil {
		s.data.IdempotencyRecords = map[string]IdempotencyRecord{}
	}
	if s.data.Events == nil {
		s.data.Events = map[string][]domain.AuditEvent{}
	}
	if s.data.Audits == nil {
		s.data.Audits = map[string]domain.AuditSummary{}
	}
}

func (s *Store) persistLocked(event string) error {
	if s.dir == "" {
		return nil
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(s.dir, snapshotFilename+".tmp")
	if err = os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(s.dir, snapshotFilename)); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(s.dir, eventsFilename), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}
	defer f.Close()
	_, _ = f.WriteString(time.Now().UTC().Format(time.RFC3339) + " " + event + "\n")
	return nil
}

func (s *Store) cloneSnapshotLocked() snapshot {
	data, _ := json.Marshal(s.data)
	var cloned snapshot
	_ = json.Unmarshal(data, &cloned)
	return cloned
}

func (s *Store) commitLocked(event string, before snapshot) error {
	if err := s.persistLocked(event); err != nil {
		s.data = before
		return err
	}
	return nil
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Store) AddArtifact(a domain.ArtifactRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := s.cloneSnapshotLocked()
	s.data.Artifacts[a.ArtifactID] = a
	return s.commitLocked("artifact:"+a.ArtifactID, before)
}

func (s *Store) Artifact(id string) (domain.ArtifactRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data.Artifacts[id]
	return a, ok
}

func (s *Store) GetTask(id string) (domain.ConservationTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.data.Tasks[id]
	return t, ok
}

func (s *Store) Tasks() []domain.ConservationTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.ConservationTask, 0, len(s.data.Tasks))
	for _, t := range s.data.Tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].TaskID < out[j].TaskID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

func (s *Store) ReadWorkflowSnapshot() WorkflowSnapshot {
	s.mu.RLock()
	cloned := s.cloneSnapshotLocked()
	s.mu.RUnlock()
	view := WorkflowSnapshot{
		Tasks:             make([]domain.ConservationTask, 0, len(cloned.Tasks)),
		ActiveInspections: map[string]domain.InspectionEntry{},
		Actions:           map[string][]domain.ActionRecommendation{},
	}
	for _, task := range cloned.Tasks {
		view.Tasks = append(view.Tasks, task)
		if inspection, ok := cloned.Inspections[task.CurrentInspectionID]; ok {
			view.ActiveInspections[task.TaskID] = inspection
		}
	}
	for _, inspection := range cloned.Inspections {
		current, found := view.ActiveInspections[inspection.TaskID]
		if (inspection.Active || inspection.SupersededBy == "") && (!found || inspection.Version > current.Version) {
			view.ActiveInspections[inspection.TaskID] = inspection
		}
	}
	for _, action := range cloned.Actions {
		view.Actions[action.TaskID] = append(view.Actions[action.TaskID], action)
	}
	sort.Slice(view.Tasks, func(i, j int) bool {
		if view.Tasks[i].CreatedAt.Equal(view.Tasks[j].CreatedAt) {
			return view.Tasks[i].TaskID < view.Tasks[j].TaskID
		}
		return view.Tasks[i].CreatedAt.Before(view.Tasks[j].CreatedAt)
	})
	for taskID := range view.Actions {
		sort.Slice(view.Actions[taskID], func(i, j int) bool {
			left, right := view.Actions[taskID][i], view.Actions[taskID][j]
			if left.DueAt.Equal(right.DueAt) {
				return left.RecommendationID < right.RecommendationID
			}
			return left.DueAt.Before(right.DueAt)
		})
	}
	return view
}

func (s *Store) Inspections(taskID string) []domain.InspectionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.inspectionsLocked(taskID)
}

func (s *Store) inspectionsLocked(taskID string) []domain.InspectionEntry {
	out := []domain.InspectionEntry{}
	for _, i := range s.data.Inspections {
		if i.TaskID == taskID {
			out = append(out, i)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Version == out[j].Version {
			return out[i].InspectionID < out[j].InspectionID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

func (s *Store) Inspection(id string) (domain.InspectionEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	i, ok := s.data.Inspections[id]
	return i, ok
}

func (s *Store) ActiveInspection(taskID string) (domain.InspectionEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeInspectionLocked(taskID)
}

func (s *Store) activeInspectionLocked(taskID string) (domain.InspectionEntry, bool) {
	t, ok := s.data.Tasks[taskID]
	if ok && t.CurrentInspectionID != "" {
		i, found := s.data.Inspections[t.CurrentInspectionID]
		return i, found
	}
	var latest domain.InspectionEntry
	found := false
	for _, i := range s.data.Inspections {
		if i.TaskID == taskID && (i.Active || i.SupersededBy == "") && (!found || i.Version > latest.Version) {
			latest, found = i, true
		}
	}
	return latest, found
}

func (s *Store) GetAction(id string) (domain.ActionRecommendation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data.Actions[id]
	return a, ok
}

func (s *Store) Actions(taskID string) []domain.ActionRecommendation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.actionsLocked(taskID)
}

func (s *Store) actionsLocked(taskID string) []domain.ActionRecommendation {
	out := []domain.ActionRecommendation{}
	for _, a := range s.data.Actions {
		if a.TaskID == taskID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DueAt.Equal(out[j].DueAt) {
			return out[i].RecommendationID < out[j].RecommendationID
		}
		return out[i].DueAt.Before(out[j].DueAt)
	})
	return out
}

func (s *Store) Audit(id string) (domain.AuditSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.data.Audits[id]
	return a, ok
}

func (s *Store) Events(taskID string) []domain.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]domain.AuditEvent(nil), s.data.Events[taskID]...)
}

func eventDigest(e domain.AuditEvent) string {
	e.Digest = ""
	return Digest(e)
}

func (s *Store) appendEventLocked(taskID, eventType, actorID, objectType, objectID string, revision int, data map[string]any, at time.Time) domain.AuditEvent {
	events := s.data.Events[taskID]
	prev := ""
	if len(events) > 0 {
		prev = events[len(events)-1].Digest
	}
	e := domain.AuditEvent{
		Sequence: len(events) + 1, EventType: eventType, OccurredAt: at.UTC(),
		ActorID: actorID, ObjectType: objectType, ObjectID: objectID,
		Revision: revision, Data: data, PreviousDigest: prev,
	}
	e.Digest = eventDigest(e)
	s.data.Events[taskID] = append(events, e)
	return e
}

func idemConflict() error {
	return domain.NewError("idempotency_conflict", "相同Idempotency-Key对应的请求内容不同", "Idempotency-Key")
}

func (s *Store) lookupIdempotencyLocked(key, fingerprint, entityType string) (string, bool, error) {
	if key == "" {
		return "", false, domain.NewError("validation_error", "缺少Idempotency-Key", "Idempotency-Key")
	}
	if rec, ok := s.data.IdempotencyRecords[key]; ok {
		if rec.Fingerprint != fingerprint || rec.EntityType != entityType {
			return "", false, idemConflict()
		}
		return rec.EntityID, true, nil
	}
	if id := s.data.Idempotency[key]; id != "" {
		return "", false, idemConflict()
	}
	return "", false, nil
}

func (s *Store) saveIdempotencyLocked(key, fingerprint, entityType, entityID string) {
	s.data.Idempotency[key] = entityID
	s.data.IdempotencyRecords[key] = IdempotencyRecord{EntityType: entityType, EntityID: entityID, Fingerprint: fingerprint}
}

func (s *Store) UpdateTask(t domain.ConservationTask, expected int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.data.Tasks[t.TaskID]
	if !ok {
		return errors.New("任务不存在")
	}
	if cur.Revision != expected {
		return &domain.BusinessError{Code: "revision_conflict", Message: fmt.Sprintf("revision冲突，当前revision为%d", cur.Revision), Field: "revision", Details: map[string]any{"current_revision": cur.Revision, "current_status": cur.Status}}
	}
	before := s.cloneSnapshotLocked()
	s.data.Tasks[t.TaskID] = t
	return s.commitLocked("task:update:"+t.TaskID, before)
}
