package storage

import (
	"fmt"
	"heritage-care/internal/domain"
	"sort"
	"time"
)

const closedStatus = domain.StatusClosed

func overlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return aStart.Before(bEnd) && bStart.Before(aEnd)
}

func revisionError(revision int, status domain.TaskStatus) error {
	return &domain.BusinessError{
		Code: "revision_conflict", Message: fmt.Sprintf("revision冲突，当前revision为%d", revision),
		Field: "revision", Details: map[string]any{"current_revision": revision, "current_status": status},
	}
}

// LookupTaskIdempotency returns the stored task when the given key and fingerprint
// match an existing task idempotency record. It allows callers to short-circuit
// creation-time validations (such as time-dependent window checks) for identical
// replays. On a conflicting request it returns an idempotency_conflict error.
func (s *Store) LookupTaskIdempotency(key, fingerprint string) (domain.ConservationTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, reused, err := s.lookupIdempotencyLocked(key, fingerprint, "task")
	if err != nil || !reused {
		return domain.ConservationTask{}, false, err
	}
	t, ok := s.data.Tasks[id]
	if !ok {
		return domain.ConservationTask{}, false, domain.NewError("integrity_error", "幂等索引指向的任务不存在", "")
	}
	return t, true, nil
}

func (s *Store) CreateTaskAtomic(t domain.ConservationTask, idem, fingerprint string) (domain.ConservationTask, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, reused, err := s.lookupIdempotencyLocked(idem, fingerprint, "task"); err != nil {
		return domain.ConservationTask{}, false, err
	} else if reused {
		old, ok := s.data.Tasks[id]
		if !ok {
			return domain.ConservationTask{}, false, domain.NewError("integrity_error", "幂等索引指向的任务不存在", "")
		}
		return old, true, nil
	}
	for _, old := range s.data.Tasks {
		if old.Status == closedStatus || !overlap(t.WindowStart, t.WindowEnd, old.WindowStart, old.WindowEnd) {
			continue
		}
		reason := ""
		if old.ArtifactID == t.ArtifactID {
			reason = "artifact_id"
		}
		if old.OwnerID == t.OwnerID {
			if reason == "" {
				reason = "owner_id"
			} else {
				reason = "artifact_id,owner_id"
			}
		}
		if reason == "" {
			continue
		}
		start := t.WindowStart
		if old.WindowStart.After(start) {
			start = old.WindowStart
		}
		end := t.WindowEnd
		if old.WindowEnd.Before(end) {
			end = old.WindowEnd
		}
		return domain.ConservationTask{}, false, &domain.BusinessError{
			Code: "schedule_conflict", Message: "计划窗口与未关闭任务交叠", Field: reason,
			Details: map[string]any{
				"conflict_task_id": old.TaskID, "conflict_status": old.Status,
				"conflict_start": start, "conflict_end": end,
			},
		}
	}
	before := s.cloneSnapshotLocked()
	s.data.Tasks[t.TaskID] = t
	s.saveIdempotencyLocked(idem, fingerprint, "task", t.TaskID)
	s.appendEventLocked(t.TaskID, "task_created", t.OwnerID, "task", t.TaskID, t.Revision,
		map[string]any{"artifact_id": t.ArtifactID, "window_start": t.WindowStart, "window_end": t.WindowEnd}, t.CreatedAt)
	if err := s.commitLocked("task:create:"+t.TaskID, before); err != nil {
		return domain.ConservationTask{}, false, err
	}
	return t, false, nil
}

