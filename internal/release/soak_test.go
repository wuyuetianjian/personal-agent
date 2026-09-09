package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"agent/internal/config"
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
