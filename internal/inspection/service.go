package inspection

import (
	"crypto/rand"
	"fmt"
	"heritage-care/internal/domain"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	EarlyTolerance = 24 * time.Hour
	LateTolerance  = 24 * time.Hour
	ClockTolerance = 5 * time.Minute
)

type Service struct {
	Store *storage.Store
	Tasks *task.Service
}

type MeasurementInput struct {
	Type   string   `json:"type"`
	Metric string   `json:"metric,omitempty"`
	Value  *float64 `json:"value"`
	Unit   string   `json:"unit"`
}

type ItemInput struct {
	Code         string             `json:"code"`
	Conclusion   string             `json:"conclusion"`
	Observation  string             `json:"observation"`
	Measurements []MeasurementInput `json:"measurements"`
	EvidenceRefs []string           `json:"evidence_refs"`
	Temperature  *float64           `json:"temperature,omitempty"`
	Humidity     *float64           `json:"humidity,omitempty"`
	Illuminance  *float64           `json:"illuminance,omitempty"`
}

type Input struct {
	InspectorID            string      `json:"inspector_id"`
	Temperature            *float64    `json:"temperature"`
	Humidity               *float64    `json:"humidity"`
	Illuminance            *float64    `json:"illuminance"`
	TemperatureUnit        string      `json:"temperature_unit"`
	HumidityUnit           string      `json:"humidity_unit"`
	IlluminanceUnit        string      `json:"illuminance_unit"`
	Observations           string      `json:"observations"`
	EvidenceRefs           []string    `json:"evidence_refs"`
	ChecklistResults       []ItemInput `json:"checklist_results"`
	Items                  []ItemInput `json:"items,omitempty"`
	RecordedAt             string      `json:"recorded_at"`
	SupersedesInspectionID string      `json:"supersedes_inspection_id"`
	CorrectionReason       string      `json:"correction_reason"`
	Revision               int         `json:"revision"`
	IdempotencyKey         string      `json:"-"`
}

func id() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return fmt.Sprintf("insp-%x", b)
}

func invalid(message, field string) error {
	return domain.NewError("validation_error", message, field)
}

func normalizeConclusion(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "normal", "pass", "正常", "通过":
		return "normal", true
	case "abnormal", "fail", "异常", "不通过":
		return "abnormal", true
	default:
		return "", false
	}
}

func normalizeMetric(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "temperature", "temp", "温度":
		return "temperature"
	case "humidity", "relative_humidity", "rh", "湿度":
		return "humidity"
	case "illuminance", "light", "lux", "照度":
		return "illuminance"
	default:
		return ""
	}
}

func normalizeUnit(metric, value string, legacy bool) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(value))
	if legacy && v == "" {
		switch metric {
		case "temperature":
			return "celsius", true
		case "humidity":
			return "percent_rh", true
		case "illuminance":
			return "lux", true
		}
	}
	switch metric {
	case "temperature":
		if v == "celsius" || v == "c" || v == "°c" || v == "℃" {
			return "celsius", true
		}
	case "humidity":
		if v == "percent_rh" || v == "%rh" || v == "rh%" || v == "%" {
			return "percent_rh", true
		}
	case "illuminance":
		if v == "lux" || v == "lx" {
			return "lux", true
		}
	}
	return "", false
}

var simpleEvidence = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

func validEvidence(ref string) bool {
	if !simpleEvidence.MatchString(ref) {
		return false
	}
	if strings.Contains(ref, "://") {
		parsed, err := url.Parse(ref)
		return err == nil && parsed.Scheme != "" && (parsed.Host != "" || parsed.Opaque != "")
	}
	return true
}

func measurementValid(metric string, value float64) bool {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return false
	}
	switch metric {
	case "temperature":
		return value >= -30 && value <= 60
	case "humidity":
		return value >= 0 && value <= 100
	case "illuminance":
		return value >= 0 && value <= 100000
	default:
		return false
	}
}

func observationAbnormal(value string) bool {
	v := strings.ToLower(value)
	for _, word := range []string{"霉", "裂", "虫", "锈", "变形", "脱落", "破损", "mold", "crack", "rust", "damage"} {
		if strings.Contains(v, word) {
			return true
		}
	}
	return false
}

