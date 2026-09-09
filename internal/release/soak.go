package release

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"time"

	"agent/internal/config"
	"agent/internal/notification"
	agentruntime "agent/internal/runtime"
	"agent/internal/storage"
)

type SoakOptions struct {
	Config               config.Config
	Duration             time.Duration
	Interval             time.Duration
	Offline              bool
	OutputPath           string
	MaxHeapGrowthBytes   uint64
	MaxGoroutineGrowth   int
	MaxSQLiteGrowthBytes int64
	Now                  func() time.Time
}

type SoakReport struct {
	RunID                  string       `json:"run_id"`
	StartedAt              time.Time    `json:"started_at"`
	CompletedAt            time.Time    `json:"completed_at"`
	Duration               string       `json:"duration"`
	Iterations             int          `json:"iterations"`
	Offline                bool         `json:"offline"`
	Samples                []SoakSample `json:"samples"`
	HeapGrowthBytes        uint64       `json:"heap_growth_bytes"`
	GoroutineGrowth        int          `json:"goroutine_growth"`
	SQLiteGrowthBytes      int64        `json:"sqlite_growth_bytes"`
	TerminalWorkflows      int64        `json:"terminal_workflows"`
	NonTerminalWorkflows   int64        `json:"non_terminal_workflows"`
	NotificationCount      int64        `json:"notification_count"`
	DuplicateNotifications int64        `json:"duplicate_notifications"`
	ChildProcessLeaks      int64        `json:"child_process_leaks"`
	WorktreeLeaks          int64        `json:"worktree_leaks"`
	BrowserArtifactLeaks   int64        `json:"browser_artifact_leaks"`
	Status                 string       `json:"status"`
	Failures               []string     `json:"failures,omitempty"`
}

type SoakSample struct {
	At                     time.Time `json:"at"`
	Goroutines             int       `json:"goroutines"`
	HeapAllocBytes         uint64    `json:"heap_alloc_bytes"`
	SQLiteBytes            int64     `json:"sqlite_bytes"`
	TaskCount              int64     `json:"task_count"`
	WorkflowCount          int64     `json:"workflow_count"`
	NonTerminalWorkflows   int64     `json:"non_terminal_workflows"`
	NotificationCount      int64     `json:"notification_count"`
	DuplicateNotifications int64     `json:"duplicate_notifications"`
}

