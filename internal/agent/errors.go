package agent

type ErrorCategory string

const (
	ErrorRetryable           ErrorCategory = "retryable_error"
	ErrorBlockedMissingInput ErrorCategory = "blocked_missing_input"
	ErrorPermissionDenied    ErrorCategory = "permission_denied"
	ErrorPrivacyBlocked      ErrorCategory = "privacy_blocked"
	ErrorBudgetExceeded      ErrorCategory = "budget_exceeded"
	ErrorVerificationFailed  ErrorCategory = "verification_failed"
	ErrorBrowserRuntime      ErrorCategory = "browser_runtime_failed"
	ErrorModelUnavailable    ErrorCategory = "model_unavailable"
)
