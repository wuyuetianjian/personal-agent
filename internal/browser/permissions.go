package browser

type PermissionPolicy struct {
	AutoAllowRead               bool
	AutoAllowNavigation         bool
	AutoAllowLowRiskInteraction bool
	RequireAllowlistedDomain    bool
}

type PermissionRequest struct {
	Action Action
	Level  PermissionLevel
}

type PermissionClassifier struct {
	Policy PermissionPolicy
}

func (c PermissionClassifier) Classify(action Action) PermissionRequest {
	return PermissionRequest{
		Action: action,
		Level:  classifyAction(action.Type),
	}
}

func (c PermissionClassifier) Decide(action Action, allowlisted bool) PermissionDecision {
	level := classifyAction(action.Type)
	if c.Policy.RequireAllowlistedDomain && isNavigation(level) && !allowlisted {
		return DecisionDenied
	}

	switch level {
	case PermissionRead:
		if c.Policy.AutoAllowRead {
			return DecisionAllowed
		}
		return DecisionRequiresConfirmation
	case PermissionNavigation:
		if c.Policy.AutoAllowNavigation {
			return DecisionAllowed
		}
		return DecisionRequiresConfirmation
	case PermissionLowRiskInteraction:
		if c.Policy.AutoAllowLowRiskInteraction {
			return DecisionAllowed
		}
		return DecisionRequiresConfirmation
	case PermissionWriteInteraction:
		return DecisionRequiresConfirmation
	case PermissionHighRiskTransaction:
		return DecisionRequiresExplicit
	default:
		return DecisionDenied
	}
}

func classifyAction(actionType ActionType) PermissionLevel {
	switch actionType {
	case ActionReadTitle, ActionReadURL, ActionReadVisibleText, ActionReadDOM, ActionReadAccessibility, ActionCaptureScreenshot, ActionLocateElement:
		return PermissionRead
	case ActionNavigate, ActionOpenTab, ActionBack, ActionForward, ActionReload:
		return PermissionNavigation
	case ActionScroll, ActionMoveMouse, ActionClick, ActionDoubleClick, ActionDrag, ActionMouseWheel, ActionFocusInput, ActionClickNavigation, ActionTypeText, ActionKeyboardShortcut:
		return PermissionLowRiskInteraction
	case ActionLoginSubmit, ActionSendMessage, ActionPostContent, ActionSaveSettings, ActionUploadFile, ActionSubmitForm:
		return PermissionWriteInteraction
	case ActionPayment, ActionPurchase, ActionDeleteData, ActionCloseAccount, ActionOAuthGrant, ActionAcceptLegalTerms:
		return PermissionHighRiskTransaction
	default:
		return PermissionHighRiskTransaction
	}
}

func isNavigation(level PermissionLevel) bool {
	return level == PermissionNavigation
}
