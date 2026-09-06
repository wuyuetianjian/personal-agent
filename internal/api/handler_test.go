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

	"agent/internal/orchestrator"
	"agent/internal/permission"
	"agent/internal/storage"
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