func (s *Service) legacyItems(t domain.ConservationTask, in Input) []ItemInput {
	out := make([]ItemInput, 0, len(t.Checklist))
	for _, item := range t.Checklist {
		conclusion := "normal"
		if item.Code == "appearance" && observationAbnormal(in.Observations) {
			conclusion = "abnormal"
		}
		result := ItemInput{Code: item.Code, Conclusion: conclusion, Observation: in.Observations}
		if strings.TrimSpace(result.Observation) == "" {
			result.Observation = "现场检查完成，未见异常"
		}
		if item.Code == "environment" {
			if in.Temperature != nil {
				result.Measurements = append(result.Measurements, MeasurementInput{Type: "temperature", Value: in.Temperature, Unit: in.TemperatureUnit})
			}
			if in.Humidity != nil {
				result.Measurements = append(result.Measurements, MeasurementInput{Type: "humidity", Value: in.Humidity, Unit: in.HumidityUnit})
			}
			if in.Illuminance != nil {
				result.Measurements = append(result.Measurements, MeasurementInput{Type: "illuminance", Value: in.Illuminance, Unit: in.IlluminanceUnit})
			}
		}
		out = append(out, result)
	}
	return out
}

func addAnomaly(set map[string]bool, code string) {
	if code != "" {
		set[code] = true
	}
}

