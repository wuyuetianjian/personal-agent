package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrUnsupportedAction     = errors.New("unsupported browser action")
	ErrConfirmationRequired  = errors.New("browser confirmation required")
	ErrCoordinateUnavailable = errors.New("browser coordinate fallback unavailable")
)

type Runtime interface {
	Navigate(ctx context.Context, rawURL string) (Observation, error)
	Observe(ctx context.Context) (Observation, error)
	Locate(ctx context.Context, target Target) (LocatedElement, error)
	Click(ctx context.Context, target Target) (Observation, error)
	MoveMouse(ctx context.Context, x int, y int) (Observation, error)
	MouseWheel(ctx context.Context, deltaX int, deltaY int) (Observation, error)
	TypeText(ctx context.Context, target Target, text string) (Observation, error)
	KeyboardShortcut(ctx context.Context, keys []string) (Observation, error)
	AccessibilityTree(ctx context.Context) (Observation, error)
	Screenshot(ctx context.Context) (Observation, error)
}

type BrowserEvidenceBus interface {
	PublishBrowserEvent(ctx context.Context, event EvidenceEvent) error
}

type RuntimeActionAdapter struct {
	Runtime      Runtime
	Evidence     EvidenceBuilder
	EvidenceBus  BrowserEvidenceBus
	Confirmation ConfirmationChecker
	Now          func() time.Time
}

func (a RuntimeActionAdapter) Execute(ctx context.Context, action Action) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if a.Runtime == nil {
		return Observation{}, ErrUnsupportedAction
	}

	switch action.Type {
	case ActionNavigate, ActionOpenTab:
		return a.Runtime.Navigate(ctx, action.URL)
	case ActionReadTitle, ActionReadURL, ActionReadVisibleText, ActionReadDOM:
		return a.Runtime.Observe(ctx)
	case ActionReadAccessibility:
		return a.Runtime.AccessibilityTree(ctx)
	case ActionCaptureScreenshot:
		return a.Runtime.Screenshot(ctx)
	case ActionLocateElement:
		located, err := a.Runtime.Locate(ctx, action.Target)
		if err != nil {
			return Observation{}, err
		}
		return Observation{VisibleText: located.Text, DOMSummary: located.Selector, A11ySummary: located.Role}, nil
	case ActionClick, ActionDoubleClick, ActionClickNavigation, ActionFocusInput:
		return a.executeTargeted(ctx, action, func(target Target) (Observation, error) {
			return a.Runtime.Click(ctx, target)
		})
	case ActionScroll, ActionMouseWheel:
		return a.Runtime.MouseWheel(ctx, action.Target.X, action.Target.Y)
	case ActionMoveMouse:
		if !hasCoordinates(action.Target) {
			return Observation{}, ErrCoordinateUnavailable
		}
		return a.Runtime.MoveMouse(ctx, action.Target.X, action.Target.Y)
	case ActionTypeText:
		return a.executeTargeted(ctx, action, func(target Target) (Observation, error) {
			return a.Runtime.TypeText(ctx, target, action.Input)
		})
	case ActionKeyboardShortcut:
		return a.Runtime.KeyboardShortcut(ctx, splitShortcut(action.Input))
	case ActionSendMessage, ActionPostContent, ActionSaveSettings:
		if action.Input != "" {
			return a.executeTargeted(ctx, action, func(target Target) (Observation, error) {
				return a.Runtime.TypeText(ctx, target, action.Input)
			})
		}
		return a.executeTargeted(ctx, action, func(target Target) (Observation, error) {
			return a.Runtime.Click(ctx, target)
		})
	case ActionLoginSubmit, ActionSubmitForm:
		return a.executeTargeted(ctx, action, func(target Target) (Observation, error) {
			return a.Runtime.Click(ctx, target)
		})
	default:
		return Observation{}, fmt.Errorf("%w: %s", ErrUnsupportedAction, action.Type)
	}
}

func (a RuntimeActionAdapter) executeTargeted(ctx context.Context, action Action, exec func(Target) (Observation, error)) (Observation, error) {
	if action.Mode == ControlModeCoordinate {
		if !hasCoordinates(action.Target) {
			return Observation{}, ErrCoordinateUnavailable
		}
		return exec(action.Target)
	}

	if hasSemanticTarget(action.Target) {
		if _, err := a.Runtime.Locate(ctx, action.Target); err != nil {
			fallback, fallbackErr := CoordinateFallback(action, err)
			if fallbackErr != nil {
				return Observation{}, err
			}
			return exec(fallback.Target)
		}
	}
	return exec(action.Target)
}

func (a RuntimeActionAdapter) RequiresConfirmation(action Action) bool {
	level := classifyAction(action.Type)
	return level == PermissionWriteInteraction || level == PermissionHighRiskTransaction
}

func (a RuntimeActionAdapter) Confirmed(ctx context.Context, action Action) bool {
	if !a.RequiresConfirmation(action) {
		return true
	}
	if a.Confirmation == nil {
		return false
	}
	return a.Confirmation.IsConfirmed(ctx, action.ConfirmationID, action)
}

func (a RuntimeActionAdapter) BuildEvidence(category EvidenceCategory, taskID string, agentID string, result Result, cancelled bool) EvidenceEvent {
	if result.RecordedAt.IsZero() {
		result.RecordedAt = a.now()
	}
	return a.Evidence.Build(category, taskID, agentID, result, cancelled)
}

func (a RuntimeActionAdapter) PublishEvidence(ctx context.Context, category EvidenceCategory, taskID string, agentID string, result Result, cancelled bool) error {
	if a.EvidenceBus == nil {
		return nil
	}
	return a.EvidenceBus.PublishBrowserEvent(ctx, a.BuildEvidence(category, taskID, agentID, result, cancelled))
}

func (a RuntimeActionAdapter) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

func splitShortcut(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		return r == '+' || r == ',' || r == ' '
	})
}
