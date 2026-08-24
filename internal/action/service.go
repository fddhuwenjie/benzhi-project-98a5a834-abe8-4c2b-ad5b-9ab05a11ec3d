package action

import (
	"crypto/rand"
	"fmt"
	"heritage-care/internal/domain"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	Store             *storage.Store
	Tasks             *task.Service
	inspectionCacheMu sync.Mutex
	inspectionCache   map[string]domain.InspectionEntry
}

type CreateInput struct {
	AnomalyCode    string `json:"anomaly_code"`
	AssigneeID     string `json:"assignee_id"`
	DueAt          string `json:"due_at"`
	Accepted       bool   `json:"accepted"`
	Revision       int    `json:"revision"`
	IdempotencyKey string `json:"-"`
}

type SubmitInput struct {
	SubmitterID    string   `json:"submitter_id"`
	ExecutorID     string   `json:"executor_id,omitempty"`
	ResultText     string   `json:"result_text"`
	CompletedAt    string   `json:"completed_at"`
	EvidenceRefs   []string `json:"evidence_refs"`
	Revision       int      `json:"revision"`
	ActionRevision int      `json:"action_revision"`
	IdempotencyKey string   `json:"-"`
}

type ReviewInput struct {
	Decision             string   `json:"decision"`
	ReviewerID           string   `json:"reviewer_id"`
	Comment              string   `json:"comment"`
	ReviewComment        string   `json:"review_comment,omitempty"`
	MissingEvidenceItems []string `json:"missing_evidence_items"`
	EvidenceComplete     *bool    `json:"evidence_complete"`
	Revision             int      `json:"revision"`
	ActionRevision       int      `json:"action_revision"`
	ResultText           string   `json:"result_text,omitempty"`
	EvidenceRefs         []string `json:"evidence_refs,omitempty"`
}

func randomID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%x", prefix, b)
}

func invalid(message, field string) error {
	return domain.NewError("validation_error", message, field)
}

func recommendationRule(code string) (severity, text string, maxDuration time.Duration) {
	switch code {
	case "appearance", "material", "emergency":
		return "high", "立即隔离文物并安排专业修复评估", 24 * time.Hour
	case "temperature", "humidity", "environment":
		return "medium", "调整温湿度参数并在处置后复测", 72 * time.Hour
	case "illuminance":
		return "medium", "降低照度或缩短曝光时间并复测", 48 * time.Hour
	default:
		return "low", "完成针对性处置并增加巡检频次", 7 * 24 * time.Hour
	}
}

