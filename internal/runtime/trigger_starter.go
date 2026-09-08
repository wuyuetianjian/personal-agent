package runtime

import (
	"context"
	"encoding/json"

	"agent/internal/storage"
	"agent/internal/trigger"
	"agent/internal/workflow"
)

type TriggerWorkflowStarter struct {
	Runtime *Runtime
}

func (s TriggerWorkflowStarter) StartTriggerWorkflow(ctx context.Context, tr trigger.Trigger) (string, error) {
	if s.Runtime == nil {
		return "", ErrWorkflowEngineUnavailable
	}
	taskID, err := randomID("task")
	if err != nil {
		return "", err
	}
	workflowID, err := randomID("wf")
	if err != nil {
		return "", err
	}
	inputText := "trigger " + tr.ID
	if tr.Condition.Query != "" {
		inputText = tr.Condition.Query
	}
	if err := s.Runtime.Storage.CreateTask(ctx, storage.Task{
		ID:            taskID,
		Title:         inputText,
		Input:         inputText,
		Status:        "running",
		LeaderModelID: s.Runtime.LeaderModel,
		PrivacyClass:  s.Runtime.PrivacyClass,
	}); err != nil {
		return "", err
	}
	input, err := json.Marshal(workflowInput{Input: inputText})
	if err != nil {
		return "", err
	}
	plan, err := s.Runtime.planWorkflow(ctx, workflowID, RunRequest{
		TaskID:        taskID,
		Input:         inputText,
		ProjectID:     tr.ProjectID,
		PrivacyClass:  s.Runtime.PrivacyClass,
		LeaderModelID: s.Runtime.LeaderModel,
	})
	if err != nil {
		_ = s.Runtime.Storage.FailTask(ctx, taskID, "planning_failed", err.Error())
		return "", err
	}
	if tr.SkillID != "" {
		plan.SkillID = tr.SkillID
	}
	if err := s.Runtime.Workflows.Create(ctx, workflow.Run{
		ID:           workflowID,
		TaskID:       taskID,
		ProjectID:    tr.ProjectID,
		SkillID:      plan.SkillID,
		SkillVersion: plan.SkillVersion,
		Status:       workflow.StatusPending,
		InputJSON:    string(input),
	}, plan.Nodes); err != nil {
		_ = s.Runtime.Storage.FailTask(ctx, taskID, "workflow_create_failed", err.Error())
		return "", err
	}
	return workflowID, nil
}
