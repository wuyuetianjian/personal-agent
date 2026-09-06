package reliability

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"agent/internal/config"
)

func TestGovernorEnforcesLimitsAndReleases(t *testing.T) {
	g := NewGovernor(config.ResourceLimitsConfig{MaxConcurrentWorkflows: 1})
	release, err := g.Acquire(ResourceWorkflow, 1)
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if _, err := g.Acquire(ResourceWorkflow, 1); !errors.Is(err, ErrResourceLimitExceeded) {
		t.Fatalf("Acquire() over limit = %v, want ErrResourceLimitExceeded", err)
	}
	release()
	if _, err := g.Acquire(ResourceWorkflow, 1); err != nil {
		t.Fatalf("Acquire() after release error = %v", err)
	}
}

func TestDiskPressureReportsSoftAndHard(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blob"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := CheckDiskPressure(config.DiskPressureConfig{Paths: []string{dir}, SoftLimitBytes: 4, HardLimitBytes: 10}).Status; got != DiskSoft {
		t.Fatalf("status = %s, want soft", got)
	}
	if got := CheckDiskPressure(config.DiskPressureConfig{Paths: []string{dir}, SoftLimitBytes: 4, HardLimitBytes: 5}).Status; got != DiskHard {
		t.Fatalf("status = %s, want hard", got)
	}
}

func TestShutdownCoordinatorRunsLIFOAndStopsAccepting(t *testing.T) {
	coordinator := NewShutdownCoordinator()
	var order []string
	coordinator.Add(func(context.Context) error {
		order = append(order, "first")
		return nil
	})
	coordinator.Add(func(context.Context) error {
		order = append(order, "second")
		return nil
	})
	if !coordinator.Accepting() {
		t.Fatal("coordinator should accept before shutdown")
	}
	if errs := coordinator.Shutdown(context.Background()); len(errs) != 0 {
		t.Fatalf("Shutdown() errors = %v", errs)
	}
	if coordinator.Accepting() {
		t.Fatal("coordinator should stop accepting after shutdown")
	}
	if !reflect.DeepEqual(order, []string{"second", "first"}) {
		t.Fatalf("order = %v, want LIFO", order)
	}
}
