package domain

import "time"

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type TaskStatus string

const (
	StatusPendingInspection TaskStatus = "pending_inspection"
	StatusPendingAction     TaskStatus = "pending_action"
	StatusPendingReview     TaskStatus = "pending_review"
	StatusReviewed          TaskStatus = "reviewed"
	StatusClosed            TaskStatus = "closed"
)

type BusinessError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Field   string         `json:"field,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *BusinessError) Error() string { return e.Message }

func NewError(code, message, field string) *BusinessError {
	return &BusinessError{Code: code, Message: message, Field: field}
}

type ArtifactRecord struct {
	ArtifactID         string    `json:"artifact_id"`
	Title              string    `json:"title"`
	Material           string    `json:"material"`
	Era                string    `json:"era"`
	Location           string    `json:"location"`
	SensitivityProfile string    `json:"sensitivity_profile"`
	CurrentRiskLevel   RiskLevel `json:"current_risk_level"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ThresholdSet struct {
	TemperatureMin  float64 `json:"temperature_min"`
	TemperatureMax  float64 `json:"temperature_max"`
	HumidityMin     float64 `json:"humidity_min"`
	HumidityMax     float64 `json:"humidity_max"`
	IlluminanceMax  float64 `json:"illuminance_max"`
	TemperatureUnit string  `json:"temperature_unit"`
	HumidityUnit    string  `json:"humidity_unit"`
	IlluminanceUnit string  `json:"illuminance_unit"`
}

type ChecklistItem struct {
	Code       string       `json:"code"`
	Label      string       `json:"label"`
	Threshold  string       `json:"threshold"`
	Thresholds ThresholdSet `json:"applicable_thresholds"`
}

type RiskScoreDetail struct {
	RuleCode    string `json:"rule_code"`
	Metric      string `json:"metric"`
	HitValue    string `json:"hit_value"`
	Threshold   string `json:"threshold"`
	Score       int    `json:"score"`
	Explanation string `json:"explanation"`
}

type RiskSnapshot struct {
	Level        RiskLevel         `json:"level"`
	Score        int               `json:"score"`
	RuleVersion  string            `json:"rule_version"`
	Details      []string          `json:"details"`
	ScoreDetails []RiskScoreDetail `json:"score_details"`
	Thresholds   ThresholdSet      `json:"applicable_thresholds"`
}