func (s *Store) RecordInspectionAtomic(entry domain.InspectionEntry, expectedRevision int, idem, fingerprint string) (domain.InspectionEntry, bool, domain.ConservationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, reused, err := s.lookupIdempotencyLocked(idem, fingerprint, "inspection"); err != nil {
		return domain.InspectionEntry{}, false, domain.ConservationTask{}, err
	} else if reused {
		old, ok := s.data.Inspections[id]
		if !ok {
			return domain.InspectionEntry{}, false, domain.ConservationTask{}, domain.NewError("integrity_error", "幂等索引指向的巡检不存在", "")
		}
		return old, true, s.data.Tasks[old.TaskID], nil
	}
	t, ok := s.data.Tasks[entry.TaskID]
	if !ok {
		return domain.InspectionEntry{}, false, t, domain.NewError("not_found", "任务不存在", "task_id")
	}
	if t.Revision != expectedRevision {
		return domain.InspectionEntry{}, false, t, revisionError(t.Revision, t.Status)
	}
	if t.Status == domain.StatusClosed || t.Status == domain.StatusReviewed {
		return domain.InspectionEntry{}, false, t, &domain.BusinessError{Code: "state_conflict", Message: "任务当前不可巡检", Details: map[string]any{"current_status": t.Status, "current_revision": t.Revision}}
	}
	eventType := "inspection_recorded"
	before := s.cloneSnapshotLocked()
	if entry.SupersedesInspectionID == "" {
		if t.Status != domain.StatusPendingInspection || t.CurrentInspectionID != "" {
			return domain.InspectionEntry{}, false, t, &domain.BusinessError{Code: "state_conflict", Message: "任务已有有效巡检", Details: map[string]any{"current_status": t.Status, "current_revision": t.Revision}}
		}
		entry.Version = 1
	} else {
		if len(s.actionsLocked(t.TaskID)) > 0 {
			return domain.InspectionEntry{}, false, t, &domain.BusinessError{Code: "state_conflict", Message: "任务已有处置建议，不能更正巡检", Details: map[string]any{"current_status": t.Status, "current_revision": t.Revision}}
		}
		current, found := s.activeInspectionLocked(t.TaskID)
		if !found || current.InspectionID != entry.SupersedesInspectionID {
			return domain.InspectionEntry{}, false, t, &domain.BusinessError{Code: "revision_conflict", Message: "只能更正当前有效巡检", Field: "supersedes_inspection_id", Details: map[string]any{"current_inspection_id": t.CurrentInspectionID, "current_revision": t.Revision}}
		}
		current.Active = false
		current.SupersededBy = entry.InspectionID
		s.data.Inspections[current.InspectionID] = current
		entry.Version = current.Version + 1
		eventType = "inspection_corrected"
	}
	entry.Active = true
	s.data.Inspections[entry.InspectionID] = entry
	t.CurrentInspectionID = entry.InspectionID
	t.Status = domain.StatusPendingAction
	t.Revision++
	s.data.Tasks[t.TaskID] = t
	s.saveIdempotencyLocked(idem, fingerprint, "inspection", entry.InspectionID)
	s.appendEventLocked(t.TaskID, eventType, entry.InspectorID, "inspection", entry.InspectionID, t.Revision,
		map[string]any{"supersedes_inspection_id": entry.SupersedesInspectionID, "anomalies": entry.Anomalies}, entry.CreatedAt)
	if err := s.commitLocked("inspection:"+entry.InspectionID, before); err != nil {
		return domain.InspectionEntry{}, false, t, err
	}
	return entry, false, t, nil
}

func anomalyCovered(actions []domain.ActionRecommendation, code string) bool {
	for _, a := range actions {
		if a.AnomalyCode == code {
			return true
		}
	}
	return false
}

func allAnomaliesCovered(anomalies []string, actions []domain.ActionRecommendation) bool {
	for _, code := range anomalies {
		if !anomalyCovered(actions, code) {
			return false
		}
	}
	return true
}

func missingCoverage(anomalies []string, actions []domain.ActionRecommendation) []string {
	missing := []string{}
	for _, code := range anomalies {
		if !anomalyCovered(actions, code) {
			missing = append(missing, code)
		}
	}
	sort.Strings(missing)
	return missing
}

func (s *Store) actionsWithReplacementLocked(taskID, actionID string, replacement domain.ActionRecommendation) []domain.ActionRecommendation {
	actions := s.actionsLocked(taskID)
	for idx := range actions {
		if actions[idx].RecommendationID == actionID {
			actions[idx] = replacement
		}
	}
	return actions
}