func (s *Service) normalize(t domain.ConservationTask, in Input) ([]domain.InspectionResult, []string, []string, float64, float64, float64, error) {
	items := in.ChecklistResults
	if len(items) == 0 {
		items = in.Items
	}
	explicit := len(items) > 0
	if !explicit {
		items = s.legacyItems(t, in)
	}
	expected := map[string]bool{}
	for _, item := range t.Checklist {
		expected[item.Code] = true
	}
	seen := map[string]bool{}
	for _, item := range items {
		code := strings.TrimSpace(item.Code)
		if !expected[code] {
			return nil, nil, nil, 0, 0, 0, invalid("检查项不属于任务清单: "+code, "checklist_results.code")
		}
		if seen[code] {
			return nil, nil, nil, 0, 0, 0, invalid("检查项重复: "+code, "checklist_results.code")
		}
		seen[code] = true
	}
	missing := []string{}
	for _, item := range t.Checklist {
		if !seen[item.Code] {
			missing = append(missing, item.Code)
		}
	}
	if len(missing) > 0 {
		return nil, nil, nil, 0, 0, 0, &domain.BusinessError{Code: "checklist_incomplete", Message: "巡检缺少任务清单项", Field: "checklist_results", Details: map[string]any{"missing_codes": missing}}
	}

	// Pre-validate and deduplicate top-level evidence refs for legacy submissions.
	// In legacy mode these serve as a shared evidence cover for all abnormal items
	// whose abnormality is computed from measurements (e.g. humidity out of range).
	legacyEvidence := []string{}
	if !explicit {
		legacySeen := map[string]bool{}
		for _, ref := range in.EvidenceRefs {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if !validEvidence(ref) {
				return nil, nil, nil, 0, 0, 0, invalid("证据引用格式无效", "evidence_refs")
			}
			if !legacySeen[ref] {
				legacySeen[ref] = true
				legacyEvidence = append(legacyEvidence, ref)
			}
		}
	}

	results := make([]domain.InspectionResult, 0, len(items))
	anomalySet, evidenceSeen := map[string]bool{}, map[string]bool{}
	allEvidence := []string{}
	var temperature, humidity, illuminance float64
	for _, item := range items {
		conclusion, ok := normalizeConclusion(item.Conclusion)
		if !ok {
			return nil, nil, nil, 0, 0, 0, invalid("conclusion必须为normal或abnormal", "checklist_results.conclusion")
		}
		observation := strings.TrimSpace(item.Observation)
		if observation == "" {
			return nil, nil, nil, 0, 0, 0, invalid("每个检查项都必须填写observation", "checklist_results.observation")
		}
		if item.Temperature != nil {
			item.Measurements = append(item.Measurements, MeasurementInput{Type: "temperature", Value: item.Temperature, Unit: in.TemperatureUnit})
		}
		if item.Humidity != nil {
			item.Measurements = append(item.Measurements, MeasurementInput{Type: "humidity", Value: item.Humidity, Unit: in.HumidityUnit})
		}
		if item.Illuminance != nil {
			item.Measurements = append(item.Measurements, MeasurementInput{Type: "illuminance", Value: item.Illuminance, Unit: in.IlluminanceUnit})
		}
		normalizedMeasurements := []domain.Measurement{}
		computedAbnormal := false
		for _, measurement := range item.Measurements {
			metric := normalizeMetric(measurement.Type)
			if metric == "" {
				metric = normalizeMetric(measurement.Metric)
			}
			if metric == "" {
				return nil, nil, nil, 0, 0, 0, invalid("未知测量类型", "checklist_results.measurements.type")
			}
			if measurement.Value == nil {
				return nil, nil, nil, 0, 0, 0, invalid("测量值不能为空", "checklist_results.measurements.value")
			}
			unit, ok := normalizeUnit(metric, measurement.Unit, !explicit)
			if !ok {
				return nil, nil, nil, 0, 0, 0, invalid("测量单位与类型不匹配", "checklist_results.measurements.unit")
			}
			if !measurementValid(metric, *measurement.Value) {
				return nil, nil, nil, 0, 0, 0, invalid("测量值超出允许范围", "checklist_results.measurements.value")
			}
			value := *measurement.Value
			normalizedMeasurements = append(normalizedMeasurements, domain.Measurement{Type: metric, Value: value, Unit: unit})
			switch metric {
			case "temperature":
				temperature = value
				if value < t.RiskSnapshot.Thresholds.TemperatureMin || value > t.RiskSnapshot.Thresholds.TemperatureMax {
					addAnomaly(anomalySet, "temperature")
					computedAbnormal = true
				}
			case "humidity":
				humidity = value
				if value < t.RiskSnapshot.Thresholds.HumidityMin || value > t.RiskSnapshot.Thresholds.HumidityMax {
					addAnomaly(anomalySet, "humidity")
					computedAbnormal = true
				}
			case "illuminance":
				illuminance = value
				if value > t.RiskSnapshot.Thresholds.IlluminanceMax {
					addAnomaly(anomalySet, "illuminance")
					computedAbnormal = true
				}
			}
		}
		itemAbnormal := conclusion == "abnormal" || computedAbnormal
		if conclusion == "abnormal" && !computedAbnormal {
			addAnomaly(anomalySet, item.Code)
		}
		if item.Code == "appearance" && observationAbnormal(observation) {
			addAnomaly(anomalySet, "appearance")
			itemAbnormal = true
		}
		// Build the effective evidence list for this item. In legacy mode, top-level
		// evidence refs act as a shared cover: attach them to abnormal items that lack
		// their own evidence so that computed anomalies (e.g. humidity out of range)
		// retain evidence on both the result and the inspection record.
		itemEvidence := append([]string(nil), item.EvidenceRefs...)
		if itemAbnormal && len(itemEvidence) == 0 && !explicit {
			itemEvidence = append(itemEvidence, legacyEvidence...)
		}
		if itemAbnormal && len(itemEvidence) == 0 {
			return nil, nil, nil, 0, 0, 0, &domain.BusinessError{Code: "evidence_required", Message: "异常检查项必须关联证据", Field: "checklist_results.evidence_refs", Details: map[string]any{"checklist_code": item.Code}}
		}
		for _, ref := range itemEvidence {
			ref = strings.TrimSpace(ref)
			if !validEvidence(ref) {
				return nil, nil, nil, 0, 0, 0, invalid("证据引用格式无效", "checklist_results.evidence_refs")
			}
			if explicit && evidenceSeen[ref] {
				return nil, nil, nil, 0, 0, 0, invalid("同次巡检不能重复使用证据引用", "checklist_results.evidence_refs")
			}
			if !evidenceSeen[ref] {
				evidenceSeen[ref] = true
				allEvidence = append(allEvidence, ref)
			}
		}
		results = append(results, domain.InspectionResult{Code: item.Code, Conclusion: conclusion, Observation: observation, Measurements: normalizedMeasurements, EvidenceRefs: append([]string(nil), itemEvidence...)})
	}
	anomalies := make([]string, 0, len(anomalySet))
	for code := range anomalySet {
		anomalies = append(anomalies, code)
	}
	sort.Strings(anomalies)
	return results, anomalies, allEvidence, temperature, humidity, illuminance, nil
}

