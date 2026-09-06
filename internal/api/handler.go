package api

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/runtime"
	"agent/internal/storage"
)

type Server struct {
	Tasks         TaskStore
	Events        EventSource
	Confirmations ConfirmationStore
	Runner        RuntimeRunner
	LeaderModelID string
	Now           func() time.Time
}

func NewServer(tasks TaskStore, events EventSource, confirmations ConfirmationStore, leaderModelID string) Server {
	return Server{
		Tasks:         tasks,
		Events:        events,
		Confirmations: confirmations,
		LeaderModelID: leaderModelID,
		Now:           func() time.Time { return time.Now().UTC() },
	}
}

func NewServerWithRunner(tasks TaskStore, events EventSource, confirmations ConfirmationStore, leaderModelID string, runner RuntimeRunner) Server {
	server := NewServer(tasks, events, confirmations, leaderModelID)
	server.Runner = runner
	return server
}

func (s Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tasks", s.createTask)
	mux.HandleFunc("GET /tasks", s.listTasks)
	mux.HandleFunc("GET /tasks/{id}", s.getTask)
	mux.HandleFunc("POST /tasks/{id}/cancel", s.cancelTask)
	mux.HandleFunc("GET /tasks/{id}/events", s.listEvents)
	mux.HandleFunc("GET /confirmations/{id}", s.getConfirmation)
	mux.HandleFunc("POST /confirmations/{id}/approve", s.approveConfirmation)
	mux.HandleFunc("POST /confirmations/{id}/deny", s.denyConfirmation)
	return mux
}

type createTaskRequest struct {
	Title        string `json:"title"`
	Input        string `json:"input"`
	LeaderModel  string `json:"leader_model_id"`
	PrivacyClass string `json:"privacy_class"`
	Long         bool   `json:"long"`
}

