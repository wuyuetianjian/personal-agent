package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"agent/internal/agent"
)

var (
	ErrMissingSubAgent  = errors.New("missing sub-agent")
	ErrDependencyFailed = errors.New("dependency failed")
)

type EarlyStopFunc func(results []agent.Result) bool

type Scheduler struct {
	Agents      map[agent.Role]agent.SubAgent
	EvidenceBus EvidenceBus
	Parallelism int
	EarlyStop   EarlyStopFunc
}

type RunResult struct {
	Results      []agent.Result
	Completed    map[string]bool
	Failed       map[string]bool
	Cancelled    map[string]bool
	EarlyStopped bool
}

type nodeStatus string

const (
	statusPending   nodeStatus = "pending"
	statusRunning   nodeStatus = "running"
	statusCompleted nodeStatus = "completed"
	statusFailed    nodeStatus = "failed"
	statusCancelled nodeStatus = "cancelled"
)

type nodeRunResult struct {
	node    agent.TaskNode
	result  agent.Result
	attempt int
	err     error
}

func (s Scheduler) Run(ctx context.Context, dag *DAG) (RunResult, error) {
	if dag == nil {
		return RunResult{}, errors.New("nil dag")
	}
	runCtx, cancelAll := context.WithCancel(ctx)
	defer cancelAll()
	parallelism := s.Parallelism
	if parallelism <= 0 {
		parallelism = 1
	}
	statuses := make(map[string]nodeStatus)
	attempts := make(map[string]int)
	for _, node := range dag.Nodes() {
		statuses[node.ID] = statusPending
	}

	var out RunResult
	out.Completed = make(map[string]bool)
	out.Failed = make(map[string]bool)
	out.Cancelled = make(map[string]bool)
	resultsByID := make(map[string]agent.Result)
	scheduledReady := make(map[string]bool)
	readyQueue := make([]agent.TaskNode, 0)
	resultCh := make(chan nodeRunResult, len(dag.Nodes()))
	running := 0
	stopping := false
	var stopErr error

	for {
		if err := runCtx.Err(); err != nil && !stopping {
			stopping = true
			stopErr = err
			for id, status := range statuses {
				if status == statusPending || status == statusRunning {
					statuses[id] = statusCancelled
					out.Cancelled[id] = true
					node, _ := dag.Node(id)
					_ = s.publish(context.Background(), EventNodeCancelled, node, attempts[id], agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error()})
				}
			}
			cancelAll()
			if running == 0 {
				return finalizeResults(dag, resultsByID, out), err
			}
		}

		if !stopping {
			completed := statusMap(statuses, statusCompleted)
			blocked := blockedMap(statuses)
			for _, node := range dag.Ready(completed, blocked) {
				if scheduledReady[node.ID] {
					continue
				}
				scheduledReady[node.ID] = true
				readyQueue = append(readyQueue, node)
				if err := s.publish(runCtx, EventNodeReady, node, attempts[node.ID], agent.Result{}); err != nil {
					return finalizeResults(dag, resultsByID, out), err
				}
			}
		}

		if !stopping && s.EarlyStop != nil && len(resultsByID) > 0 {
			out.Results = orderedResults(dag, resultsByID)
			if s.EarlyStop(out.Results) {
				out.EarlyStopped = true
				stopping = true
				cancelAll()
				for id, status := range statuses {
					if status == statusPending {
						statuses[id] = statusCancelled
						out.Cancelled[id] = true
					}
				}
				if err := s.publish(context.Background(), EventEarlyStopped, agent.TaskNode{}, 0, agent.Result{}); err != nil {
					return finalizeResults(dag, resultsByID, out), err
				}
				if running == 0 {
					return finalizeResults(dag, resultsByID, out), nil
				}
			}
		}

		for !stopping && running < parallelism && len(readyQueue) > 0 {
			node := readyQueue[0]
			readyQueue = readyQueue[1:]
			if statuses[node.ID] != statusPending {
				continue
			}
			subAgent := s.Agents[node.Role]
			if subAgent == nil {
				statuses[node.ID] = statusFailed
				out.Failed[node.ID] = true
				result := failedResult(node, agent.ErrorBlockedMissingInput, fmt.Sprintf("%v: %s", ErrMissingSubAgent, node.Role))
				resultsByID[node.ID] = result
				if err := s.publish(runCtx, EventNodeFailed, node, attempts[node.ID], result); err != nil {
					return finalizeResults(dag, resultsByID, out), err
				}
				cancelDependents(runCtx, s, dag, statuses, out.Cancelled, node.ID)
				continue
			}
			statuses[node.ID] = statusRunning
			attempts[node.ID]++
			running++
			if err := s.publish(runCtx, EventNodeStarted, node, attempts[node.ID], agent.Result{}); err != nil {
				return finalizeResults(dag, resultsByID, out), err
			}
			go func(node agent.TaskNode, attempt int, subAgent agent.SubAgent) {
				nodeCtx := runCtx
				cancel := func() {}
				if node.Timeout > 0 {
					nodeCtx, cancel = context.WithTimeout(runCtx, node.Timeout)
				}
				defer cancel()
				result, err := subAgent.Execute(nodeCtx, node)
				if result.TaskID == "" {
					result.TaskID = node.TaskID
				}
				if result.NodeID == "" {
					result.NodeID = node.ID
				}
				if result.Role == "" {
					result.Role = node.Role
				}
				resultCh <- nodeRunResult{node: node, result: result, attempt: attempt, err: err}
			}(node, attempts[node.ID], subAgent)
		}

		if allTerminal(statuses) {
			if stopErr != nil {
				return finalizeResults(dag, resultsByID, out), stopErr
			}
			return finalizeResults(dag, resultsByID, out), nil
		}
		if running == 0 && len(readyQueue) == 0 {
			if stopErr != nil {
				return finalizeResults(dag, resultsByID, out), stopErr
			}
			return finalizeResults(dag, resultsByID, out), nil
		}

		nodeResult := <-resultCh
		running--
		node := nodeResult.node
		result := nodeResult.result
		if nodeResult.err != nil && result.ErrorMessage == "" {
			result.ErrorMessage = nodeResult.err.Error()
		}
		if nodeResult.err != nil && result.ErrorCategory == "" {
			result.ErrorCategory = agent.ErrorRetryable
		}
		if errors.Is(nodeResult.err, context.Canceled) || errors.Is(nodeResult.err, context.DeadlineExceeded) {
			statuses[node.ID] = statusCancelled
			out.Cancelled[node.ID] = true
			resultsByID[node.ID] = result
			if err := s.publish(context.Background(), EventNodeCancelled, node, nodeResult.attempt, result); err != nil {
				return finalizeResults(dag, resultsByID, out), err
			}
			continue
		}
		if nodeResult.err != nil || result.ErrorCategory != "" {
			if shouldRetry(result, nodeResult.err) && attempts[node.ID] < node.MaxAttempts {
				statuses[node.ID] = statusPending
				scheduledReady[node.ID] = false
				if err := s.publish(runCtx, EventNodeRetrying, node, nodeResult.attempt, result); err != nil {
					return finalizeResults(dag, resultsByID, out), err
				}
				continue
			}
			statuses[node.ID] = statusFailed
			out.Failed[node.ID] = true
			resultsByID[node.ID] = result
			if err := s.publish(runCtx, EventNodeFailed, node, nodeResult.attempt, result); err != nil {
				return finalizeResults(dag, resultsByID, out), err
			}
			cancelDependents(runCtx, s, dag, statuses, out.Cancelled, node.ID)
			continue
		}
		statuses[node.ID] = statusCompleted
		out.Completed[node.ID] = true
		resultsByID[node.ID] = result
		if err := s.publish(runCtx, EventNodeCompleted, node, nodeResult.attempt, result); err != nil {
			return finalizeResults(dag, resultsByID, out), err
		}
	}
}

