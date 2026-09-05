package privacy

import (
	"errors"
	"strings"
	"testing"
)

func TestPseudonymizeStable(t *testing.T) {
	gateway, err := NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	first := gateway.Pseudonymize("email", "Alice@example.com")
	second := gateway.Pseudonymize("email", "alice@example.com")
	if first != second {
		t.Fatalf("Pseudonymize() = %q then %q, want stable value", first, second)
	}
	if strings.Contains(first, "alice") || strings.Contains(first, "example") {
		t.Fatalf("Pseudonymize() leaked source value: %q", first)
	}
}

func TestTransformTextRedactsSecretsAndPseudonymizesIdentifiers(t *testing.T) {
	gateway, err := NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	input := `email alice@example.com host=prod-db-1 username=sunny Authorization: Bearer abc.def token=secret123 password=hunter2 db postgres://user:pass@localhost/app Cookie: sid=abc123`

	result, err := gateway.TransformText(input)
	if err != nil {
		t.Fatalf("TransformText() error = %v", err)
	}
	blocked := []string{"alice@example.com", "prod-db-1", "username=sunny", "abc.def", "secret123", "hunter2", "user:pass", "sid=abc123"}
	for _, value := range blocked {
		if strings.Contains(result.Text, value) {
			t.Fatalf("TransformText() leaked %q in %q", value, result.Text)
		}
	}
	if result.Audit.Pseudonymized == 0 || result.Audit.Redacted == 0 {
		t.Fatalf("TransformText() audit = %+v, want pseudonymized and redacted counts", result.Audit)
	}
}

func TestTransformTextBlocksCredentialDumps(t *testing.T) {
	gateway, err := NewGateway("test-secret")
	if err != nil {
		t.Fatalf("NewGateway() error = %v", err)
	}
	fixtures := []string{
		"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
		"Cookie: a=b\nCookie: c=d",
		"AWS_SECRET_ACCESS_KEY=abc123",
		"PASSWORD=hunter2\nTOKEN=abc123",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
	}
	for _, fixture := range fixtures {
		result, err := gateway.TransformText(fixture)
		if !errors.Is(err, ErrPayloadBlocked) {
			t.Fatalf("TransformText(%q) error = %v, want %v", fixture, err, ErrPayloadBlocked)
		}
		if !result.Audit.Blocked || result.Audit.BlockReason == "" {
			t.Fatalf("TransformText(%q) audit = %+v, want block metadata", fixture, result.Audit)
		}
	}
}
