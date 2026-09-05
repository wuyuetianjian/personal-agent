package browser

import "testing"

func TestPermissionClassifierDecide(t *testing.T) {
	classifier := PermissionClassifier{
		Policy: PermissionPolicy{
			AutoAllowRead:            true,
			AutoAllowNavigation:      true,
			RequireAllowlistedDomain: true,
		},
	}

	cases := []struct {
		name        string
		action      Action
		allowlisted bool
		want        PermissionDecision
	}{
		{
			name:        "read auto allowed",
			action:      Action{Type: ActionReadVisibleText},
			allowlisted: true,
			want:        DecisionAllowed,
		},
		{
			name:        "navigation denied outside allowlist",
			action:      Action{Type: ActionNavigate},
			allowlisted: false,
			want:        DecisionDenied,
		},
		{
			name:        "write requires confirmation",
			action:      Action{Type: ActionSendMessage},
			allowlisted: true,
			want:        DecisionRequiresConfirmation,
		},
		{
			name:        "high risk requires explicit confirmation",
			action:      Action{Type: ActionPayment},
			allowlisted: true,
			want:        DecisionRequiresExplicit,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifier.Decide(tc.action, tc.allowlisted); got != tc.want {
				t.Fatalf("Decide() = %q, want %q", got, tc.want)
			}
		})
	}
}
