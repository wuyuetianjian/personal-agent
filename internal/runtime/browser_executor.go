package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"agent/internal/agent"
	"agent/internal/browser"
	"agent/internal/config"
)

type BrowserTool interface {
	Execute(ctx context.Context, action browser.Action) (browser.Result, error)
	Observe(ctx context.Context, sessionID string) (browser.Observation, error)
}

type BrowserExecutor struct {
	Tool           BrowserTool
	Config         config.BrowserConfig
	Evidence       EvidenceStore
	RuntimeFactory func(ctx context.Context) (browser.Runtime, func(), error)
}

func NewBrowserExecutor(cfg config.BrowserConfig) BrowserExecutor {
	return BrowserExecutor{
		Config: cfg,
		RuntimeFactory: func(ctx context.Context) (browser.Runtime, func(), error) {
			rt, err := browser.NewChromedpRuntime(ctx, browser.ChromedpOptions{
				ParentContext: ctx,
				ScreenshotDir: cfg.ScreenshotDir,
			})
			if err != nil {
				return nil, nil, err
			}
			return rt, rt.Close, nil
		},
	}
}

func (e BrowserExecutor) Execute(ctx context.Context, node agent.TaskNode, input workflowInput) (agent.Result, error) {
	if err := ctx.Err(); err != nil {
		return agent.Result{}, err
	}
	tool, closeFn, err := e.tool(ctx)
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	if closeFn != nil {
		defer closeFn()
	}
	action := browserActionFromNode(node, e.Config)
	var observation browser.Observation
	var result browser.Result
	switch node.Type {
	case "browser.read":
		observation, err = tool.Observe(ctx, action.SessionID)
		result = browser.Result{Action: action, Observation: observation, Decision: browser.DecisionAllowed, RecordedAt: time.Now().UTC()}
	case "browser.navigate", "browser.write":
		result, err = tool.Execute(ctx, action)
		observation = result.Observation
	default:
		err = browser.ErrUnsupportedAction
	}
	if err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	if result.Error != nil {
		err = errors.New(result.Error.Message)
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: result.Error.Message, Usage: agentUsage(node.Input, "")}, err
	}
	if e.Evidence == nil {
		err = fmt.Errorf("browser executor evidence store is required")
		return agent.Result{ErrorCategory: agent.ErrorBlockedMissingInput, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, "")}, err
	}
	content := browserObservationText(observation)
	evidence := Evidence{
		ID:           stableEvidenceID(node.TaskID, string(SourceBrowser), node.ID),
		TaskID:       node.TaskID,
		NodeID:       node.ID,
		Claim:        "browser observation",
		SourceType:   SourceBrowser,
		SourceID:     string(action.Type),
		Content:      content,
		Score:        1,
		Trust:        0.75,
		PrivacyClass: defaultPrivacyClass(inputPrivacyClass(input)),
		CreatedAt:    time.Now().UTC(),
	}
	if err := putEvidence(ctx, e.Evidence, []Evidence{evidence}); err != nil {
		return agent.Result{ErrorCategory: agent.ErrorRetryable, ErrorMessage: err.Error(), Usage: agentUsage(node.Input, content)}, err
	}
	return agent.Result{Text: content, EvidenceIDs: []string{evidence.ID}, Usage: agentUsage(node.Input, content)}, nil
}

func (e BrowserExecutor) tool(ctx context.Context) (BrowserTool, func(), error) {
	if e.Tool != nil {
		return e.Tool, nil, nil
	}
	if e.RuntimeFactory == nil {
		return nil, nil, browser.ErrUnsupportedAction
	}
	rt, closeFn, err := e.RuntimeFactory(ctx)
	if err != nil {
		return nil, nil, err
	}
	sessions := browser.NewInMemorySessionManager()
	allowed := append([]string(nil), e.Config.ProfileReuse.AllowedDomains...)
	if len(allowed) == 0 {
		allowed = []string{"localhost", "127.0.0.1"}
	}
	var expiresAt time.Time
	if e.Config.ProfileReuse.SessionTTL.Duration > 0 {
		expiresAt = time.Now().Add(e.Config.ProfileReuse.SessionTTL.Duration)
	}
	_, _ = sessions.Create(browser.Session{
		ID:              "default",
		Domains:         allowed,
		ExpiresAt:       expiresAt,
		ReusePolicy:     browser.ReuseWhenAllowed,
		IsolationPolicy: browser.IsolationPerTask,
		StoragePolicy:   browser.StorageLocalOnly,
	})
	adapter := browser.RuntimeActionAdapter{Runtime: rt}
	tool := browser.GovernedTool{
		Sessions: sessions,
		Classifier: browser.PermissionClassifier{Policy: browser.PermissionPolicy{
			AutoAllowRead:               true,
			AutoAllowNavigation:         true,
			AutoAllowLowRiskInteraction: true,
			RequireAllowlistedDomain:    true,
		}},
		Runtime:  adapter,
		Evidence: browser.EvidenceBuilder{Privacy: browser.PrivacyGateway{}},
	}
	return tool, closeFn, nil
}

func browserActionFromNode(node agent.TaskNode, cfg config.BrowserConfig) browser.Action {
	var action browser.Action
	if err := json.Unmarshal([]byte(node.Input), &action); err != nil {
		action = browser.Action{}
	}
	action.TaskID = node.TaskID
	action.AgentID = string(node.Role)
	if action.SessionID == "" {
		action.SessionID = "default"
	}
	if action.Type == "" {
		switch node.Type {
		case "browser.navigate":
			action.Type = browser.ActionNavigate
			action.URL = strings.TrimSpace(node.Input)
		case "browser.write":
			action.Type = browser.ActionClick
		default:
			action.Type = browser.ActionReadVisibleText
		}
	}
	if action.Domain == "" && action.URL != "" {
		if parsed, err := url.Parse(action.URL); err == nil {
			action.Domain = parsed.Hostname()
		}
	}
	if action.URL == "" && node.Type == "browser.navigate" {
		action.URL = strings.TrimSpace(node.Input)
	}
	if action.Mode == "" {
		action.Mode = browser.ControlModeSemantic
	}
	_ = cfg
	return action
}

func browserObservationText(observation browser.Observation) string {
	parts := []string{}
	if observation.URL != "" {
		parts = append(parts, "url: "+observation.URL)
	}
	if observation.Title != "" {
		parts = append(parts, "title: "+observation.Title)
	}
	if observation.VisibleText != "" {
		parts = append(parts, "visible_text: "+observation.VisibleText)
	}
	if observation.A11ySummary != "" {
		parts = append(parts, "accessibility: "+observation.A11ySummary)
	}
	if observation.ScreenshotID != "" {
		parts = append(parts, "screenshot: "+observation.ScreenshotID)
	}
	if len(parts) == 0 {
		return "browser observation completed"
	}
	return strings.Join(parts, "\n")
}

func inputPrivacyClass(input workflowInput) string {
	_ = input
	return defaultPrivacyClass("")
}