func (s *Service) Record(taskID string, in Input) (domain.InspectionEntry, bool, error) {
	t, err := s.Tasks.Get(taskID)
	if err != nil {
		return domain.InspectionEntry{}, false, err
	}
	in.InspectorID = strings.TrimSpace(in.InspectorID)
	if in.InspectorID == "" {
		return domain.InspectionEntry{}, false, invalid("inspector_id不能为空", "inspector_id")
	}
	if strings.TrimSpace(in.IdempotencyKey) == "" {
		return domain.InspectionEntry{}, false, invalid("缺少Idempotency-Key", "Idempotency-Key")
	}
	if in.SupersedesInspectionID != "" && strings.TrimSpace(in.CorrectionReason) == "" {
		return domain.InspectionEntry{}, false, invalid("更正巡检必须填写correction_reason", "correction_reason")
	}
	recordedAt := s.Tasks.Now
	now := time.Now().UTC()
	if recordedAt != nil {
		now = recordedAt().UTC()
	}
	rt := now
	if in.RecordedAt != "" {
		rt, err = time.Parse(time.RFC3339, in.RecordedAt)
		if err != nil {
			return domain.InspectionEntry{}, false, invalid("recorded_at格式应为RFC3339", "recorded_at")
		}
		rt = rt.UTC()
	}
	if rt.Before(t.WindowStart.Add(-EarlyTolerance)) {
		return domain.InspectionEntry{}, false, invalid("recorded_at早于计划窗口允许偏差", "recorded_at")
	}
	if rt.After(now.Add(ClockTolerance)) {
		return domain.InspectionEntry{}, false, invalid("recorded_at晚于当前时间允许偏差", "recorded_at")
	}
	if rt.After(t.WindowEnd.Add(LateTolerance)) {
		return domain.InspectionEntry{}, false, invalid("recorded_at晚于计划窗口允许偏差", "recorded_at")
	}
	results, anomalies, evidence, temperature, humidity, illuminance, err := s.normalize(t, in)
	if err != nil {
		return domain.InspectionEntry{}, false, err
	}
	summary := domain.InspectionSummary{CoveragePercent: 100, AnomalyCount: len(anomalies), AnomalyCodes: append([]string(nil), anomalies...), EvidenceCompleteness: 100}
	entry := domain.InspectionEntry{
		InspectionID: id(), TaskID: t.TaskID, InspectorID: in.InspectorID,
		Temperature: temperature, Humidity: humidity, Illuminance: illuminance,
		Observations: strings.TrimSpace(in.Observations), Results: results, Summary: summary,
		Anomalies: anomalies, EvidenceRefs: evidence, RecordedAt: rt, CreatedAt: now,
		SupersedesInspectionID: strings.TrimSpace(in.SupersedesInspectionID),
		CorrectionReason:       strings.TrimSpace(in.CorrectionReason), IdempotencyKey: in.IdempotencyKey,
	}
	expected := in.Revision
	if expected == 0 {
		expected = t.Revision
	}
	recordedAtFingerprint := ""
	if in.RecordedAt != "" {
		recordedAtFingerprint = rt.Format(time.RFC3339Nano)
	}
	fingerprint := storage.Digest(struct {
		TaskID      string                    `json:"task_id"`
		InspectorID string                    `json:"inspector_id"`
		RecordedAt  string                    `json:"recorded_at"`
		Results     []domain.InspectionResult `json:"results"`
		Supersedes  string                    `json:"supersedes"`
		Reason      string                    `json:"reason"`
	}{t.TaskID, entry.InspectorID, recordedAtFingerprint, entry.Results, entry.SupersedesInspectionID, entry.CorrectionReason})
	stored, reused, _, err := s.Store.RecordInspectionAtomic(entry, expected, in.IdempotencyKey, fingerprint)
	return stored, reused, err
}
