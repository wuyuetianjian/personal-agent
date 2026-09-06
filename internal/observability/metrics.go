package observability

import (
	"context"
	"database/sql"
	"sync"
)

type Registry struct {
	mu       sync.RWMutex
	counters map[string]float64
	gauges   map[string]float64
}

func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]float64{},
		gauges:   map[string]float64{},
	}
}

func (r *Registry) Inc(name string, delta float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counters[Redact(name)] += delta
}

func (r *Registry) SetGauge(name string, value float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[Redact(name)] = value
}

func (r *Registry) AddGauge(name string, delta float64) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[Redact(name)] += delta
}

func (r *Registry) Snapshot(ctx context.Context, db *sql.DB) map[string]any {
	snapshot := map[string]any{
		"counters": map[string]float64{},
		"gauges":   map[string]float64{},
	}
	if r != nil {
		r.mu.RLock()
		counters := snapshot["counters"].(map[string]float64)
		for name, value := range r.counters {
			counters[Redact(name)] = value
		}
		gauges := snapshot["gauges"].(map[string]float64)
		for name, value := range r.gauges {
			gauges[Redact(name)] = value
		}
		r.mu.RUnlock()
	}
	dbMetrics := map[string]int64{}
	if db != nil {
		dbMetrics["task_count"] = countRows(ctx, db, "tasks")
		dbMetrics["workflow_count"] = countRows(ctx, db, "workflow_runs")
		dbMetrics["active_agents"] = 0
		dbMetrics["queue_depth"] = countWhere(ctx, db, "tasks", "status", "running")
	}
	snapshot["runtime"] = dbMetrics
	return sanitizeAny(snapshot).(map[string]any)
}

func countRows(ctx context.Context, db *sql.DB, table string) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0
	}
	return count
}

func countWhere(ctx context.Context, db *sql.DB, table string, column string, value string) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", value).Scan(&count); err != nil {
		return 0
	}
	return count
}

func sanitizeAny(value any) any {
	switch typed := value.(type) {
	case string:
		return Redact(typed)
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[Redact(key)] = sanitizeAny(item)
		}
		return out
	case map[string]float64:
		out := make(map[string]float64, len(typed))
		for key, item := range typed {
			out[Redact(key)] = item
		}
		return out
	case map[string]int64:
		out := make(map[string]int64, len(typed))
		for key, item := range typed {
			out[Redact(key)] = item
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, sanitizeAny(item))
		}
		return out
	default:
		return value
	}
}