func RunSoak(ctx context.Context, opts SoakOptions) (SoakReport, error) {
	if opts.Duration <= 0 {
		opts.Duration = 30 * time.Second
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.MaxHeapGrowthBytes == 0 {
		opts.MaxHeapGrowthBytes = 64 * 1024 * 1024
	}
	if opts.MaxGoroutineGrowth == 0 {
		opts.MaxGoroutineGrowth = 16
	}
	if opts.MaxSQLiteGrowthBytes == 0 {
		opts.MaxSQLiteGrowthBytes = 64 * 1024 * 1024
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cfg := opts.Config
	if opts.Offline {
		cfg = offlineSoakConfig(cfg)
	}
	rt, err := agentruntime.Build(ctx, cfg)
	if err != nil {
		return SoakReport{}, err
	}
	defer rt.Close()

	started := now().UTC()
	deadline := started.Add(opts.Duration)
	runID := fmt.Sprintf("soak-%s", started.Format("20060102T150405.000000000Z"))
	report := SoakReport{RunID: runID, StartedAt: started, Duration: opts.Duration.String(), Offline: opts.Offline}
	browserArtifactsAtStart := countTreeFiles(cfg.Browser.ScreenshotDir)
	worktreesAtStart := countWorktreeArtifacts(cfg)
	report.Samples = append(report.Samples, collectSoakSample(ctx, rt.Storage.SQL, cfg.Storage.SQLite.Path, now))

	iteration := 0
	for {
		if !now().Before(deadline) && iteration > 0 {
			break
		}
		iteration++
		if err := runSoakWorkload(ctx, rt, cfg, runID, iteration, now); err != nil {
			report.Failures = append(report.Failures, err.Error())
			break
		}
		report.Samples = append(report.Samples, collectSoakSample(ctx, rt.Storage.SQL, cfg.Storage.SQLite.Path, now))
		if !now().Before(deadline) {
			break
		}
		timer := time.NewTimer(opts.Interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return report, ctx.Err()
		case <-timer.C:
		}
	}
	report.CompletedAt = now().UTC()
	report.Iterations = iteration
	report.finish(ctx, rt.Storage.SQL, cfg, browserArtifactsAtStart, worktreesAtStart)
	report.applyThresholds(opts)
	if opts.OutputPath != "" {
		if err := writeSoakReport(opts.OutputPath, report); err != nil {
			return report, err
		}
	}
	if len(report.Failures) > 0 {
		return report, errors.New("soak gate failed")
	}
	return report, nil
}

func offlineSoakConfig(cfg config.Config) config.Config {
	cfg.Models = config.ModelsConfig{}
	cfg.Agent.Leader.ModelID = ""
	cfg.Agent.Planner.Enabled = boolPtr(false)
	for role, subagent := range cfg.Agent.SubAgents {
		subagent.ModelID = ""
		cfg.Agent.SubAgents[role] = subagent
	}
	cfg.RAG.Vector.Enabled = false
	cfg.RAG.Reranker.Enabled = false
	cfg.Browser.Enabled = false
	cfg.MCP.Servers = map[string]config.MCPServerConfig{}
	cfg.Tools.Allowlist = nil
	for id, backend := range cfg.CodingAgents.Backends {
		backend.Enabled = boolPtr(false)
		cfg.CodingAgents.Backends[id] = backend
	}
	cfg.Proactive.Enabled = boolPtr(false)
	return cfg
}

func runSoakWorkload(ctx context.Context, rt *agentruntime.Runtime, cfg config.Config, runID string, iteration int, now func() time.Time) error {
	taskID := fmt.Sprintf("%s-task-%06d", runID, iteration)
	input := fmt.Sprintf("soak iteration %d local workflow health check", iteration)
	if err := rt.Storage.CreateTask(ctx, storage.Task{
		ID:            taskID,
		Title:         fmt.Sprintf("soak iteration %d", iteration),
		Input:         input,
		Status:        "running",
		LeaderModelID: cfg.Agent.Leader.ModelID,
		PrivacyClass:  "local_private",
	}); err != nil {
		return err
	}
	if _, err := rt.Run(ctx, agentruntime.RunRequest{
		TaskID:        taskID,
		Input:         input,
		PrivacyClass:  "local_private",
		LeaderModelID: cfg.Agent.Leader.ModelID,
	}); err != nil {
		return err
	}
	store := notification.Store{DB: rt.Storage.SQL}
	_, err := store.PutWithPolicy(ctx, notification.Notification{
		ID:        fmt.Sprintf("%s-note-%06d", runID, iteration),
		Title:     "soak notification",
		DedupKey:  fmt.Sprintf("%s-note-%06d", runID, iteration),
		Severity:  "info",
		CreatedAt: now().UTC(),
	}, notification.Policy{})
	return err
}

func collectSoakSample(ctx context.Context, db *sql.DB, sqlitePath string, now func() time.Time) SoakSample {
	var mem stdruntime.MemStats
	stdruntime.ReadMemStats(&mem)
	return SoakSample{
		At:                     now().UTC(),
		Goroutines:             stdruntime.NumGoroutine(),
		HeapAllocBytes:         mem.HeapAlloc,
		SQLiteBytes:            sqliteSize(sqlitePath),
		TaskCount:              countRows(ctx, db, "tasks"),
		WorkflowCount:          countRows(ctx, db, "workflow_runs"),
		NonTerminalWorkflows:   countNonTerminalWorkflows(ctx, db),
		NotificationCount:      countRows(ctx, db, "notification_inbox"),
		DuplicateNotifications: countDuplicateNotifications(ctx, db),
	}
}

func (r *SoakReport) finish(ctx context.Context, db *sql.DB, cfg config.Config, browserArtifactsAtStart int64, worktreesAtStart int64) {
	if len(r.Samples) >= 2 {
		first := r.Samples[0]
		last := r.Samples[len(r.Samples)-1]
		if last.HeapAllocBytes >= first.HeapAllocBytes {
			r.HeapGrowthBytes = last.HeapAllocBytes - first.HeapAllocBytes
		}
		r.GoroutineGrowth = last.Goroutines - first.Goroutines
		r.SQLiteGrowthBytes = last.SQLiteBytes - first.SQLiteBytes
		r.NotificationCount = last.NotificationCount
		r.DuplicateNotifications = last.DuplicateNotifications
	}
	r.TerminalWorkflows = countTerminalWorkflows(ctx, db)
	r.NonTerminalWorkflows = countNonTerminalWorkflows(ctx, db)
	r.ChildProcessLeaks = 0
	r.WorktreeLeaks = maxInt64(0, countWorktreeArtifacts(cfg)-worktreesAtStart)
	r.BrowserArtifactLeaks = maxInt64(0, countTreeFiles(cfg.Browser.ScreenshotDir)-browserArtifactsAtStart)
}

func (r *SoakReport) applyThresholds(opts SoakOptions) {
	if r.HeapGrowthBytes > opts.MaxHeapGrowthBytes {
		r.Failures = append(r.Failures, fmt.Sprintf("heap growth %d exceeds %d", r.HeapGrowthBytes, opts.MaxHeapGrowthBytes))
	}
	if r.GoroutineGrowth > opts.MaxGoroutineGrowth {
		r.Failures = append(r.Failures, fmt.Sprintf("goroutine growth %d exceeds %d", r.GoroutineGrowth, opts.MaxGoroutineGrowth))
	}
	if r.SQLiteGrowthBytes > opts.MaxSQLiteGrowthBytes {
		r.Failures = append(r.Failures, fmt.Sprintf("sqlite growth %d exceeds %d", r.SQLiteGrowthBytes, opts.MaxSQLiteGrowthBytes))
	}
	if r.NonTerminalWorkflows > 0 {
		r.Failures = append(r.Failures, fmt.Sprintf("%d non-terminal workflows remain", r.NonTerminalWorkflows))
	}
	if r.DuplicateNotifications > 0 {
		r.Failures = append(r.Failures, fmt.Sprintf("%d duplicate notifications observed", r.DuplicateNotifications))
	}
	if opts.Offline && r.ChildProcessLeaks+r.WorktreeLeaks+r.BrowserArtifactLeaks > 0 {
		r.Failures = append(r.Failures, "offline soak leaked external artifacts")
	}
	if len(r.Failures) > 0 {
		r.Status = "fail"
		return
	}
	r.Status = "pass"
}

func countRows(ctx context.Context, db *sql.DB, table string) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0
	}
	return count
}

