package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPerformanceBaselineOfflineProducesReport(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "performance.json")
	report, err := RunPerformanceBaseline(context.Background(), PerformanceOptions{
		Config:      minimalSoakConfig(filepath.Join(dir, "performance.db")),
		Offline:     true,
		OutputPath:  output,
		Concurrency: 2,
	})
	if err != nil {
		t.Fatalf("RunPerformanceBaseline() error = %v, report = %+v", err, report)
	}
	if report.Status != "pass" {
		t.Fatalf("status = %q, failures=%v", report.Status, report.Failures)
	}
	if report.StartupLatencyMS <= 0 || report.LocalQueryLatencyMS <= 0 || report.HybridRAGLatencyMS <= 0 || report.WorkflowDispatchLatencyMS <= 0 {
		t.Fatalf("latencies were not populated: %+v", report)
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
