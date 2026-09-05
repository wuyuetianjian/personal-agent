package browser

import "regexp"

type PrivacyGateway struct {
	Replacement string
}

func (g PrivacyGateway) Redact(input string) string {
	replacement := g.Replacement
	if replacement == "" {
		replacement = "[REDACTED]"
	}

	output := input
	for _, pattern := range redactionPatterns {
		output = pattern.ReplaceAllString(output, replacement)
	}
	return output
}

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization:\s*bearer\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(cookie|set-cookie):\s*[^;\n]+`),
	regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[_-]?key|csrf)[=:]\s*["']?[^"'\s&<>]+`),
	regexp.MustCompile(`(?i)<input[^>]*(password|passwd|pwd|token|secret|api[_-]?key|csrf)[^>]*value=["'][^"']+["'][^>]*>`),
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`),
	regexp.MustCompile(`/Users/[^/\s]+/[^\s"'<>]*`),
	regexp.MustCompile(`(?i)C:\\Users\\[^\\\s]+\\[^\s"'<>]*`),
}