type ConservationTask struct {
	TaskID              string          `json:"task_id"`
	ArtifactID          string          `json:"artifact_id"`
	OwnerID             string          `json:"owner_id"`
	WindowStart         time.Time       `json:"window_start"`
	WindowEnd           time.Time       `json:"window_end"`
	Status              TaskStatus      `json:"status"`
	Revision            int             `json:"revision"`
	Checklist           []ChecklistItem `json:"checklist"`
	RiskSnapshot        RiskSnapshot    `json:"risk_snapshot"`
	CurrentInspectionID string          `json:"current_inspection_id,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	ClosedAt            *time.Time      `json:"closed_at,omitempty"`
}

type Measurement struct {
	Type  string  `json:"type"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type InspectionResult struct {
	Code         string        `json:"code"`
	Conclusion   string        `json:"conclusion"`
	Observation  string        `json:"observation"`
	Measurements []Measurement `json:"measurements,omitempty"`
	EvidenceRefs []string      `json:"evidence_refs,omitempty"`
}

type InspectionSummary struct {
	CoveragePercent      float64  `json:"coverage_percent"`
	AnomalyCount         int      `json:"anomaly_count"`
	AnomalyCodes         []string `json:"anomaly_codes"`
	EvidenceCompleteness float64  `json:"evidence_completeness"`
}

type InspectionEntry struct {
	InspectionID           string             `json:"inspection_id"`
	TaskID                 string             `json:"task_id"`
	InspectorID            string             `json:"inspector_id"`
	Temperature            float64            `json:"temperature"`
	Humidity               float64            `json:"humidity"`
	Illuminance            float64            `json:"illuminance"`
	Observations           string             `json:"observations"`
	Results                []InspectionResult `json:"checklist_results"`
	Summary                InspectionSummary  `json:"summary"`
	Anomalies              []string           `json:"anomalies"`
	EvidenceRefs           []string           `json:"evidence_refs"`
	RecordedAt             time.Time          `json:"recorded_at"`
	CreatedAt              time.Time          `json:"created_at"`
	SupersedesInspectionID string             `json:"supersedes_inspection_id,omitempty"`
	SupersededBy           string             `json:"superseded_by,omitempty"`
	CorrectionReason       string             `json:"correction_reason,omitempty"`
	Active                 bool               `json:"active"`
	Version                int                `json:"version"`
	IdempotencyKey         string             `json:"idempotency_key"`
}

type ActionSubmission struct {
	SubmissionID string    `json:"submission_id"`
	Version      int       `json:"version"`
	SubmitterID  string    `json:"submitter_id"`
	ResultText   string    `json:"result_text"`
	CompletedAt  time.Time `json:"completed_at"`
	EvidenceRefs []string  `json:"evidence_refs"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

type ActionReview struct {
	ReviewID             string    `json:"review_id"`
	SubmissionVersion    int       `json:"submission_version"`
	ReviewerID           string    `json:"reviewer_id"`
	Decision             string    `json:"decision"`
	Comment              string    `json:"comment,omitempty"`
	MissingEvidenceItems []string  `json:"missing_evidence_items,omitempty"`
	EvidenceComplete     bool      `json:"evidence_complete"`
	ReviewedAt           time.Time `json:"reviewed_at"`
}

type ActionRecommendation struct {
	RecommendationID   string             `json:"recommendation_id"`
	TaskID             string             `json:"task_id"`
	SourceInspectionID string             `json:"source_inspection_id"`
	AnomalyCode        string             `json:"anomaly_code"`
	Severity           string             `json:"severity"`
	ActionText         string             `json:"action_text"`
	AssigneeID         string             `json:"assignee_id"`
	DueAt              time.Time          `json:"due_at"`
	Accepted           bool               `json:"accepted"`
	AcceptedAt         *time.Time         `json:"accepted_at,omitempty"`
	ResultText         string             `json:"result_text,omitempty"`
	ReviewerID         string             `json:"reviewer_id,omitempty"`
	ReviewStatus       string             `json:"review_status"`
	EvidenceComplete   bool               `json:"evidence_complete"`
	Submissions        []ActionSubmission `json:"submissions"`
	Reviews            []ActionReview     `json:"reviews"`
	Revision           int                `json:"revision"`
	CreatedAt          time.Time          `json:"created_at"`
	IdempotencyKey     string             `json:"idempotency_key"`
	LegacyCombined     bool               `json:"legacy_combined,omitempty"`
}

type AuditEvent struct {
	Sequence       int            `json:"sequence"`
	EventType      string         `json:"event_type"`
	OccurredAt     time.Time      `json:"occurred_at"`
	ActorID        string         `json:"actor_id"`
	ObjectType     string         `json:"object_type"`
	ObjectID       string         `json:"object_id"`
	Revision       int            `json:"revision"`
	Data           map[string]any `json:"data,omitempty"`
	PreviousDigest string         `json:"previous_digest"`
	Digest         string         `json:"digest"`
}

type AuditSummary struct {
	TaskID             string       `json:"task_id"`
	ClosedAt           time.Time    `json:"closed_at"`
	Digest             string       `json:"digest"`
	SnapshotDigest     string       `json:"snapshot_digest"`
	Events             []string     `json:"events,omitempty"`
	Timeline           []AuditEvent `json:"timeline"`
	VerificationStatus string       `json:"verification_status"`
	FailureSequence    int          `json:"failure_sequence,omitempty"`
	FailureReason      string       `json:"failure_reason,omitempty"`
}

type TodoItem struct {
	Type       string    `json:"type"`
	ObjectID   string    `json:"object_id,omitempty"`
	AssigneeID string    `json:"assignee_id,omitempty"`
	DueAt      time.Time `json:"due_at,omitempty"`
}

type TaskProgress struct {
	TaskID              string     `json:"task_id"`
	ArtifactID          string     `json:"artifact_id"`
	OwnerID             string     `json:"owner_id"`
	Status              TaskStatus `json:"status"`
	Revision            int        `json:"revision"`
	ChecklistCompletion float64    `json:"checklist_completion"`
	AnomalyCount        int        `json:"anomaly_count"`
	ActionCoverage      int        `json:"action_coverage"`
	ApprovedCount       int        `json:"approved_count"`
	EvidenceGapCount    int        `json:"evidence_gap_count"`
	ScheduleStatus      string     `json:"schedule_status"`
	Overdue             bool       `json:"overdue"`
	EarliestDueAt       *time.Time `json:"earliest_due_at,omitempty"`
	EarliestAssigneeID  string     `json:"earliest_assignee_id,omitempty"`
	UncoveredAnomalies  []string   `json:"uncovered_anomalies"`
	Todos               []TodoItem `json:"todos"`
}