func (s *Store) CreateActionAtomic(a domain.ActionRecommendation, expectedRevision int, idem, fingerprint string) (domain.ActionRecommendation, bool, domain.ConservationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, reused, err := s.lookupIdempotencyLocked(idem, fingerprint, "action"); err != nil {
		return domain.ActionRecommendation{}, false, domain.ConservationTask{}, err
	} else if reused {
		old, ok := s.data.Actions[id]
		if !ok {
			return domain.ActionRecommendation{}, false, domain.ConservationTask{}, domain.NewError("integrity_error", "幂等索引指向的处置建议不存在", "")
		}
		return old, true, s.data.Tasks[old.TaskID], nil
	}
	t, ok := s.data.Tasks[a.TaskID]
	if !ok {
		return a, false, t, domain.NewError("not_found", "任务不存在", "task_id")
	}
	if expectedRevision != 0 && t.Revision != expectedRevision {
		return a, false, t, revisionError(t.Revision, t.Status)
	}
	actions := s.actionsLocked(t.TaskID)
	if anomalyCovered(actions, a.AnomalyCode) {
		return a, false, t, &domain.BusinessError{Code: "duplicate_coverage", Message: "该异常已有有效处置建议", Field: "anomaly_code", Details: map[string]any{"anomaly_code": a.AnomalyCode}}
	}
	if t.Status != domain.StatusPendingAction {
		return a, false, t, &domain.BusinessError{Code: "state_conflict", Message: "任务当前不可生成处置", Details: map[string]any{"current_status": t.Status, "current_revision": t.Revision}}
	}
	inspection, found := s.activeInspectionLocked(t.TaskID)
	if !found || inspection.InspectionID != a.SourceInspectionID {
		return a, false, t, domain.NewError("state_conflict", "缺少当前有效巡检", "source_inspection_id")
	}
	validAnomaly := false
	for _, code := range inspection.Anomalies {
		if code == a.AnomalyCode {
			validAnomaly = true
		}
	}
	if !validAnomaly {
		return a, false, t, domain.NewError("validation_error", "anomaly_code不属于当前有效巡检", "anomaly_code")
	}
	before := s.cloneSnapshotLocked()
	s.data.Actions[a.RecommendationID] = a
	s.saveIdempotencyLocked(idem, fingerprint, "action", a.RecommendationID)
	actions = append(actions, a)
	t.Revision++
	if allAnomaliesCovered(inspection.Anomalies, actions) {
		t.Status = domain.StatusPendingReview
	}
	s.data.Tasks[t.TaskID] = t
	s.appendEventLocked(t.TaskID, "action_assigned", a.AssigneeID, "action", a.RecommendationID, t.Revision,
		map[string]any{"anomaly_code": a.AnomalyCode, "severity": a.Severity, "due_at": a.DueAt}, a.CreatedAt)
	if err := s.commitLocked("action:"+a.RecommendationID, before); err != nil {
		return a, false, t, err
	}
	return a, false, t, nil
}

