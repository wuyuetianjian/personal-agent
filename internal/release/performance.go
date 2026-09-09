package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sync"
	"time"

	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/rag"
	agentruntime "agent/internal/runtime"
	"agent/internal/storage"
)

type PerformanceOptions struct {
	Config                     config.Config
	Offline                    bool
	OutputPath                 string
	Concurrency                int
	MaxStartupLatency          time.Duration
	MaxLocalQueryLatency       time.Duration
	MaxHybridRAGLatency        time.Duration
	MaxWorkflowDispatchLatency time.Duration
}

type PerformanceReport struct {
	Status                       string    `json:"status"`
	StartedAt                    time.Time `json:"started_at"`
	CompletedAt                  time.Time `json:"completed_at"`
	Offline                      bool      `json:"offline"`
	StartupLatencyMS             int64     `json:"startup_latency_ms"`
	LocalQueryLatencyMS          int64     `json:"local_query_latency_ms"`
	HybridRAGLatencyMS           int64     `json:"hybrid_rag_latency_ms"`
	WorkflowDispatchLatencyMS    int64     `json:"workflow_dispatch_latency_ms"`
	HeapAllocBytes               uint64    `json:"heap_alloc_bytes"`
	ConcurrentWorkflowsRequested int       `json:"concurrent_workflows_requested"`
	ConcurrentWorkflowsCompleted int       `json:"concurrent_workflows_completed"`
	ConcurrentWorkflowLatencyMS  int64     `json:"concurrent_workflow_latency_ms"`
	Failures                     []string  `json:"failures,omitempty"`
}