func (s *Service) now() time.Time {
	if s.Tasks.Now != nil {
		return s.Tasks.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) cachedInspection(taskID string) (domain.InspectionEntry, bool) {
	s.inspectionCacheMu.Lock()
	defer s.inspectionCacheMu.Unlock()
	if inspection, ok := s.inspectionCache[taskID]; ok {
		return inspection, true
	}
	inspection, ok := s.Store.ActiveInspection(taskID)
	if !ok {
		return domain.InspectionEntry{}, false
	}
	if s.inspectionCache == nil {
		s.inspectionCache = map[string]domain.InspectionEntry{}
	}
	s.inspectionCache[taskID] = inspection
	return inspection, true
}

func (s *Service) Recommend(taskID string, in CreateInput) (domain.ActionRecommendation, bool, error) {
	_, err := s.Tasks.Get(taskID)
	if err != nil {
		return domain.ActionRecommendation{}, false, err
	}
	inspection, ok := s.cachedInspection(taskID)
	if !ok {
		return domain.ActionRecommendation{}, false, domain.NewError("state_conflict", "缺少当前有效巡检", "task_id")
	}
	in.AssigneeID, in.AnomalyCode = strings.TrimSpace(in.AssigneeID), strings.TrimSpace(in.AnomalyCode)
	if in.AssigneeID == "" {
		return domain.ActionRecommendation{}, false, invalid("assignee_id不能为空", "assignee_id")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return domain.ActionRecommendation{}, false, invalid("缺少Idempotency-Key", "Idempotency-Key")
	}
	legacyCombined := in.AnomalyCode == ""
	if legacyCombined {
		if len(inspection.Anomalies) == 0 {
			return domain.ActionRecommendation{}, false, invalid("当前巡检没有可处置异常", "anomaly_code")
		}
		in.AnomalyCode = inspection.Anomalies[0]
	}
	found := false
	for _, code := range inspection.Anomalies {
		if code == in.AnomalyCode {
			found = true
		}
	}
	if !found {
		return domain.ActionRecommendation{}, false, invalid("anomaly_code不属于当前有效巡检", "anomaly_code")
	}
	severity, text, maxDuration := recommendationRule(in.AnomalyCode)
	now := s.now()
	due := now.Add(maxDuration)
	if in.DueAt != "" {
		due, err = time.Parse(time.RFC3339, in.DueAt)
		if err != nil {
			return domain.ActionRecommendation{}, false, invalid("due_at格式应为RFC3339", "due_at")
		}
		due = due.UTC()
		if !due.After(now) {
			return domain.ActionRecommendation{}, false, invalid("due_at必须晚于当前时间", "due_at")
		}
		if due.After(now.Add(maxDuration)) {
			return domain.ActionRecommendation{}, false, &domain.BusinessError{Code: "due_limit_exceeded", Message: "due_at晚于严重度允许上限", Field: "due_at", Details: map[string]any{"severity": severity, "latest_due_at": now.Add(maxDuration)}}
		}
	}
	a := domain.ActionRecommendation{
		RecommendationID: randomID("action"), TaskID: taskID, SourceInspectionID: inspection.InspectionID,
		AnomalyCode: in.AnomalyCode, Severity: severity, ActionText: text, AssigneeID: in.AssigneeID,
		DueAt: due, Accepted: in.Accepted, ReviewStatus: "pending_submission", Revision: 1,
		CreatedAt: now, IdempotencyKey: in.IdempotencyKey, Submissions: []domain.ActionSubmission{}, Reviews: []domain.ActionReview{},
		LegacyCombined: legacyCombined,
	}
	if in.Accepted {
		at := now
		a.AcceptedAt = &at
	}
	dueAtFingerprint := ""
	if in.DueAt != "" {
		dueAtFingerprint = due.Format(time.RFC3339Nano)
	}
	fingerprint := storage.Digest(struct {
		TaskID       string
		InspectionID string
		AnomalyCode  string
		AssigneeID   string
		DueAt        string
		Accepted     bool
	}{taskID, inspection.InspectionID, in.AnomalyCode, in.AssigneeID, dueAtFingerprint, in.Accepted})
	stored, reused, _, err := s.Store.CreateActionAtomic(a, in.Revision, in.IdempotencyKey, fingerprint)
	return stored, reused, err
}

func uniqueEvidence(refs []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return nil, invalid("证据引用不能为空", "evidence_refs")
		}
		if seen[ref] {
			return nil, invalid("证据引用不能重复", "evidence_refs")
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out, nil
}

func (s *Service) Submit(actionID string, in SubmitInput) (domain.ActionRecommendation, bool, error) {
	a, ok := s.Store.GetAction(actionID)
	if !ok {
		return a, false, domain.NewError("not_found", "处置建议不存在", "recommendation_id")
	}
	t, err := s.Tasks.Get(a.TaskID)
	if err != nil {
		return a, false, err
	}
	in.SubmitterID = strings.TrimSpace(in.SubmitterID)
	if in.SubmitterID == "" {
		in.SubmitterID = strings.TrimSpace(in.ExecutorID)
	}
	if in.SubmitterID == "" {
		return a, false, invalid("submitter_id不能为空", "submitter_id")
	}
	if in.SubmitterID != a.AssigneeID {
		return a, false, invalid("提交人必须等于处置责任人", "submitter_id")
	}
	if strings.TrimSpace(in.ResultText) == "" {
		return a, false, invalid("result_text不能为空", "result_text")
	}
	evidence, err := uniqueEvidence(in.EvidenceRefs)
	if err != nil {
		return a, false, err
	}
	if len(evidence) == 0 {
		return a, false, invalid("处置结果必须包含证据引用", "evidence_refs")
	}
	now := s.now()
	completed := now
	if in.CompletedAt != "" {
		completed, err = time.Parse(time.RFC3339, in.CompletedAt)
		if err != nil {
			return a, false, invalid("completed_at格式应为RFC3339", "completed_at")
		}
		completed = completed.UTC()
		if completed.After(now.Add(5 * time.Minute)) {
			return a, false, invalid("completed_at不能晚于当前时间允许偏差", "completed_at")
		}
	}
	actionRevision := in.ActionRevision
	if actionRevision == 0 {
		actionRevision = a.Revision
	}
	taskRevision := in.Revision
	if taskRevision == 0 {
		taskRevision = t.Revision
	}
	submission := domain.ActionSubmission{
		SubmissionID: randomID("submission"), Version: len(a.Submissions) + 1,
		SubmitterID: in.SubmitterID, ResultText: strings.TrimSpace(in.ResultText), CompletedAt: completed,
		EvidenceRefs: evidence, SubmittedAt: now,
	}
	completedAtFingerprint := ""
	if in.CompletedAt != "" {
		completedAtFingerprint = completed.Format(time.RFC3339Nano)
	}
	fingerprint := storage.Digest(struct {
		ActionID     string
		SubmitterID  string
		ResultText   string
		CompletedAt  string
		EvidenceRefs []string
	}{actionID, submission.SubmitterID, submission.ResultText, completedAtFingerprint, evidence})
	stored, reused, _, err := s.Store.SubmitActionAtomic(actionID, submission, actionRevision, taskRevision, in.IdempotencyKey, fingerprint)
	return stored, reused, err
}

func normalizeDecision(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "approved", "approve":
		return "approved"
	case "rejected", "reject":
		return "rejected"
	default:
		return ""
	}
}

