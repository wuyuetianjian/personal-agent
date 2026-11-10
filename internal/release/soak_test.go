package release

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"agent/internal/config"
	"agent/internal/storage"
)

func TestRunSoakOfflineProducesPassingReport(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "soak-report.json")
	cfg := minimalSoakConfig(filepath.Join(dir, "soak.db"))
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	report, err := RunSoak(context.Background(), SoakOptions{
		Config:     cfg,
		Duration:   time.Nanosecond,
		Interval:   time.Nanosecond,
		Offline:    true,
		OutputPath: output,
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	})
	if err != nil {
		t.Fatalf("RunSoak() error = %v, report = %+v", err, report)
	}
	if report.Status != "pass" {
		t.Fatalf("RunSoak() status = %q, want pass; failures=%v", report.Status, report.Failures)
	}
	if report.Iterations == 0 {
		t.Fatal("RunSoak() did not execute any workload iterations")
	}
	if report.NonTerminalWorkflows != 0 {
		t.Fatalf("non-terminal workflows = %d, want 0", report.NonTerminalWorkflows)
	}
	if report.DuplicateNotifications != 0 {
		t.Fatalf("duplicate notifications = %d, want 0", report.DuplicateNotifications)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("soak report was not written: %v", err)
	}
	if len(report.Samples) != report.Iterations+2 || report.Samples[len(report.Samples)-1].Phase != "settled" {
		t.Fatalf("expected baseline, every raw workload sample, and final settled sample: %+v", report.Samples)
	}
	last := report.Samples[len(report.Samples)-1]
	if report.CompletedAt.Before(last.At) || last.TaskCount != int64(report.Iterations) || report.NotificationCount != last.NotificationCount {
		t.Fatalf("inconsistent settled report timing or counts: %+v", report)
	}
}

func TestRunSoakOfflineCanRunRepeatedlyOnSameDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalSoakConfig(filepath.Join(dir, "soak.db"))
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	nextNow := func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	for i := 0; i < 2; i++ {
		report, err := RunSoak(context.Background(), SoakOptions{
			Config:   cfg,
			Duration: time.Nanosecond,
			Interval: time.Nanosecond,
			Offline:  true,
			Now:      nextNow,
		})
		if err != nil {
			t.Fatalf("RunSoak() iteration %d error = %v, report = %+v", i, err, report)
		}
		if report.Status != "pass" {
			t.Fatalf("RunSoak() iteration %d status = %q, failures=%v", i, report.Status, report.Failures)
		}
	}
}

func minimalSoakConfig(dbPath string) config.Config {
	enabled := false
	return config.Config{
		ConfigVersion: 1,
		App:           config.AppConfig{Name: "personal-agent", Environment: "test", DataDir: filepath.Dir(dbPath)},
		Storage: config.StorageConfig{
			Driver: "sqlite",
			SQLite: config.SQLiteConfig{Path: dbPath},
		},
		Agent: config.AgentConfig{
			Leader:    config.RoleModelConfig{},
			Planner:   config.PlannerConfig{Enabled: &enabled, MaxNodes: 8},
			SubAgents: map[string]config.RoleModelConfig{},
		},
		MCP:          config.MCPConfig{Servers: map[string]config.MCPServerConfig{}},
		CodingAgents: config.CodingAgentsConfig{Backends: map[string]config.CodingAgentBackendConfig{}},
		Proactive:    config.ProactiveConfig{Enabled: &enabled},
	}
}

// Synctest controls cleanup/deadline time, so scheduler stalls cannot masquerade
// as a >100ms cleanup window. Each cohort contains 32 real blocked goroutines;
// global baseline noise from other tests is not assumed to be exactly zero.
// This catches gating on the immediate workload sample, which includes goroutines
// whose canceled database work has finished but whose cleanup has not yet run.
func TestSoakSettlementPreservesTransientSpikeAndAppendsSettledSample(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		report, err, _ := sampleSoakWithCleanupGoroutines(t, 20*time.Millisecond, nil)
		if err != nil {
			t.Fatalf("transient cleanup failed soak gate: %v; growth=%d failures=%v", err, report.GoroutineGrowth, report.Failures)
		}
		if len(report.Samples) != 3 {
			t.Fatalf("samples = %d, want baseline, raw workload, and settled samples", len(report.Samples))
		}
		first, raw, settled := report.Samples[0], report.Samples[1], report.Samples[2]
		if raw.Goroutines < 32 {
			t.Fatalf("raw spike was lost: baseline=%d raw=%d", first.Goroutines, raw.Goroutines)
		}
		if report.GoroutineGrowth != settled.Goroutines-first.Goroutines || report.GoroutineGrowth > 16 {
			t.Fatalf("gate did not use settled sample: growth=%d baseline=%d settled=%d", report.GoroutineGrowth, first.Goroutines, settled.Goroutines)
		}
		payload, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Samples []struct {
				Phase string `json:"phase"`
			} `json:"samples"`
		}
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Samples[2].Phase != "settled" {
			t.Fatalf("final sample phase = %q, want settled", decoded.Samples[2].Phase)
		}
		if raw.TaskCount != 1 || settled.TaskCount != 1 || settled.WorkflowCount != 1 || report.NotificationCount != 1 {
			t.Fatalf("settling changed workload counts: report=%+v", report)
		}
		if settled.At.Before(raw.At) || report.CompletedAt.Before(settled.At) {
			t.Fatalf("inconsistent report times: raw=%v settled=%v completed=%v", raw.At, settled.At, report.CompletedAt)
		}
	})
}

