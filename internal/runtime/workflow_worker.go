package runtime

import (
	"context"
	"errors"
	"sync"
	"time"

	"agent/internal/workflow"
)

const DefaultWorkflowWorkerPollInterval = 5 * time.Second

type WorkflowWorker struct {
	Engine        *WorkflowEngine
	Workflows     workflow.Store
	BatchSize     int
	MaxConcurrent int
	PollInterval  time.Duration
}

func NewWorkflowWorker(engine *WorkflowEngine) WorkflowWorker {
	worker := WorkflowWorker{Engine: engine, BatchSize: 20, MaxConcurrent: 2, PollInterval: DefaultWorkflowWorkerPollInterval}
	if engine != nil {
		worker.Workflows = engine.Workflows
		worker.MaxConcurrent = engine.Parallelism
	}
	return worker
}

func (w WorkflowWorker) Start(ctx context.Context) {
	interval := w.PollInterval
	if interval <= 0 {
		interval = DefaultWorkflowWorkerPollInterval
	}
	go func() {
		_, _ = w.RunOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = w.RunOnce(ctx)
			}
		}
	}()
}

func (w WorkflowWorker) RunOnce(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if w.Engine == nil {
		return 0, ErrWorkflowEngineUnavailable
	}
	batchSize := w.BatchSize
	if batchSize <= 0 {
		batchSize = 20
	}
	runs, err := w.Workflows.ListRunnable(ctx, batchSize)
	if err != nil {
		return 0, err
	}
	if len(runs) == 0 {
		return 0, nil
	}
	maxConcurrent := w.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	sem := make(chan struct{}, maxConcurrent)
	errs := make(chan error, len(runs))
	var wg sync.WaitGroup
	for _, run := range runs {
		if err := ctx.Err(); err != nil {
			return len(runs), err
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(workflowID string) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := w.Engine.RunWorkflow(ctx, workflowID); err != nil && !errors.Is(err, context.Canceled) {
				errs <- err
			}
		}(run.ID)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		return len(runs), err
	}
	return len(runs), nil
}
