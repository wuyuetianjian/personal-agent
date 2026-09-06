package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agent/internal/storage"
)

func TestLoggerRedactsStructuredFields(t *testing.T) {
	var out bytes.Buffer
	logger := NewLogger(&out)
	err := logger.Log("Authorization: Bearer abc.def", LogFields{
		TaskID:        "task-1 token=secret123",
		WorkflowID:    "workflow-1",
		NodeID:        "node-1",
		Agent:         "leader",
		Capability:    "rag.search",
		Provider:      "local password=hunter2",
		Duration:      15 * time.Millisecond,
		Status:        "failed",
		ErrorCategory: "api_key=raw",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, leaked := range []string{"abc.def", "secret123", "hunter2", "api_key=raw"} {
		if strings.Contains(got, leaked) {
			t.Fatalf("log leaked %q: %s", leaked, got)
		}
	}
	var record map[string]any
	if err := json.Unmarshal(out.Bytes(), &record); err != nil {
		t.Fatalf("invalid log json: %v", err)
	}
	for _, field := range []string{"task_id", "workflow_id", "node_id", "agent", "capability", "provider", "duration_ms", "status", "error_category"} {
		if _, ok := record[field]; !ok {
			t.Fatalf("log field %s missing from %#v", field, record)
		}
	}
}

func TestMetricsSnapshotRedactsNamesAndCountsRuntimeRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir+"/agent.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateTask(ctx, storage.Task{ID: "task-1", Title: "title", Input: "input", Status: "running", LeaderModelID: "local"}); err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry()
	registry.Inc("model_calls_token=secret123", 1)
	snapshot := registry.Snapshot(ctx, db.SQL)
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret123") {
		t.Fatalf("metrics leaked secret: %s", encoded)
	}
	runtimeMetrics := snapshot["runtime"].(map[string]int64)
	if runtimeMetrics["task_count"] != 1 || runtimeMetrics["queue_depth"] != 1 {
		t.Fatalf("runtime metrics = %#v", runtimeMetrics)
	}
}

func TestReadinessRedactsDependencyRemediation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.OpenSQLite(ctx, dir+"/agent.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	report := Readiness(ctx, db.SQL, true, []ComponentStatus{{
		Name:        "model_provider.local",
		Status:      "WARN",
		Remediation: "token=secret123",
	}})
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Ready {
		t.Fatalf("readiness = %#v, want ready despite dependency warning", report)
	}
	if strings.Contains(string(encoded), "secret123") {
		t.Fatalf("readiness leaked secret: %s", encoded)
	}
}