// This catches suppressing raw spikes by ignoring genuine retained goroutines,
// or waiting indefinitely for a leak that cannot clean itself up.
func TestSoakSettlementSustainedGoroutineLeakFailsWithinBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		report, err, elapsed := sampleSoakWithCleanupGoroutines(t, 0, nil)
		if err == nil || report.Status != "fail" || report.GoroutineGrowth <= 16 {
			t.Fatalf("sustained 32-goroutine leak passed: error=%v growth=%d status=%s", err, report.GoroutineGrowth, report.Status)
		}
		if !strings.Contains(strings.Join(report.Failures, ";"), "goroutine growth") {
			t.Fatalf("missing goroutine gate failure: %v", report.Failures)
		}
		if elapsed != 100*time.Millisecond {
			t.Fatalf("settlement elapsed = %v, want bounded 100ms grace period", elapsed)
		}
		if len(report.Samples) != 3 {
			t.Fatalf("samples = %d, want baseline, raw workload, and settled samples", len(report.Samples))
		}
	})
}

// This catches a fixed sleep or a polling loop that ignores cancellation.
func TestSoakSettlementRespondsToCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		report, err, elapsed := sampleSoakWithCleanupGoroutines(t, 0, func() { time.AfterFunc(10*time.Millisecond, cancel) }, ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled; report=%+v", err, report)
		}
		if elapsed != 10*time.Millisecond {
			t.Fatalf("canceled settlement took %v", elapsed)
		}
		if len(report.Samples) != 2 {
			t.Fatalf("cancellation lost raw sample evidence: samples=%d", len(report.Samples))
		}
	})
}

// Exercise the real sampler and terminal gates without Runtime.Build/Close:
// their own asynchronous cleanup must not pollute a controlled leak's baseline
// or the settlement-only timing bound. Full RunSoak wiring is covered above.
func sampleSoakWithCleanupGoroutines(t *testing.T, cleanupAfter time.Duration, onStart func(), contexts ...context.Context) (SoakReport, error, time.Duration) {
	t.Helper()
	ctx := context.Background()
	if len(contexts) != 0 {
		ctx = contexts[0]
	}
	path := filepath.Join(t.TempDir(), "soak.db")
	db, err := storage.OpenSQLite(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// These are the complete schema/rows used by the sampler's read-only
	// queries. Avoid unrelated migration transactions' own async cleanup.
	if _, err := db.SQL.ExecContext(context.Background(), `
CREATE TABLE tasks (id TEXT);
CREATE TABLE workflow_runs (status TEXT);
CREATE TABLE notification_inbox (dedup_key TEXT);
INSERT INTO tasks VALUES ('sample-task');
INSERT INTO workflow_runs VALUES ('completed');
INSERT INTO notification_inbox VALUES ('sample-note');
`); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var releaseOnce sync.Once
	cleanup := func() { releaseOnce.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	t.Cleanup(func() { cleanup(); workers.Wait() })
	report := SoakReport{Samples: []SoakSample{collectSoakSample(context.Background(), db.SQL, path, time.Now)}}
	ready := make(chan struct{}, 32)
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			ready <- struct{}{}
			<-release
		}()
	}
	for range 32 {
		<-ready
	}
	report.Samples = append(report.Samples, collectSoakSample(context.Background(), db.SQL, path, time.Now))
	if cleanupAfter > 0 {
		time.AfterFunc(cleanupAfter, cleanup)
	}
	if onStart != nil {
		onStart()
	}
	started := time.Now()
	settled, err := collectSettledSoakSample(ctx, db.SQL, path, report.Samples[0].Goroutines, time.Now)
	elapsed := time.Since(started)
	if err != nil {
		return report, err, elapsed
	}
	report.Samples = append(report.Samples, settled)
	report.CompletedAt = time.Now()
	report.finish(context.Background(), db.SQL, minimalSoakConfig(path), 0, 0)
	report.applyThresholds(SoakOptions{MaxGoroutineGrowth: 16, MaxHeapGrowthBytes: 64 * 1024 * 1024, MaxSQLiteGrowthBytes: 64 * 1024 * 1024})
	if report.Status == "fail" {
		err = errors.New("soak gate failed")
	}
	return report, err, elapsed
}
