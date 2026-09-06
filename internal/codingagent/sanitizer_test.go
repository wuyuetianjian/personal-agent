package codingagent

import (
	"strings"
	"testing"
)

func TestSanitizedEnvironmentStripsSecrets(t *testing.T) {
	t.Setenv("PACHAT_TEST_API_KEY", "secret")
	t.Setenv("PACHAT_TEST_TOKEN", "secret")
	t.Setenv("PACHAT_TEST_SAFE", "value")

	env := SanitizedEnvironment([]string{"EXTRA_TOKEN=secret", "EXTRA_SAFE=value"})
	joined := strings.Join(env, "\n")
	for _, forbidden := range []string{"PACHAT_TEST_API_KEY", "PACHAT_TEST_TOKEN", "EXTRA_TOKEN"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("sanitized env contains %s: %q", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "EXTRA_SAFE=value") {
		t.Fatalf("sanitized env missing explicit safe variable: %q", joined)
	}
}
