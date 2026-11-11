package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunPerformanceBaselineOfflineProducesReport(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "performance.json")
	report, err := RunPerformanceBaseline(context.Background(), PerformanceOptions{
		Config:      minimalSoakConfig(filepath.Join(dir, "performance.db")),
		Offline:     true,
		OutputPath:  output,
		Concurrency: 2,
		// Exercise report generation independently of host speed. Threshold policy
		// is verified deterministically below.
		MaxStartupLatency:          time.Hour,
		MaxLocalQueryLatency:       time.Hour,
		MaxHybridRAGLatency:        time.Hour,
		MaxWorkflowDispatchLatency: time.Hour,
	})
	if err != nil {
		t.Fatalf("RunPerformanceBaseline() error = %v, report = %+v", err, report)
	}
	if report.Status != "pass" {
		t.Fatalf("status = %q, failures=%v", report.Status, report.Failures)
	}
	if report.StartupLatencyMS < 0 || report.LocalQueryLatencyMS < 0 || report.HybridRAGLatencyMS < 0 || report.WorkflowDispatchLatencyMS < 0 {
		t.Fatalf("latencies were not populated: %+v", report)
	}
	if report.StartedAt.IsZero() || report.CompletedAt.Before(report.StartedAt) {
		t.Fatalf("invalid measurement interval: %+v", report)
	}
	if report.ConcurrentWorkflowsCompleted != 2 {
		t.Fatalf("completed concurrent workflows = %d, want 2", report.ConcurrentWorkflowsCompleted)
	}
	if report.HeapAllocBytes == 0 {
		t.Fatal("heap allocation was not recorded")
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("performance report not written: %v", err)
	}
}

func TestPerformanceThresholdPolicy(t *testing.T) {
	opts := normalizePerformanceOptions(PerformanceOptions{})
	if opts.MaxStartupLatency != 2*time.Second || opts.MaxLocalQueryLatency != 100*time.Millisecond || opts.MaxHybridRAGLatency != 250*time.Millisecond || opts.MaxWorkflowDispatchLatency != 250*time.Millisecond {
		t.Fatalf("default thresholds changed: %+v", opts)
	}
	for _, tc := range []struct {
		name     string
		report   PerformanceReport
		failures int
	}{
		{"zero duration is a valid clock reading", PerformanceReport{}, 0},
		{"at limits", PerformanceReport{StartupLatencyMS: 2000, LocalQueryLatencyMS: 100, HybridRAGLatencyMS: 250, WorkflowDispatchLatencyMS: 250}, 0},
		{"above limits", PerformanceReport{StartupLatencyMS: 2001, LocalQueryLatencyMS: 101, HybridRAGLatencyMS: 251, WorkflowDispatchLatencyMS: 251}, 4},
		{"incomplete workflows", PerformanceReport{ConcurrentWorkflowsRequested: 2, ConcurrentWorkflowsCompleted: 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.report.applyPerformanceThresholds(opts)
			if len(tc.report.Failures) != tc.failures {
				t.Fatalf("report=%+v, want %d failures", tc.report, tc.failures)
			}
			want := "pass"
			if tc.failures > 0 {
				want = "fail"
			}
			if tc.report.Status != want {
				t.Fatalf("status=%s want=%s", tc.report.Status, want)
			}
		})
	}
}
