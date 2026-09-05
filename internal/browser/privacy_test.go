package browser

import (
	"strings"
	"testing"
)

func TestPrivacyGatewayRedact(t *testing.T) {
	gateway := PrivacyGateway{}
	input := `Authorization: Bearer abc.def token=secret123 password=hunter2 email alice@example.com ip 10.0.0.1 path /Users/alice/Library/Profile`

	got := gateway.Redact(input)

	blocked := []string{
		"abc.def",
		"secret123",
		"hunter2",
		"alice@example.com",
		"10.0.0.1",
		"/Users/alice",
	}
	for _, value := range blocked {
		if strings.Contains(got, value) {
			t.Fatalf("Redact() leaked %q in %q", value, got)
		}
	}
}
