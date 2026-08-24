package task

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"heritage-care/internal/domain"
	"heritage-care/internal/risk"
	"heritage-care/internal/storage"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const MaxWindowDuration = 30 * 24 * time.Hour

type Service struct {
	Store         *storage.Store
	Now           func() time.Time
	detailCacheMu sync.Mutex
	detailCache   map[string]detailCacheEntry
}

type detailCacheEntry struct {
	revision int
	detail   Detail
}

type CreateInput struct {
	ArtifactID       string   `json:"artifact_id"`
	OwnerID          string   `json:"owner_id"`
	WindowStart      string   `json:"window_start"`
	WindowEnd        string   `json:"window_end"`
	IdempotencyKey   string   `json:"-"`
	Temperature      *float64 `json:"temperature"`
	Humidity         *float64 `json:"humidity"`
	Illuminance      *float64 `json:"illuminance"`
	HistoricalIssues []string `json:"historical_issues"`
}

type Detail struct {
	domain.ConservationTask
	Progress domain.TaskProgress `json:"progress"`
}

type ListFilter struct {
	OwnerID    string
	Status     domain.TaskStatus
	Overdue    *bool
	ArtifactID string
	Limit      int
	Cursor     string
}

type ListResult struct {
	Items      []domain.TaskProgress `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
	Count      int                   `json:"count"`
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func validation(message, field string) error {
	return domain.NewError("validation_error", message, field)
}

func (s *Service) Create(in CreateInput) (domain.ConservationTask, bool, error) {
	in.ArtifactID, in.OwnerID = strings.TrimSpace(in.ArtifactID), strings.TrimSpace(in.OwnerID)
	if in.ArtifactID == "" {
		return domain.ConservationTask{}, false, validation("artifact_id不能为空", "artifact_id")
	}
	if in.OwnerID == "" {
		return domain.ConservationTask{}, false, validation("owner_id不能为空", "owner_id")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return domain.ConservationTask{}, false, validation("缺少Idempotency-Key", "Idempotency-Key")
	}
	a, ok := s.Store.Artifact(in.ArtifactID)
	if !ok {
		return domain.ConservationTask{}, false, domain.NewError("not_found", "文物档案不存在", "artifact_id")
	}
	ws, err := time.Parse(time.RFC3339, in.WindowStart)
	if err != nil {
		return domain.ConservationTask{}, false, validation("window_start格式应为RFC3339", "window_start")
	}
	we, err := time.Parse(time.RFC3339, in.WindowEnd)
	if err != nil {
		return domain.ConservationTask{}, false, validation("window_end格式应为RFC3339", "window_end")
	}
	ws, we = ws.UTC(), we.UTC()
	now := s.now()
	if ws.Before(now) {
		return domain.ConservationTask{}, false, validation("window_start不得早于当前时间", "window_start")
	}
	if we.Equal(ws) {
		return domain.ConservationTask{}, false, validation("window_end不得与window_start相同", "window_end")
	}
	if we.Before(ws) {
		return domain.ConservationTask{}, false, validation("window_end必须晚于window_start", "window_end")
	}
	if we.Sub(ws) > MaxWindowDuration {
		return domain.ConservationTask{}, false, validation("计划窗口不得超过30天", "window_end")
	}

	rs := risk.Assess(risk.Input{
		Material: a.Material, Sensitivity: a.SensitivityProfile, Location: a.Location,
		Temperature: in.Temperature, Humidity: in.Humidity, Illuminance: in.Illuminance,
		HistoricalIssues: in.HistoricalIssues,
	})
	normalized := struct {
		ArtifactID       string   `json:"artifact_id"`
		OwnerID          string   `json:"owner_id"`
		WindowStart      string   `json:"window_start"`
		WindowEnd        string   `json:"window_end"`
		Temperature      *float64 `json:"temperature"`
		Humidity         *float64 `json:"humidity"`
		Illuminance      *float64 `json:"illuminance"`
		HistoricalIssues []string `json:"historical_issues"`
	}{in.ArtifactID, in.OwnerID, ws.Format(time.RFC3339Nano), we.Format(time.RFC3339Nano), in.Temperature, in.Humidity, in.Illuminance, in.HistoricalIssues}
	t := domain.ConservationTask{
		TaskID: newID("task"), ArtifactID: a.ArtifactID, OwnerID: in.OwnerID,
		WindowStart: ws, WindowEnd: we, Status: domain.StatusPendingInspection, Revision: 1,
		Checklist: risk.Checklist(rs.Level, a.Material, rs.Thresholds), RiskSnapshot: rs, CreatedAt: now,
	}
	stored, reused, err := s.Store.CreateTaskAtomic(t, in.IdempotencyKey, storage.Digest(normalized))
	return stored, reused, err
}

func (s *Service) Get(id string) (domain.ConservationTask, error) {
	t, ok := s.Store.GetTask(strings.TrimSpace(id))
	if !ok {
		return t, domain.NewError("not_found", "任务不存在", "task_id")
	}
	return t, nil
}

func (s *Service) Detail(id string) (Detail, error) {
	id = strings.TrimSpace(id)
	current, ok := s.Store.GetTask(id)
	if !ok {
		return Detail{}, domain.NewError("not_found", "任务不存在", "task_id")
	}
	s.detailCacheMu.Lock()
	cached, cachedOK := s.detailCache[id]
	s.detailCacheMu.Unlock()
	if cachedOK && cached.revision == current.Revision {
		return cached.detail, nil
	}
	view := s.Store.ReadWorkflowSnapshot()
	for _, t := range view.Tasks {
		if t.TaskID == id {
			inspection, hasInspection := view.ActiveInspections[t.TaskID]
			detail := Detail{ConservationTask: t, Progress: s.progress(t, s.now(), inspection, hasInspection, view.Actions[t.TaskID])}
			s.detailCacheMu.Lock()
			if s.detailCache == nil {
				s.detailCache = make(map[string]detailCacheEntry)
			}
			s.detailCache[id] = detailCacheEntry{revision: t.Revision, detail: detail}
			s.detailCacheMu.Unlock()
			return detail, nil
		}
	}
	return Detail{}, domain.NewError("not_found", "任务不存在", "task_id")
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) Progress(t domain.ConservationTask, now time.Time) domain.TaskProgress {
	inspection, hasInspection := s.Store.ActiveInspection(t.TaskID)
	return s.progress(t, now, inspection, hasInspection, s.Store.Actions(t.TaskID))
}

func (s *Service) progress(t domain.ConservationTask, now time.Time, inspection domain.InspectionEntry, hasInspection bool, actions []domain.ActionRecommendation) domain.TaskProgress {
	p := domain.TaskProgress{
		TaskID: t.TaskID, ArtifactID: t.ArtifactID, OwnerID: t.OwnerID, Status: t.Status, Revision: t.Revision,
		UncoveredAnomalies: []string{}, Todos: []domain.TodoItem{},
	}
	if t.Status != domain.StatusClosed {
		due := t.WindowEnd
		p.EarliestDueAt, p.EarliestAssigneeID = &due, t.OwnerID
	}
	if hasInspection {
		p.ChecklistCompletion = inspection.Summary.CoveragePercent
		p.AnomalyCount = len(inspection.Anomalies)
	} else {
		p.Todos = append(p.Todos, domain.TodoItem{Type: "pending_inspection", ObjectID: t.TaskID, AssigneeID: t.OwnerID, DueAt: t.WindowEnd})
	}
	for _, anomaly := range inspection.Anomalies {
		covered := false
		for _, a := range actions {
			if a.AnomalyCode == anomaly {
				covered = true
				break
			}
		}
		if !covered {
			p.UncoveredAnomalies = append(p.UncoveredAnomalies, anomaly)
			p.Todos = append(p.Todos, domain.TodoItem{Type: "anomaly_unassigned", ObjectID: anomaly, AssigneeID: t.OwnerID, DueAt: t.WindowEnd})
		}
	}
	sort.Strings(p.UncoveredAnomalies)
	for _, a := range actions {
		p.ActionCoverage++
		switch a.ReviewStatus {
		case "approved":
			p.ApprovedCount++
		case "pending_review":
			p.Todos = append(p.Todos, domain.TodoItem{Type: "review_pending", ObjectID: a.RecommendationID, DueAt: a.DueAt})
		case "rejected":
			p.Todos = append(p.Todos, domain.TodoItem{Type: "result_resubmission", ObjectID: a.RecommendationID, AssigneeID: a.AssigneeID, DueAt: a.DueAt})
			if len(a.Reviews) > 0 {
				p.EvidenceGapCount += len(a.Reviews[len(a.Reviews)-1].MissingEvidenceItems)
			}
		default:
			p.Todos = append(p.Todos, domain.TodoItem{Type: "result_pending", ObjectID: a.RecommendationID, AssigneeID: a.AssigneeID, DueAt: a.DueAt})
		}
		if a.ReviewStatus != "approved" {
			if p.EarliestDueAt == nil || a.DueAt.Before(*p.EarliestDueAt) {
				due := a.DueAt
				p.EarliestDueAt, p.EarliestAssigneeID = &due, a.AssigneeID
			}
			if now.After(a.DueAt) {
				p.Overdue = true
			}
		}
	}
	if t.Status == domain.StatusReviewed {
		p.Todos = append(p.Todos, domain.TodoItem{Type: "closable", ObjectID: t.TaskID, AssigneeID: t.OwnerID})
	}
	switch {
	case t.Status == domain.StatusClosed:
		p.ScheduleStatus = "completed"
	case now.Before(t.WindowStart):
		p.ScheduleStatus = "not_started"
	case p.Overdue || now.After(t.WindowEnd):
		p.ScheduleStatus, p.Overdue = "overdue", true
	default:
		p.ScheduleStatus = "in_progress"
	}
	if p.Overdue {
		p.ScheduleStatus = "overdue"
	}
	return p
}

var validStatuses = map[domain.TaskStatus]bool{
	domain.StatusPendingInspection: true, domain.StatusPendingAction: true, domain.StatusPendingReview: true,
	domain.StatusReviewed: true, domain.StatusClosed: true,
}

func parseCursor(cursor string) (time.Time, string, error) {
	if cursor == "" {
		return time.Time{}, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", validation("非法游标", "cursor")
	}
	var value struct {
		At time.Time `json:"at"`
		ID string    `json:"id"`
	}
	if json.Unmarshal(raw, &value) != nil || value.At.IsZero() || value.ID == "" {
		return time.Time{}, "", validation("非法游标", "cursor")
	}
	return value.At, value.ID, nil
}

func makeCursor(at time.Time, id string) string {
	b, _ := json.Marshal(struct {
		At time.Time `json:"at"`
		ID string    `json:"id"`
	}{at, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

type sortableProgress struct {
	progress domain.TaskProgress
	at       time.Time
}

func (s *Service) List(filter ListFilter) (ListResult, error) {
	if filter.Status != "" && !validStatuses[filter.Status] {
		return ListResult{}, validation("status筛选值无效", "status")
	}
	if filter.Limit == 0 {
		filter.Limit = 20
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		return ListResult{}, validation("limit必须在1到100之间", "limit")
	}
	cursorAt, cursorID, err := parseCursor(filter.Cursor)
	if err != nil {
		return ListResult{}, err
	}
	now := s.now()
	items := []sortableProgress{}
	view := s.Store.ReadWorkflowSnapshot()
	for _, t := range view.Tasks {
		if filter.OwnerID != "" && t.OwnerID != filter.OwnerID {
			continue
		}
		if filter.ArtifactID != "" && t.ArtifactID != filter.ArtifactID {
			continue
		}
		if filter.Status != "" && t.Status != filter.Status {
			continue
		}
		inspection, hasInspection := view.ActiveInspections[t.TaskID]
		actions := view.Actions[t.TaskID]
		p := s.progress(t, now, inspection, hasInspection, actions)
		if filter.Overdue != nil && p.Overdue != *filter.Overdue {
			continue
		}
		sortAt := t.CreatedAt
		if p.Overdue {
			sortAt = t.WindowEnd
			for _, a := range actions {
				if a.ReviewStatus != "approved" && a.DueAt.Before(sortAt) {
					sortAt = a.DueAt
				}
			}
		}
		items = append(items, sortableProgress{p, sortAt})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].at.Equal(items[j].at) {
			return items[i].progress.TaskID < items[j].progress.TaskID
		}
		return items[i].at.Before(items[j].at)
	})
	start := 0
	if cursorID != "" {
		found := false
		for idx, item := range items {
			if item.at.Equal(cursorAt) && item.progress.TaskID == cursorID {
				start, found = idx+1, true
				break
			}
		}
		if !found {
			return ListResult{}, validation("游标已失效或不属于当前筛选结果", "cursor")
		}
	}
	end := start + filter.Limit
	if end > len(items) {
		end = len(items)
	}
	result := ListResult{Items: []domain.TaskProgress{}, Count: end - start}
	for _, item := range items[start:end] {
		result.Items = append(result.Items, item.progress)
	}
	if end < len(items) {
		result.NextCursor = makeCursor(items[end-1].at, items[end-1].progress.TaskID)
	}
	return result, nil
}

func ParseLimit(value string) (int, error) {
	if value == "" {
		return 20, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, validation("limit必须是整数", "limit")
	}
	return n, nil
}

func ParseOverdue(value string) (*bool, error) {
	if value == "" {
		return nil, nil
	}
	if value != "true" && value != "false" {
		return nil, validation("overdue必须为true或false", "overdue")
	}
	v := value == "true"
	return &v, nil
}

func (s *Service) Close(id, actorID string, expected int) (domain.AuditSummary, error) {
	if strings.TrimSpace(actorID) == "" {
		t, err := s.Get(id)
		if err != nil {
			return domain.AuditSummary{}, err
		}
		actorID = t.OwnerID
	}
	return s.Store.CloseTaskAtomic(id, actorID, expected, s.now())
}

func (s *Service) Advance(id string, expected int, status domain.TaskStatus) (domain.ConservationTask, error) {
	t, err := s.Get(id)
	if err != nil {
		return t, err
	}
	if t.Status == domain.StatusClosed {
		return t, errors.New("任务已关闭")
	}
	t.Status, t.Revision = status, t.Revision+1
	if err = s.Store.UpdateTask(t, expected); err != nil {
		return t, err
	}
	return t, nil
}

func MissingAnomalies(inspection domain.InspectionEntry, actions []domain.ActionRecommendation) []string {
	out := []string{}
	for _, code := range inspection.Anomalies {
		found := false
		for _, a := range actions {
			if a.AnomalyCode == code {
				found = true
			}
		}
		if !found && !contains(out, code) {
			out = append(out, code)
		}
	}
	sort.Strings(out)
	return out
}