func (s *Store) SubmitActionAtomic(actionID string, submission domain.ActionSubmission, actionRevision, taskRevision int, idem, fingerprint string) (domain.ActionRecommendation, bool, domain.ConservationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idem != "" {
		if id, reused, err := s.lookupIdempotencyLocked(idem, fingerprint, "action_submission"); err != nil {
			return domain.ActionRecommendation{}, false, domain.ConservationTask{}, err
		} else if reused {
			old, ok := s.data.Actions[id]
			if !ok {
				return domain.ActionRecommendation{}, false, domain.ConservationTask{}, domain.NewError("integrity_error", "幂等索引指向的处置不存在", "")
			}
			return old, true, s.data.Tasks[old.TaskID], nil
		}
	}
	a, ok := s.data.Actions[actionID]
	if !ok {
		return a, false, domain.ConservationTask{}, domain.NewError("not_found", "处置建议不存在", "recommendation_id")
	}
	t := s.data.Tasks[a.TaskID]
	if a.Revision != actionRevision || t.Revision != taskRevision {
		return a, false, t, revisionError(t.Revision, t.Status)
	}
	if a.ReviewStatus == "approved" {
		return a, false, t, domain.NewError("state_conflict", "已批准版本不可改写", "revision")
	}
	before := s.cloneSnapshotLocked()
	a.Submissions = append(a.Submissions, submission)
	a.ResultText = submission.ResultText
	a.ReviewStatus = "pending_review"
	a.EvidenceComplete = false
	a.Revision++
	t.Revision++
	s.data.Actions[actionID] = a
	s.data.Tasks[t.TaskID] = t
	if idem != "" {
		s.saveIdempotencyLocked(idem, fingerprint, "action_submission", actionID)
	}
	s.appendEventLocked(t.TaskID, "action_result_submitted", submission.SubmitterID, "action", actionID, t.Revision,
		map[string]any{"submission_version": submission.Version, "evidence_count": len(submission.EvidenceRefs)}, submission.SubmittedAt)
	if err := s.commitLocked("action:submit:"+actionID, before); err != nil {
		return a, false, t, err
	}
	return a, false, t, nil
}

func (s *Store) ReviewActionAtomic(actionID string, review domain.ActionReview, actionRevision, taskRevision int) (domain.ActionRecommendation, domain.ConservationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.data.Actions[actionID]
	if !ok {
		return a, domain.ConservationTask{}, domain.NewError("not_found", "处置建议不存在", "recommendation_id")
	}
	t := s.data.Tasks[a.TaskID]
	if a.Revision != actionRevision || t.Revision != taskRevision {
		return a, t, revisionError(t.Revision, t.Status)
	}
	if a.ReviewStatus != "pending_review" || len(a.Submissions) == 0 {
		return a, t, &domain.BusinessError{Code: "state_conflict", Message: "处置结果当前不可复核", Details: map[string]any{"review_status": a.ReviewStatus, "current_revision": t.Revision}}
	}
	before := s.cloneSnapshotLocked()
	a.Reviews = append(a.Reviews, review)
	a.ReviewerID = review.ReviewerID
	a.EvidenceComplete = review.EvidenceComplete
	a.ReviewStatus = review.Decision
	a.Revision++
	t.Revision++
	allApproved := true
	for id, item := range s.data.Actions {
		if item.TaskID != t.TaskID {
			continue
		}
		if id == actionID {
			item = a
		}
		if item.ReviewStatus != "approved" {
			allApproved = false
		}
	}
	inspection, hasInspection := s.activeInspectionLocked(t.TaskID)
	if allApproved && hasInspection && allAnomaliesCovered(inspection.Anomalies, s.actionsWithReplacementLocked(t.TaskID, actionID, a)) {
		t.Status = domain.StatusReviewed
	} else {
		t.Status = domain.StatusPendingReview
	}
	s.data.Actions[actionID] = a
	s.data.Tasks[t.TaskID] = t
	eventType := "action_review_approved"
	if review.Decision == "rejected" {
		eventType = "action_review_rejected"
	}
	s.appendEventLocked(t.TaskID, eventType, review.ReviewerID, "action", actionID, t.Revision,
		map[string]any{"submission_version": review.SubmissionVersion, "missing_evidence_items": review.MissingEvidenceItems}, review.ReviewedAt)
	if err := s.commitLocked("action:review:"+actionID, before); err != nil {
		return a, t, err
	}
	return a, t, nil
}