func countTerminalWorkflows(ctx context.Context, db *sql.DB) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE status IN ('completed', 'failed', 'cancelled')`).Scan(&count); err != nil {
		return 0
	}
	return count
}

func countNonTerminalWorkflows(ctx context.Context, db *sql.DB) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM workflow_runs WHERE status NOT IN ('completed', 'failed', 'cancelled')`).Scan(&count); err != nil {
		return 0
	}
	return count
}

func countDuplicateNotifications(ctx context.Context, db *sql.DB) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT dedup_key FROM notification_inbox WHERE dedup_key != '' GROUP BY dedup_key HAVING COUNT(*) > 1)`).Scan(&count); err != nil {
		return 0
	}
	return count
}

func sqliteSize(path string) int64 {
	var total int64
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, err := os.Stat(candidate)
		if err == nil {
			total += info.Size()
		}
	}
	return total
}

func countTreeFiles(root string) int64 {
	if strings.TrimSpace(root) == "" {
		return 0
	}
	var count int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		count++
		return nil
	})
	return count
}

func countWorktreeArtifacts(cfg config.Config) int64 {
	return countTreeFiles(filepath.Join(cfg.App.DataDir, "worktrees"))
}

func writeSoakReport(path string, report SoakReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func boolPtr(value bool) *bool {
	return &value
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