type taskResponse struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	LeaderModelID   string   `json:"leader_model_id"`
	PrivacyClass    string   `json:"privacy_class"`
	FinalAnswer     *string  `json:"final_answer,omitempty"`
	FinalConfidence *float64 `json:"final_confidence,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
	UpdatedAt       string   `json:"updated_at,omitempty"`
}

type eventResponse struct {
	Type          string   `json:"type"`
	TaskID        string   `json:"task_id"`
	NodeID        string   `json:"node_id,omitempty"`
	Role          string   `json:"role,omitempty"`
	Attempt       int      `json:"attempt,omitempty"`
	ErrorCategory string   `json:"error_category,omitempty"`
	ErrorMessage  string   `json:"error_message,omitempty"`
	EvidenceIDs   []string `json:"evidence_ids,omitempty"`
	RecordedAt    string   `json:"recorded_at,omitempty"`
}

type confirmationResponse struct {
	ID             string   `json:"id"`
	TaskID         string   `json:"task_id"`
	NodeID         string   `json:"node_id,omitempty"`
	Action         string   `json:"action"`
	Target         string   `json:"target"`
	Risk           string   `json:"risk"`
	EvidenceIDs    []string `json:"evidence_ids,omitempty"`
	ProposedEffect string   `json:"proposed_effect"`
	Status         string   `json:"status"`
	RequestedAt    string   `json:"requested_at,omitempty"`
}

func (s Server) createTask(w http.ResponseWriter, r *http.Request) {
	var request createTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	request.Input = strings.TrimSpace(request.Input)
	request.Title = strings.TrimSpace(request.Title)
	if request.Input == "" {
		writeError(w, http.StatusBadRequest, "input_required")
		return
	}
	if request.Title == "" {
		request.Title = summarizeTitle(request.Input)
	}
	modelID := request.LeaderModel
	if modelID == "" {
		modelID = s.LeaderModelID
	}
	if modelID == "" {
		modelID = "local"
	}
	privacyClass := request.PrivacyClass
	if privacyClass == "" {
		privacyClass = "local_private"
	}
	id, err := newID("task")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "id_generation_failed")
		return
	}
	status := "running"
	now := s.now()
	if !request.Long && s.Runner == nil {
		writeError(w, http.StatusServiceUnavailable, "runtime_unavailable")
		return
	}
	task := storage.Task{
		ID:            id,
		Title:         sanitizeForAPI(request.Title),
		Input:         request.Input,
		Status:        status,
		CreatedAt:     now,
		UpdatedAt:     now,
		LeaderModelID: modelID,
		PrivacyClass:  privacyClass,
	}
	if err := s.Tasks.CreateTask(r.Context(), task); err != nil {
		writeError(w, http.StatusInternalServerError, "task_create_failed")
		return
	}
	if !request.Long {
		if _, err := s.Runner.Run(r.Context(), runtime.RunRequest{
			TaskID:        id,
			Input:         request.Input,
			LeaderModelID: modelID,
			PrivacyClass:  privacyClass,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, "runtime_failed")
			return
		}
		updated, err := s.Tasks.GetTask(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "task_get_failed")
			return
		}
		writeJSON(w, http.StatusCreated, taskToResponse(updated))
		return
	}
	writeJSON(w, http.StatusCreated, taskToResponse(task))
}

func (s Server) listTasks(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit")
			return
		}
		limit = parsed
	}
	tasks, err := s.Tasks.ListTasks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "task_list_failed")
		return
	}
	responses := make([]taskResponse, 0, len(tasks))
	for _, task := range tasks {
		responses = append(responses, taskToResponse(task))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": responses})
}

func (s Server) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := s.Tasks.GetTask(r.Context(), r.PathValue("id"))
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "task_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "task_get_failed")
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(task))
}

func (s Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Tasks.CancelTask(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "task_cancel_failed")
		return
	}
	task, err := s.Tasks.GetTask(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "task_not_found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "task_get_failed")
		return
	}
	writeJSON(w, http.StatusOK, taskToResponse(task))
}

func (s Server) listEvents(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	var responses []eventResponse
	if s.Events != nil {
		for _, event := range s.Events.Events() {
			if event.TaskID != taskID {
				continue
			}
			responses = append(responses, eventToResponse(event))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": responses})
}

func (s Server) getConfirmation(w http.ResponseWriter, r *http.Request) {
	request, ok := s.Confirmations.RequestByID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "confirmation_not_found")
		return
	}
	status := "pending"
	if statuses, ok := s.Confirmations.(ConfirmationStatusStore); ok {
		got, err := statuses.Status(r.Context(), request.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "confirmation_status_failed")
			return
		}
		if got != "" {
			status = got
		}
	}
	writeJSON(w, http.StatusOK, confirmationToResponse(request, status))
}

func (s Server) approveConfirmation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.Confirmations.RequestByID(id); !ok {
		writeError(w, http.StatusNotFound, "confirmation_not_found")
		return
	}
	if err := s.Confirmations.Approve(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "confirmation_approve_failed")
		return
	}
	request, _ := s.Confirmations.RequestByID(id)
	writeJSON(w, http.StatusOK, confirmationToResponse(request, "approved"))
}

func (s Server) denyConfirmation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.Confirmations.RequestByID(id); !ok {
		writeError(w, http.StatusNotFound, "confirmation_not_found")
		return
	}
	if err := s.Confirmations.Deny(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "confirmation_deny_failed")
		return
	}
	request, _ := s.Confirmations.RequestByID(id)
	writeJSON(w, http.StatusOK, confirmationToResponse(request, "denied"))
}

func taskToResponse(task storage.Task) taskResponse {
	return taskResponse{
		ID:              task.ID,
		Title:           sanitizeForAPI(task.Title),
		Status:          task.Status,
		LeaderModelID:   task.LeaderModelID,
		PrivacyClass:    task.PrivacyClass,
		FinalAnswer:     sanitizePtr(task.FinalAnswer),
		FinalConfidence: task.FinalConfidence,
		CreatedAt:       formatTime(task.CreatedAt),
		UpdatedAt:       formatTime(task.UpdatedAt),
	}
}

func eventToResponse(event orchestrator.Event) eventResponse {
	return eventResponse{
		Type:          string(event.Type),
		TaskID:        event.TaskID,
		NodeID:        event.NodeID,
		Role:          string(event.Role),
		Attempt:       event.Attempt,
		ErrorCategory: string(event.ErrorCategory),
		ErrorMessage:  sanitizeForAPI(event.ErrorMessage),
		EvidenceIDs:   append([]string(nil), event.EvidenceIDs...),
		RecordedAt:    formatTime(event.RecordedAt),
	}
}

func confirmationToResponse(request permission.Request, status string) confirmationResponse {
	return confirmationResponse{
		ID:             request.ID,
		TaskID:         request.TaskID,
		NodeID:         request.NodeID,
		Action:         string(request.Action),
		Target:         sanitizeForAPI(request.Target),
		Risk:           string(request.Risk),
		EvidenceIDs:    append([]string(nil), request.EvidenceIDs...),
		ProposedEffect: sanitizeForAPI(request.ProposedEffect),
		Status:         status,
		RequestedAt:    formatTime(request.RequestedAt),
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func (s Server) now() time.Time {
	if s.Now == nil {
		return time.Now().UTC()
	}
	return s.Now().UTC()
}

func newID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}

func summarizeTitle(input string) string {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) <= 80 {
		return trimmed
	}
	return trimmed[:80]
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func sanitizePtr(value *string) *string {
	if value == nil {
		return nil
	}
	sanitized := sanitizeForAPI(*value)
	return &sanitized
}

func sanitizeForAPI(value string) string {
	return redactForAPI(value)
}
