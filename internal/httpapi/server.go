package httpapi

import (
	"encoding/json"
	"errors"
	"heritage-care/internal/action"
	"heritage-care/internal/domain"
	"heritage-care/internal/inspection"
	"heritage-care/internal/storage"
	"heritage-care/internal/task"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	Store       *storage.Store
	Tasks       *task.Service
	Inspections *inspection.Service
	Actions     *action.Service
}

func New(store *storage.Store) *Server {
	ts := &task.Service{Store: store}
	return &Server{
		Store: store, Tasks: ts,
		Inspections: &inspection.Service{Store: store, Tasks: ts},
		Actions:     &action.Service{Store: store, Tasks: ts},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/v1/artifacts", s.artifacts)
	mux.HandleFunc("/v1/conservation-tasks", s.tasks)
	mux.HandleFunc("/v1/conservation-tasks/", s.tasks)
	mux.HandleFunc("/v1/inspections/", s.inspections)
	mux.HandleFunc("/v1/actions/", s.actions)
	mux.HandleFunc("/v1/audits/", s.audit)
	return mux
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func errw(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	body := map[string]any{"error": err.Error(), "code": "bad_request"}
	var business *domain.BusinessError
	if errors.As(err, &business) {
		body["code"] = business.Code
		if business.Field != "" {
			body["field"] = business.Field
		}
		if len(business.Details) > 0 {
			body["details"] = business.Details
			for key, value := range business.Details {
				body[key] = value
			}
		}
		switch business.Code {
		case "not_found":
			status = http.StatusNotFound
		case "schedule_conflict", "idempotency_conflict", "revision_conflict", "state_conflict", "duplicate_coverage", "due_limit_exceeded", "integrity_error":
			status = http.StatusConflict
		}
	} else {
		if strings.Contains(err.Error(), "不存在") {
			status = http.StatusNotFound
		}
		if strings.Contains(err.Error(), "冲突") {
			status = http.StatusConflict
		}
	}
	write(w, status, body)
}

func decode(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("请求体为空")
	}
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(v); err != nil {
		return domain.NewError("invalid_json", "请求JSON格式无效", "")
	}
	return nil
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		write(w, http.StatusMethodNotAllowed, nil)
		return
	}
	write(w, http.StatusOK, map[string]string{"status": "ok", "service": "heritage-care"})
}

func (s *Server) artifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a, ok := s.Store.Artifact(r.URL.Query().Get("id"))
		if !ok {
			errw(w, domain.NewError("not_found", "文物档案不存在", "id"))
			return
		}
		write(w, http.StatusOK, a)
		return
	}
	if r.Method != http.MethodPost {
		write(w, http.StatusMethodNotAllowed, nil)
		return
	}
	var a domain.ArtifactRecord
	if err := decode(r, &a); err != nil {
		errw(w, err)
		return
	}
	a.ArtifactID, a.Title, a.Material = strings.TrimSpace(a.ArtifactID), strings.TrimSpace(a.Title), strings.TrimSpace(a.Material)
	if a.ArtifactID == "" {
		errw(w, domain.NewError("validation_error", "artifact_id不能为空", "artifact_id"))
		return
	}
	if a.Title == "" {
		errw(w, domain.NewError("validation_error", "title不能为空", "title"))
		return
	}
	if a.Material == "" {
		errw(w, domain.NewError("validation_error", "material不能为空", "material"))
		return
	}
	idem := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	now := time.Now().UTC()
	a.CreatedAt, a.UpdatedAt, a.CurrentRiskLevel = now, now, domain.RiskLow
	fingerprint := storage.Digest(struct {
		ArtifactID string `json:"artifact_id"`
		Title      string `json:"title"`
		Material   string `json:"material"`
	}{a.ArtifactID, a.Title, a.Material})
	stored, reused, err := s.Store.AddArtifactAtomic(a, idem, fingerprint)
	if err != nil {
		errw(w, err)
		return
	}
	status := http.StatusCreated
	if reused {
		status = http.StatusOK
	}
	write(w, status, stored)
}

