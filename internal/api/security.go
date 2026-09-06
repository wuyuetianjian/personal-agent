package api

import "regexp"

var redactionRules = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization:\s*(bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(cookie|set-cookie):\s*[^;\n]+(?:;[^\n]*)?`),
	regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[_-]?key|csrf|refresh_token)\s*[=:]\s*["']?[^"'\s&<>]+`),
	regexp.MustCompile(`(?i)(postgres|postgresql|mysql|mongodb|redis)://[^:\s/@]+:[^@\s]+@[^\s]+`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}
