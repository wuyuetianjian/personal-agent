package observability

import (
	"context"
	"database/sql"
	"time"
)

type ComponentStatus struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Remediation string `json:"remediation,omitempty"`
}

type HealthReport struct {
	Status    string `json:"status"`
	CheckedAt string `json:"checked_at"`
}

type ReadinessReport struct {
	Status       string            `json:"status"`
	Ready        bool              `json:"ready"`
	Checks       []ComponentStatus `json:"checks"`
	Dependencies []ComponentStatus `json:"dependencies"`
	CheckedAt    string            `json:"checked_at"`
}

func Health() HealthReport {
	return HealthReport{
		Status:    "ok",
		CheckedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func Readiness(ctx context.Context, db *sql.DB, runtimeInitialized bool, dependencies []ComponentStatus) ReadinessReport {
	checks := []ComponentStatus{}
	ready := true
	if db == nil {
		checks = append(checks, ComponentStatus{Name: "database", Status: "FAIL", Remediation: "database is not initialized"})
		ready = false
	} else if err := db.PingContext(ctx); err != nil {
		checks = append(checks, ComponentStatus{Name: "database", Status: "FAIL", Remediation: Redact(err.Error())})
		ready = false
	} else {
		checks = append(checks, ComponentStatus{Name: "database", Status: "OK"})
	}
	if db == nil {
		checks = append(checks, ComponentStatus{Name: "migrations", Status: "FAIL", Remediation: "database is not initialized"})
		ready = false
	} else if migrationsReady(ctx, db) {
		checks = append(checks, ComponentStatus{Name: "migrations", Status: "OK"})
	} else {
		checks = append(checks, ComponentStatus{Name: "migrations", Status: "FAIL", Remediation: "schema_migrations table has no applied versions"})
		ready = false
	}
	if runtimeInitialized {
		checks = append(checks, ComponentStatus{Name: "runtime", Status: "OK"})
	} else {
		checks = append(checks, ComponentStatus{Name: "runtime", Status: "FAIL", Remediation: "runtime is not initialized"})
		ready = false
	}
	return ReadinessReport{
		Status:       statusText(ready),
		Ready:        ready,
		Checks:       sanitizeComponents(checks),
		Dependencies: sanitizeComponents(dependencies),
		CheckedAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

func migrationsReady(ctx context.Context, db *sql.DB) bool {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		return false
	}
	return count > 0
}

func statusText(ready bool) string {
	if ready {
		return "ready"
	}
	return "not_ready"
}

func sanitizeComponents(in []ComponentStatus) []ComponentStatus {
	out := make([]ComponentStatus, 0, len(in))
	for _, item := range in {
		out = append(out, ComponentStatus{
			Name:        Redact(item.Name),
			Status:      Redact(item.Status),
			Remediation: Redact(item.Remediation),
		})
	}
	return out
}