func (s *Service) Review(actionID string, in ReviewInput) (domain.ActionRecommendation, error) {
	a, ok := s.Store.GetAction(actionID)
	if !ok {
		return a, domain.NewError("not_found", "处置建议不存在", "recommendation_id")
	}
	t, err := s.Tasks.Get(a.TaskID)
	if err != nil {
		return a, err
	}
	in.ReviewerID = strings.TrimSpace(in.ReviewerID)
	if in.ReviewerID == "" {
		return a, invalid("reviewer_id不能为空", "reviewer_id")
	}
	if in.ReviewerID == a.AssigneeID {
		return a, invalid("复核人不能与处置责任人相同", "reviewer_id")
	}
	decision := normalizeDecision(in.Decision)
	if decision == "" && strings.TrimSpace(in.ResultText) != "" {
		if in.EvidenceComplete == nil || !*in.EvidenceComplete {
			return a, invalid("证据不完整，无法批准", "evidence_complete")
		}
		now := s.now()
		submission := domain.ActionSubmission{
			SubmissionID: randomID("submission"), Version: len(a.Submissions) + 1, SubmitterID: a.AssigneeID,
			ResultText: strings.TrimSpace(in.ResultText), CompletedAt: now, EvidenceRefs: append([]string(nil), in.EvidenceRefs...), SubmittedAt: now,
		}
		review := domain.ActionReview{
			ReviewID: randomID("review"), SubmissionVersion: submission.Version, ReviewerID: in.ReviewerID,
			Decision: "approved", EvidenceComplete: true, ReviewedAt: now,
		}
		stored, _, err := s.Store.CompleteAndApproveActionAtomic(actionID, submission, review)
		return stored, err
	}
	if decision == "" {
		return a, invalid("decision必须为approved或rejected", "decision")
	}
	if len(a.Submissions) == 0 {
		return a, domain.NewError("state_conflict", "处置结果尚未提交", "")
	}
	comment := strings.TrimSpace(in.Comment)
	if comment == "" {
		comment = strings.TrimSpace(in.ReviewComment)
	}
	missing := []string{}
	seen := map[string]bool{}
	for _, item := range in.MissingEvidenceItems {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			missing = append(missing, item)
		}
	}
	sort.Strings(missing)
	evidenceComplete := in.EvidenceComplete != nil && *in.EvidenceComplete
	if decision == "rejected" {
		if comment == "" {
			return a, invalid("驳回必须填写复核意见", "comment")
		}
		if len(missing) == 0 {
			return a, invalid("驳回必须列出缺失证据项", "missing_evidence_items")
		}
		evidenceComplete = false
	} else {
		if !evidenceComplete {
			return a, invalid("批准必须确认evidence_complete为true", "evidence_complete")
		}
		if len(missing) != 0 {
			return a, invalid("批准时不能包含缺失证据项", "missing_evidence_items")
		}
	}
	actionRevision := in.ActionRevision
	if actionRevision == 0 {
		actionRevision = a.Revision
	}
	taskRevision := in.Revision
	if taskRevision == 0 {
		taskRevision = t.Revision
	}
	review := domain.ActionReview{
		ReviewID: randomID("review"), SubmissionVersion: a.Submissions[len(a.Submissions)-1].Version,
		ReviewerID: in.ReviewerID, Decision: decision, Comment: comment, MissingEvidenceItems: missing,
		EvidenceComplete: evidenceComplete, ReviewedAt: s.now(),
	}
	stored, _, err := s.Store.ReviewActionAtomic(actionID, review, actionRevision, taskRevision)
	return stored, err
}