func taskPathID(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "/v1/conservation-tasks/"), "/")
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/close") {
		id := strings.TrimSuffix(taskPathID(r.URL.Path), "/close")
		var in struct {
			Revision int    `json:"revision"`
			ActorID  string `json:"actor_id"`
		}
		if err := decode(r, &in); err != nil {
			errw(w, err)
			return
		}
		audit, err := s.Tasks.Close(id, in.ActorID, in.Revision)
		if err != nil {
			errw(w, err)
			return
		}
		write(w, http.StatusOK, audit)
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/v1/conservation-tasks" {
		var in task.CreateInput
		if err := decode(r, &in); err != nil {
			errw(w, err)
			return
		}
		in.IdempotencyKey = r.Header.Get("Idempotency-Key")
		created, reused, err := s.Tasks.Create(in)
		if err != nil {
			errw(w, err)
			return
		}
		status := http.StatusCreated
		if reused {
			status = http.StatusOK
		}
		write(w, status, created)
		return
	}
	if r.Method == http.MethodGet {
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" && r.URL.Path != "/v1/conservation-tasks" {
			id = taskPathID(r.URL.Path)
		}
		if id != "" {
			detail, err := s.Tasks.Detail(id)
			if err != nil {
				errw(w, err)
				return
			}
			write(w, http.StatusOK, detail)
			return
		}
		limit, err := task.ParseLimit(r.URL.Query().Get("limit"))
		if err != nil {
			errw(w, err)
			return
		}
		overdue, err := task.ParseOverdue(r.URL.Query().Get("overdue"))
		if err != nil {
			errw(w, err)
			return
		}
		result, err := s.Tasks.List(task.ListFilter{
			OwnerID: r.URL.Query().Get("owner_id"), Status: domain.TaskStatus(r.URL.Query().Get("status")),
			Overdue: overdue, ArtifactID: r.URL.Query().Get("artifact_id"), Limit: limit, Cursor: r.URL.Query().Get("cursor"),
		})
		if err != nil {
			errw(w, err)
			return
		}
		write(w, http.StatusOK, result)
		return
	}
	write(w, http.StatusMethodNotAllowed, nil)
}

func (s *Server) inspections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		write(w, http.StatusMethodNotAllowed, nil)
		return
	}
	var in inspection.Input
	if err := decode(r, &in); err != nil {
		errw(w, err)
		return
	}
	in.IdempotencyKey = r.Header.Get("Idempotency-Key")
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/inspections/"), "/")
	if id == "" {
		id = r.URL.Query().Get("task_id")
	}
	entry, reused, err := s.Inspections.Record(id, in)
	if err != nil {
		errw(w, err)
		return
	}
	status := http.StatusCreated
	if reused {
		status = http.StatusOK
	}
	write(w, status, entry)
}

func actionPathID(path string) string {
	return strings.Trim(strings.TrimPrefix(path, "/v1/actions/"), "/")
}

func (s *Server) actions(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/review") {
		id := strings.TrimSuffix(actionPathID(r.URL.Path), "/review")
		var in action.ReviewInput
		if err := decode(r, &in); err != nil {
			errw(w, err)
			return
		}
		value, err := s.Actions.Review(id, in)
		if err != nil {
			errw(w, err)
			return
		}
		write(w, http.StatusOK, value)
		return
	}
	if r.Method == http.MethodPost {
		id := actionPathID(r.URL.Path)
		if _, isAction := s.Store.GetAction(id); isAction {
			var in action.SubmitInput
			if err := decode(r, &in); err != nil {
				errw(w, err)
				return
			}
			in.IdempotencyKey = r.Header.Get("Idempotency-Key")
			value, reused, err := s.Actions.Submit(id, in)
			if err != nil {
				errw(w, err)
				return
			}
			status := http.StatusCreated
			if reused {
				status = http.StatusOK
			}
			write(w, status, value)
			return
		}
		var in action.CreateInput
		if err := decode(r, &in); err != nil {
			errw(w, err)
			return
		}
		in.IdempotencyKey = r.Header.Get("Idempotency-Key")
		value, reused, err := s.Actions.Recommend(id, in)
		if err != nil {
			errw(w, err)
			return
		}
		status := http.StatusCreated
		if reused {
			status = http.StatusOK
		}
		write(w, status, value)
		return
	}
	write(w, http.StatusMethodNotAllowed, nil)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		write(w, http.StatusMethodNotAllowed, nil)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/audits/"), "/")
	audit, ok := s.Store.VerifyAudit(id)
	if !ok {
		errw(w, domain.NewError("not_found", "审计摘要不存在", "task_id"))
		return
	}
	if audit.VerificationStatus != "passed" {
		write(w, http.StatusConflict, map[string]any{
			"error": "审计完整性校验失败", "code": "audit_verification_failed",
			"audit": audit, "verification_status": audit.VerificationStatus,
			"failure_sequence": audit.FailureSequence, "failure_reason": audit.FailureReason,
		})
		return
	}
	write(w, http.StatusOK, audit)
}
