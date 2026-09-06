package browser

import "time"

type ActionType string

const (
	ActionReadTitle         ActionType = "read_title"
	ActionReadURL           ActionType = "read_url"
	ActionReadVisibleText   ActionType = "read_visible_text"
	ActionReadDOM           ActionType = "read_dom"
	ActionReadAccessibility ActionType = "read_accessibility"
	ActionCaptureScreenshot ActionType = "capture_screenshot"
	ActionNavigate          ActionType = "navigate"
	ActionOpenTab           ActionType = "open_tab"
	ActionBack              ActionType = "back"
	ActionForward           ActionType = "forward"
	ActionReload            ActionType = "reload"
	ActionLocateElement     ActionType = "locate_element"
	ActionScroll            ActionType = "scroll"
	ActionMoveMouse         ActionType = "move_mouse"
	ActionClick             ActionType = "click"
	ActionDoubleClick       ActionType = "double_click"
	ActionDrag              ActionType = "drag"
	ActionMouseWheel        ActionType = "mouse_wheel"
	ActionFocusInput        ActionType = "focus_input"
	ActionClickNavigation   ActionType = "click_navigation"
	ActionTypeText          ActionType = "type_text"
	ActionKeyboardShortcut  ActionType = "keyboard_shortcut"
	ActionLoginSubmit       ActionType = "login_submit"
	ActionSendMessage       ActionType = "send_message"
	ActionPostContent       ActionType = "post_content"
	ActionSaveSettings      ActionType = "save_settings"
	ActionUploadFile        ActionType = "upload_file"
	ActionSubmitForm        ActionType = "submit_form"
	ActionPayment           ActionType = "payment"
	ActionPurchase          ActionType = "purchase"
	ActionDeleteData        ActionType = "delete_data"
	ActionCloseAccount      ActionType = "close_account"
	ActionOAuthGrant        ActionType = "oauth_grant"
	ActionAcceptLegalTerms  ActionType = "accept_legal_terms"
)

type ControlMode string

const (
	ControlModeSemantic   ControlMode = "semantic"
	ControlModeCoordinate ControlMode = "coordinate"
)

type PermissionLevel int

const (
	PermissionRead PermissionLevel = iota
	PermissionNavigation
	PermissionLowRiskInteraction
	PermissionWriteInteraction
	PermissionHighRiskTransaction
)

type PermissionDecision string

const (
	DecisionAllowed              PermissionDecision = "allowed"
	DecisionDenied               PermissionDecision = "denied"
	DecisionRequiresConfirmation PermissionDecision = "requires_confirmation"
	DecisionRequiresExplicit     PermissionDecision = "requires_explicit_confirmation"
)

type ReusePolicy string

const (
	ReuseWhenAllowed ReusePolicy = "reuse_when_allowed"
	ReuseNever       ReusePolicy = "never"
)

type IsolationPolicy string

const (
	IsolationShared    IsolationPolicy = "shared"
	IsolationPerDomain IsolationPolicy = "per_domain"
	IsolationPerTask   IsolationPolicy = "per_task"
)

type StoragePolicy string

const (
	StorageLocalOnly StoragePolicy = "local_only"
)

type Target struct {
	Role        string
	Text        string
	Selector    string
	Description string
	X           int
	Y           int
}

type Action struct {
	Type           ActionType
	TaskID         string
	AgentID        string
	SessionID      string
	Domain         string
	URL            string
	Mode           ControlMode
	Target         Target
	Input          string
	ConfirmationID string
	FallbackReason string
}

type Observation struct {
	URL          string
	Title        string
	VisibleText  string
	DOMSummary   string
	A11ySummary  string
	ScreenshotID string
}

type Result struct {
	Action      Action
	Observation Observation
	Decision    PermissionDecision
	Error       *ActionError
	RecordedAt  time.Time
}

type ActionError struct {
	Category string
	Message  string
}
