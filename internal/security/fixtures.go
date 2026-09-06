package security

import (
	"strings"

	"agent/internal/observability"
)

var SecretLeakFixtures = []string{
	"api_key=sk-test-secret",
	"token=hidden-token",
	"Cookie: session=secret-cookie",
	"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----",
	"AWS_SECRET_ACCESS_KEY=secret",
	"password=supersecret",
}

func ContainsRawSecret(value string) bool {
	redacted := observability.Redact(value)
	for _, fixture := range SecretLeakFixtures {
		for _, part := range strings.FieldsFunc(fixture, func(r rune) bool {
			return r == '=' || r == ':' || r == '\n' || r == ' '
		}) {
			part = strings.TrimSpace(part)
			if len(part) >= 6 && strings.Contains(redacted, part) {
				return true
			}
		}
	}
	return false
}