func (s *Store) CompleteAndApproveActionAtomic(actionID string, submission domain.ActionSubmission, review domain.ActionReview) (domain.ActionRecommendation, domain.ConservationTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.data.Actions[actionID]
	if !ok {
		return a, domain.ConservationTask{}, domain.NewError("not_found", "处置建议不存在", "recommendation_id")
	}
	t := s.data.Tasks[a.TaskID]
	if a.ReviewStatus == "approved" {
		return a, t, domain.NewError("state_conflict", "处置建议已复核", "")
	}
	before := s.cloneSnapshotLocked()
	a.Submissions = append(a.Submissions, submission)
	a.Reviews = append(a.Reviews, review)
	a.ResultText, a.ReviewerID = submission.ResultText, review.ReviewerID
	a.EvidenceComplete, a.ReviewStatus = true, "approved"
	a.Revision++
	t.Revision++
	allApproved := true
	for id, item := range s.data.Actions {
		if item.TaskID != t.TaskID {
			continue
		}
		if id == actionID {
			item = a
		}
		if item.ReviewStatus != "approved" {
			allApproved = false
		}
	}
	inspection, hasInspection := s.activeInspectionLocked(t.TaskID)
	if allApproved && (a.LegacyCombined || (hasInspection && allAnomaliesCovered(inspection.Anomalies, s.actionsWithReplacementLocked(t.TaskID, actionID, a)))) {
		t.Status = domain.StatusReviewed
	}
	s.data.Actions[actionID], s.data.Tasks[t.TaskID] = a, t
	s.appendEventLocked(t.TaskID, "action_result_submitted", submission.SubmitterID, "action", actionID, t.Revision,
		map[string]any{"submission_version": submission.Version, "evidence_count": len(submission.EvidenceRefs)}, submission.SubmittedAt)
	s.appendEventLocked(t.TaskID, "action_review_approved", review.ReviewerID, "action", actionID, t.Revision,
		map[string]any{"submission_version": review.SubmissionVersion}, review.ReviewedAt)
	if err := s.commitLocked("action:legacy-review:"+actionID, before); err != nil {
		return a, t, err
	}
	return a, t, nil
}

func (s *Store) CloseTaskAtomic(taskID, actorID string, expectedRevision int, at time.Time) (domain.AuditSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.data.Tasks[taskID]
	if !ok {
		return domain.AuditSummary{}, domain.NewError("not_found", "任务不存在", "task_id")
	}
	if t.Revision != expectedRevision {
		return domain.AuditSummary{}, revisionError(t.Revision, t.Status)
	}
	if t.Status != domain.StatusReviewed {
		pending := []string{}
		for _, a := range s.actionsLocked(taskID) {
			if a.ReviewStatus != "approved" {
				pending = append(pending, a.RecommendationID)
			}
		}
		return domain.AuditSummary{}, &domain.BusinessError{Code: "state_conflict", Message: "任务尚有未批准处置，不能关闭", Details: map[string]any{"current_status": t.Status, "incomplete_recommendation_ids": pending}}
	}
	inspection, found := s.activeInspectionLocked(taskID)
	if !found {
		return domain.AuditSummary{}, domain.NewError("integrity_error", "缺少有效巡检快照", "")
	}
	actions := s.actionsLocked(taskID)
	if !allAnomaliesCovered(inspection.Anomalies, actions) {
		legacy := false
		for _, action := range actions {
			if action.LegacyCombined {
				legacy = true
			}
		}
		if !legacy {
			return domain.AuditSummary{}, &domain.BusinessError{Code: "state_conflict", Message: "巡检异常尚未全部生成处置建议", Details: map[string]any{"uncovered_anomalies": missingCoverage(inspection.Anomalies, actions)}}
		}
	}
	for _, a := range actions {
		if a.ReviewStatus != "approved" || !a.EvidenceComplete {
			return domain.AuditSummary{}, domain.NewError("state_conflict", "处置证据尚未完整复核", "")
		}
	}
	before := s.cloneSnapshotLocked()
	t.Status, t.ClosedAt = domain.StatusClosed, &at
	t.Revision++
	s.data.Tasks[taskID] = t
	s.appendEventLocked(taskID, "task_closed", actorID, "task", taskID, t.Revision, nil, at)
	events := append([]domain.AuditEvent(nil), s.data.Events[taskID]...)
	snapshotDigest := Digest(struct {
		Task       domain.ConservationTask       `json:"task"`
		Inspection domain.InspectionEntry        `json:"inspection"`
		Actions    []domain.ActionRecommendation `json:"actions"`
		Events     []domain.AuditEvent           `json:"events"`
	}{t, inspection, actions, events})
	lastDigest := ""
	if len(events) > 0 {
		lastDigest = events[len(events)-1].Digest
	}
	audit := domain.AuditSummary{TaskID: taskID, ClosedAt: at, SnapshotDigest: snapshotDigest, Timeline: events, VerificationStatus: "passed"}
	audit.Digest = Digest(struct {
		TaskID          string    `json:"task_id"`
		ClosedAt        time.Time `json:"closed_at"`
		SnapshotDigest  string    `json:"snapshot_digest"`
		LastEventDigest string    `json:"last_event_digest"`
	}{taskID, at, snapshotDigest, lastDigest})
	for _, e := range events {
		audit.Events = append(audit.Events, e.EventType)
	}
	s.data.Audits[taskID] = audit
	if err := s.commitLocked("task:close:"+taskID, before); err != nil {
		return domain.AuditSummary{}, err
	}
	return audit, nil
}