func RunPerformanceBaseline(ctx context.Context, opts PerformanceOptions) (PerformanceReport, error) {
	opts = normalizePerformanceOptions(opts)
	cfg := opts.Config
	if opts.Offline {
		cfg = offlineSoakConfig(cfg)
	}
	report := PerformanceReport{StartedAt: time.Now().UTC(), Offline: opts.Offline, ConcurrentWorkflowsRequested: opts.Concurrency}
	start := time.Now()
	rt, err := agentruntime.Build(ctx, cfg)
	if err != nil {
		return PerformanceReport{}, err
	}
	report.StartupLatencyMS = millis(time.Since(start))
	defer rt.Close()

	if err := seedPerformanceFixture(ctx, rt); err != nil {
		return report, err
	}
	report.LocalQueryLatencyMS, err = measure(ctx, func() error {
		_, err := rt.Memory.Search(ctx, "perf-local-query", "baseline memory preference", 8)
		return err
	})
	if err != nil {
		return report, err
	}
	report.HybridRAGLatencyMS, err = measure(ctx, func() error {
		evidence, err := rt.RAG.Retrieve(ctx, "perf-rag-query", "hybrid fallback latency", agentruntime.RetrieveOptions{TopK: 4})
		if err != nil {
			return err
		}
		if len(evidence) == 0 {
			return errors.New("hybrid RAG baseline returned no evidence")
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	report.WorkflowDispatchLatencyMS, err = measure(ctx, func() error {
		return runPerformanceWorkflow(ctx, rt, cfg, "perf-workflow")
	})
	if err != nil {
		return report, err
	}
	var mem stdruntime.MemStats
	stdruntime.ReadMemStats(&mem)
	report.HeapAllocBytes = mem.HeapAlloc
	concurrentStart := time.Now()
	report.ConcurrentWorkflowsCompleted = runConcurrentPerformanceWorkflows(ctx, rt, cfg, opts.Concurrency)
	report.ConcurrentWorkflowLatencyMS = millis(time.Since(concurrentStart))
	report.CompletedAt = time.Now().UTC()
	report.applyPerformanceThresholds(opts)
	if opts.OutputPath != "" {
		if err := writePerformanceReport(opts.OutputPath, report); err != nil {
			return report, err
		}
	}
	if len(report.Failures) > 0 {
		return report, errors.New("performance baseline failed")
	}
	return report, nil
}

func normalizePerformanceOptions(opts PerformanceOptions) PerformanceOptions {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 2
	}
	if opts.MaxStartupLatency <= 0 {
		opts.MaxStartupLatency = 2 * time.Second
	}
	if opts.MaxLocalQueryLatency <= 0 {
		opts.MaxLocalQueryLatency = 100 * time.Millisecond
	}
	if opts.MaxHybridRAGLatency <= 0 {
		opts.MaxHybridRAGLatency = 250 * time.Millisecond
	}
	if opts.MaxWorkflowDispatchLatency <= 0 {
		opts.MaxWorkflowDispatchLatency = 250 * time.Millisecond
	}
	return opts
}

func seedPerformanceFixture(ctx context.Context, rt *agentruntime.Runtime) error {
	if err := memory.NewStore(rt.Storage.SQL).Append(ctx, memory.EpisodicEvent{ID: "perf-memory", EventType: "baseline", Summary: "baseline memory preference"}); err != nil {
		return err
	}
	_, err := rag.NewSQLiteStore(rt.Storage.SQL, rag.Chunker{MaxTokens: 32}).Index(ctx, rag.Document{
		ID:           "perf-rag-doc",
		SourceURI:    "local://performance",
		Title:        "Performance Baseline",
		Text:         "hybrid fallback latency should return this local BM25 evidence",
		PrivacyClass: "local_private",
	})
	return err
}

func runPerformanceWorkflow(ctx context.Context, rt *agentruntime.Runtime, cfg config.Config, id string) error {
	if err := rt.Storage.CreateTask(ctx, storage.Task{ID: id, Title: id, Input: "hybrid fallback latency", Status: "running", LeaderModelID: cfg.Agent.Leader.ModelID, PrivacyClass: "local_private"}); err != nil {
		return err
	}
	_, err := rt.Run(ctx, agentruntime.RunRequest{TaskID: id, Input: "hybrid fallback latency", PrivacyClass: "local_private", LeaderModelID: cfg.Agent.Leader.ModelID})
	return err
}

func runConcurrentPerformanceWorkflows(ctx context.Context, rt *agentruntime.Runtime, cfg config.Config, concurrency int) int {
	var wg sync.WaitGroup
	results := make(chan bool, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			id := fmt.Sprintf("perf-concurrent-%03d", index)
			results <- runPerformanceWorkflow(ctx, rt, cfg, id) == nil
		}(i)
	}
	wg.Wait()
	close(results)
	completed := 0
	for ok := range results {
		if ok {
			completed++
		}
	}
	return completed
}

func (r *PerformanceReport) applyPerformanceThresholds(opts PerformanceOptions) {
	if time.Duration(r.StartupLatencyMS)*time.Millisecond > opts.MaxStartupLatency {
		r.Failures = append(r.Failures, fmt.Sprintf("startup latency %dms exceeds %s", r.StartupLatencyMS, opts.MaxStartupLatency))
	}
	if time.Duration(r.LocalQueryLatencyMS)*time.Millisecond > opts.MaxLocalQueryLatency {
		r.Failures = append(r.Failures, fmt.Sprintf("local query latency %dms exceeds %s", r.LocalQueryLatencyMS, opts.MaxLocalQueryLatency))
	}
	if time.Duration(r.HybridRAGLatencyMS)*time.Millisecond > opts.MaxHybridRAGLatency {
		r.Failures = append(r.Failures, fmt.Sprintf("hybrid RAG latency %dms exceeds %s", r.HybridRAGLatencyMS, opts.MaxHybridRAGLatency))
	}
	if time.Duration(r.WorkflowDispatchLatencyMS)*time.Millisecond > opts.MaxWorkflowDispatchLatency {
		r.Failures = append(r.Failures, fmt.Sprintf("workflow dispatch latency %dms exceeds %s", r.WorkflowDispatchLatencyMS, opts.MaxWorkflowDispatchLatency))
	}
	if r.ConcurrentWorkflowsCompleted != r.ConcurrentWorkflowsRequested {
		r.Failures = append(r.Failures, fmt.Sprintf("concurrent workflows completed %d of %d", r.ConcurrentWorkflowsCompleted, r.ConcurrentWorkflowsRequested))
	}
	if len(r.Failures) > 0 {
		r.Status = "fail"
		return
	}
	r.Status = "pass"
}

func measure(ctx context.Context, fn func() error) (int64, error) {
	start := time.Now()
	if err := fn(); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return millis(time.Since(start)), nil
}

func millis(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	ms := duration.Milliseconds()
	if ms == 0 {
		return 1
	}
	return ms
}

func writePerformanceReport(path string, report PerformanceReport) error {
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
