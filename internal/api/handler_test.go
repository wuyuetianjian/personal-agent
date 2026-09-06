package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agent/internal/config"
	agentEvent "agent/internal/event"
	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/trigger"
)

func TestTaskLifecycleEndpoints(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	bus := &orchestrator.InMemoryEvidenceBus{}
	confirmations := permission.NewInMemoryConfirmationStore()
	server := NewServer(db, bus, confirmations, "local-planner")
	handler := server.Handler()

	createBody := `{"input":"summarize local notes password=supersecret","long":true}`
	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(createBody)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	if strings.Contains(create.Body.String(), "supersecret") || strings.Contains(create.Body.String(), "password=") {
		t.Fatalf("create response leaked secret: %s", create.Body.String())
	}
	var created taskResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Status != "running" {
		t.Fatalf("created task = %+v, want running task with id", created)
	}

	if err := bus.Publish(ctx, orchestrator.Event{
		Type:         orchestrator.EventNodeStarted,
		TaskID:       created.ID,
		NodeID:       "node-1",
		ErrorMessage: "token=hidden",
		RecordedAt:   time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}

	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK {
		t.Fatalf("events status = %d body=%s", events.Code, events.Body.String())
	}
	if !strings.Contains(events.Body.String(), string(orchestrator.EventNodeStarted)) {
		t.Fatalf("events response missing event: %s", events.Body.String())
	}
	if strings.Contains(events.Body.String(), "hidden") || strings.Contains(events.Body.String(), "token=") {
		t.Fatalf("events response leaked secret: %s", events.Body.String())
	}

	cancel := httptest.NewRecorder()
	handler.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/tasks/"+created.ID+"/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	if !strings.Contains(cancel.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel response = %s, want cancelled", cancel.Body.String())
	}

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/tasks?limit=10", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), created.ID) {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
}

func TestCreateTaskRunsRuntime(t *testing.T) {
	db := openTestDB(t)
	bus := &orchestrator.InMemoryEvidenceBus{}
	runner := runtime.NewLocal(configForAPITest(), db, bus)
	handler := NewServerWithRunner(db, bus, permission.NewInMemoryConfirmationStore(), "local-planner", runner).Handler()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"input":"summarize local runtime notes"}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created taskResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if created.Status != "completed" || created.FinalAnswer == nil {
		t.Fatalf("created task = %+v, want completed runtime result", created)
	}
	if strings.Contains(*created.FinalAnswer, "No-op task completed.") {
		t.Fatalf("final answer still no-op: %q", *created.FinalAnswer)
	}
	if len(bus.Events()) == 0 {
		t.Fatal("runtime did not publish API-visible events")
	}
	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/tasks/"+created.ID+"/events", nil))
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"events"`) {
		t.Fatalf("events status=%d body=%s", events.Code, events.Body.String())
	}
}

func TestCancelTaskCancelsBackgroundRuntime(t *testing.T) {
	db := openTestDB(t)
	bus := &orchestrator.InMemoryEvidenceBus{}
	runner := &blockingRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
	server := NewServerWithRunner(db, bus, permission.NewInMemoryConfirmationStore(), "local-planner", runner)
	handler := server.Handler()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(`{"input":"long running runtime","long":true}`)))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created taskResponse
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !runner.waitStarted(time.Second) {
		t.Fatal("background runner did not start")
	}

	cancel := httptest.NewRecorder()
	handler.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/tasks/"+created.ID+"/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", cancel.Code, cancel.Body.String())
	}
	select {
	case <-runner.cancelled:
	case <-time.After(time.Second):
		t.Fatal("background runtime context was not cancelled")
	}
}

func TestConfirmationEndpoints(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	confirmations := permission.NewInMemoryConfirmationStore()
	request := permission.Request{
		ID:             "confirm-1",
		TaskID:         "task-1",
		Action:         permission.ActionBrowserHighRisk,
		Target:         "https://example.test/delete?token=secret",
		Risk:           permission.RiskCritical,
		EvidenceIDs:    []string{"ev-1"},
		ProposedEffect: "delete account with password=supersecret",
		RequestedAt:    time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC),
	}
	if _, err := confirmations.Request(ctx, request); err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	handler := NewServer(db, nil, confirmations, "local-planner").Handler()

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/confirmations/confirm-1", nil))
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	assertRedacted(t, get.Body)
	if !strings.Contains(get.Body.String(), `"status":"pending"`) {
		t.Fatalf("confirmation response = %s, want pending", get.Body.String())
	}

	approve := httptest.NewRecorder()
	handler.ServeHTTP(approve, httptest.NewRequest(http.MethodPost, "/confirmations/confirm-1/approve", nil))
	if approve.Code != http.StatusOK || !strings.Contains(approve.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status=%d body=%s", approve.Code, approve.Body.String())
	}
	approved, err := confirmations.IsApproved(ctx, "confirm-1")
	if err != nil || !approved {
		t.Fatalf("IsApproved() = %v, %v; want true", approved, err)
	}

	deny := httptest.NewRecorder()
	handler.ServeHTTP(deny, httptest.NewRequest(http.MethodPost, "/confirmations/confirm-1/deny", nil))
	if deny.Code != http.StatusOK || !strings.Contains(deny.Body.String(), `"status":"denied"`) {
		t.Fatalf("deny status=%d body=%s", deny.Code, deny.Body.String())
	}
	approved, err = confirmations.IsApproved(ctx, "confirm-1")
	if err != nil || approved {
		t.Fatalf("IsApproved() after deny = %v, %v; want false", approved, err)
	}
}

func TestTriggerAndEventEndpoints(t *testing.T) {
	db := openTestDB(t)
	server := NewServer(db, nil, permission.NewInMemoryConfirmationStore(), "local-planner")
	server.Triggers = trigger.Store{DB: db.SQL}
	server.EventStore = agentEvent.Store{DB: db.SQL}
	handler := server.Handler()

	createTrigger := httptest.NewRecorder()
	handler.ServeHTTP(createTrigger, httptest.NewRequest(http.MethodPost, "/triggers", strings.NewReader(`{"id":"tr-api","project_id":"default","type":"manual","enabled":true}`)))
	if createTrigger.Code != http.StatusCreated {
		t.Fatalf("trigger create status = %d body=%s", createTrigger.Code, createTrigger.Body.String())
	}
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/triggers", nil))
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "tr-api") {
		t.Fatalf("trigger list status=%d body=%s", list.Code, list.Body.String())
	}
	run := httptest.NewRecorder()
	handler.ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/triggers/tr-api/run", nil))
	if run.Code != http.StatusOK {
		t.Fatalf("trigger run status=%d body=%s", run.Code, run.Body.String())
	}
	history := httptest.NewRecorder()
	handler.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/triggers/tr-api/history", nil))
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), "trigger fired") {
		t.Fatalf("history status=%d body=%s", history.Code, history.Body.String())
	}

	createEvent := httptest.NewRecorder()
	handler.ServeHTTP(createEvent, httptest.NewRequest(http.MethodPost, "/events", strings.NewReader(`{"id":"ev-api","source":"test","type":"push","project_id":"default","payload":{"token":"secret"}}`)))
	if createEvent.Code != http.StatusCreated {
		t.Fatalf("event create status=%d body=%s", createEvent.Code, createEvent.Body.String())
	}
}

func TestSecurityPolicyMiddleware(t *testing.T) {
	db := openTestDB(t)
	server := NewServer(db, nil, permission.NewInMemoryConfirmationStore(), "local-planner")
	now := time.Date(2026, 9, 6, 1, 2, 3, 0, time.UTC)
	server.Security = SecurityPolicy{
		AuthToken:          "test-token",
		AllowedOrigins:     []string{"http://127.0.0.1:8787"},
		MaxBodyBytes:       64,
		RateLimitPerMinute: 1,
		Now:                func() time.Time { return now },
	}
	handler := server.Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/tasks", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
	if unauthorized.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers missing: %+v", unauthorized.Header())
	}

	deniedOrigin := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "https://evil.test")
	req.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(deniedOrigin, req)
	if deniedOrigin.Code != http.StatusForbidden {
		t.Fatalf("denied origin status = %d", deniedOrigin.Code)
	}

	ok := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8787")
	req.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(ok, req)
	if ok.Code != http.StatusOK {
		t.Fatalf("authorized status = %d body=%s", ok.Code, ok.Body.String())
	}
	if ok.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:8787" {
		t.Fatalf("CORS header missing: %+v", ok.Header())
	}

	rateLimited := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/tasks", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	handler.ServeHTTP(rateLimited, req)
	if rateLimited.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited status = %d", rateLimited.Code)
	}
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.Migrate(context.Background(), db.SQL); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

func assertRedacted(t *testing.T, body *bytes.Buffer) {
	t.Helper()
	got := body.String()
	for _, forbidden := range []string{"supersecret", "password=", "token=secret"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, got)
		}
	}
}

func configForAPITest() config.Config {
	var cfg config.Config
	cfg.Agent.Leader.ModelID = "local-planner"
	return cfg
}

type blockingRunner struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, req runtime.RunRequest) (*runtime.RunResult, error) {
	close(r.started)
	<-ctx.Done()
	close(r.cancelled)
	return nil, ctx.Err()
}

func (r *blockingRunner) waitStarted(timeout time.Duration) bool {
	select {
	case <-r.started:
		return true
	case <-time.After(timeout):
		return false
	}
}