func (s *Store) VerifyAudit(taskID string) (domain.AuditSummary, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	audit, ok := s.data.Audits[taskID]
	if !ok {
		return audit, false
	}
	events := append([]domain.AuditEvent(nil), s.data.Events[taskID]...)
	audit.Timeline = events
	audit.Events = audit.Events[:0]
	for _, event := range events {
		audit.Events = append(audit.Events, event.EventType)
	}
	audit.VerificationStatus, audit.FailureSequence, audit.FailureReason = "passed", 0, ""
	prev := ""
	for idx, event := range events {
		expectedSeq := idx + 1
		if event.Sequence != expectedSeq {
			audit.VerificationStatus, audit.FailureSequence, audit.FailureReason = "failed", expectedSeq, "事件序号断裂"
			return audit, true
		}
		if event.PreviousDigest != prev {
			audit.VerificationStatus, audit.FailureSequence, audit.FailureReason = "failed", expectedSeq, "前一事件摘要不匹配"
			return audit, true
		}
		if eventDigest(event) != event.Digest {
			audit.VerificationStatus, audit.FailureSequence, audit.FailureReason = "failed", expectedSeq, "事件摘要不匹配"
			return audit, true
		}
		prev = event.Digest
	}
	t, taskOK := s.data.Tasks[taskID]
	inspection, inspectionOK := s.activeInspectionLocked(taskID)
	if !taskOK || !inspectionOK {
		audit.VerificationStatus, audit.FailureReason = "failed", "关闭快照缺失"
		return audit, true
	}
	actions := s.actionsLocked(taskID)
	currentSnapshotDigest := Digest(struct {
		Task       domain.ConservationTask       `json:"task"`
		Inspection domain.InspectionEntry        `json:"inspection"`
		Actions    []domain.ActionRecommendation `json:"actions"`
		Events     []domain.AuditEvent           `json:"events"`
	}{t, inspection, actions, events})
	if currentSnapshotDigest != audit.SnapshotDigest {
		audit.VerificationStatus, audit.FailureReason = "failed", "关闭快照摘要不匹配"
		return audit, true
	}
	lastDigest := ""
	if len(events) > 0 {
		lastDigest = events[len(events)-1].Digest
	}
	expectedFinal := Digest(struct {
		TaskID          string    `json:"task_id"`
		ClosedAt        time.Time `json:"closed_at"`
		SnapshotDigest  string    `json:"snapshot_digest"`
		LastEventDigest string    `json:"last_event_digest"`
	}{taskID, audit.ClosedAt, audit.SnapshotDigest, lastDigest})
	if expectedFinal != audit.Digest {
		audit.VerificationStatus, audit.FailureReason = "failed", "最终审计摘要不匹配"
	}
	return audit, true
}

func StableAnomalies(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