func (s Scheduler) publish(ctx context.Context, eventType EventType, node agent.TaskNode, attempt int, result agent.Result) error {
	if s.EvidenceBus == nil {
		return nil
	}
	return s.EvidenceBus.Publish(ctx, Event{
		Type:          eventType,
		TaskID:        node.TaskID,
		NodeID:        node.ID,
		Role:          node.Role,
		Attempt:       attempt,
		ErrorCategory: result.ErrorCategory,
		ErrorMessage:  result.ErrorMessage,
		EvidenceIDs:   append([]string(nil), result.EvidenceIDs...),
	})
}

func failedResult(node agent.TaskNode, category agent.ErrorCategory, message string) agent.Result {
	return agent.Result{
		TaskID:        node.TaskID,
		NodeID:        node.ID,
		Role:          node.Role,
		ErrorCategory: category,
		ErrorMessage:  message,
	}
}

func shouldRetry(result agent.Result, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return result.ErrorCategory == "" || result.ErrorCategory == agent.ErrorRetryable
}

func cancelDependents(ctx context.Context, s Scheduler, dag *DAG, statuses map[string]nodeStatus, cancelled map[string]bool, id string) {
	for _, depID := range dag.Dependents(id) {
		if statuses[depID] != statusPending {
			continue
		}
		statuses[depID] = statusCancelled
		cancelled[depID] = true
		node, _ := dag.Node(depID)
		result := failedResult(node, agent.ErrorBlockedMissingInput, ErrDependencyFailed.Error())
		_ = s.publish(ctx, EventNodeCancelled, node, 0, result)
		cancelDependents(ctx, s, dag, statuses, cancelled, depID)
	}
}

func statusMap(statuses map[string]nodeStatus, wanted nodeStatus) map[string]bool {
	out := make(map[string]bool)
	for id, status := range statuses {
		if status == wanted {
			out[id] = true
		}
	}
	return out
}

func blockedMap(statuses map[string]nodeStatus) map[string]bool {
	out := make(map[string]bool)
	for id, status := range statuses {
		if status != statusPending {
			out[id] = true
		}
	}
	return out
}

func allTerminal(statuses map[string]nodeStatus) bool {
	for _, status := range statuses {
		if status == statusPending || status == statusRunning {
			return false
		}
	}
	return true
}

func finalizeResults(dag *DAG, resultsByID map[string]agent.Result, out RunResult) RunResult {
	out.Results = orderedResults(dag, resultsByID)
	return out
}

func orderedResults(dag *DAG, resultsByID map[string]agent.Result) []agent.Result {
	results := make([]agent.Result, 0, len(resultsByID))
	for _, node := range dag.Nodes() {
		if result, ok := resultsByID[node.ID]; ok {
			results = append(results, result)
		}
	}
	return results
}
