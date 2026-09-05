package privacy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

var (
	ErrMissingHMACSecret = errors.New("privacy hmac secret is required")
	ErrPayloadBlocked    = errors.New("privacy gateway blocked credential material")
)

type Audit struct {
	Pseudonymized int
	Redacted      int
	Blocked       bool
	BlockReason   string
}

type Result struct {
	Text  string
	Audit Audit
}

type Gateway struct {
	secret []byte
}

func NewGateway(secret string) (*Gateway, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrMissingHMACSecret
	}
	return &Gateway{secret: []byte(secret)}, nil
}

func (g *Gateway) TransformText(input string) (Result, error) {
	if g == nil {
		return Result{}, ErrMissingHMACSecret
	}
	if reason := blockReason(input); reason != "" {
		return Result{Audit: Audit{Blocked: true, BlockReason: reason}}, ErrPayloadBlocked
	}
	output := input
	audit := Audit{}
	for _, pattern := range pseudonymPatterns {
		output = pattern.ReplaceAllStringFunc(output, func(value string) string {
			audit.Pseudonymized++
			return "[PSEUDONYM:" + g.digest(value) + "]"
		})
	}
	for _, rule := range redactionRules {
		matches := rule.FindAllString(output, -1)
		if len(matches) > 0 {
			audit.Redacted += len(matches)
			output = rule.ReplaceAllString(output, "[REDACTED]")
		}
	}
	return Result{Text: output, Audit: audit}, nil
}

func (g *Gateway) Pseudonymize(kind string, value string) string {
	return kind + ":" + g.digest(strings.ToLower(strings.TrimSpace(value)))
}

func (g *Gateway) digest(value string) string {
	mac := hmac.New(sha256.New, g.secret)
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func blockReason(input string) string {
	for _, rule := range blockRules {
		if rule.pattern.MatchString(input) {
			return rule.reason
		}
	}
	return ""
}

type blockRule struct {
	pattern *regexp.Regexp
	reason  string
}

var blockRules = []blockRule{
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*-----END [A-Z ]*PRIVATE KEY-----`), "private key dump"},
	{regexp.MustCompile(`(?is)(^|\n)\s*(cookie|set-cookie):\s*[^\n]+(\n\s*(cookie|set-cookie):\s*[^\n]+)+`), "raw cookie jar"},
	{regexp.MustCompile(`(?im)^\s*(AWS_ACCESS_KEY_ID|AWS_SECRET_ACCESS_KEY|GOOGLE_APPLICATION_CREDENTIALS|AZURE_CLIENT_SECRET)\s*=`), "cloud credential file"},
	{regexp.MustCompile(`(?im)^\s*(PASSWORD|TOKEN|API_KEY|SECRET|PRIVATE_KEY)\s*=`), ".env secret dump"},
	{regexp.MustCompile(`(?s)-----BEGIN OPENSSH PRIVATE KEY-----.*-----END OPENSSH PRIVATE KEY-----`), "ssh key material"},
}

var pseudonymPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`(?i)\b(hostname|host|username|user|user_id|org_id|organization_id|project_id|repo_id|repository_id)\s*[:=]\s*[A-Za-z0-9._@/\-]+`),
	regexp.MustCompile(`/Users/[^/\s"'<>]+`),
	regexp.MustCompile(`(?i)C:\\Users\\[^\\\s"'<>]+`),
}

var redactionRules = []*regexp.Regexp{
	regexp.MustCompile(`(?i)authorization:\s*(bearer|basic)\s+[a-z0-9._~+/=-]+`),
	regexp.MustCompile(`(?i)(cookie|set-cookie):\s*[^;\n]+(?:;[^\n]*)?`),
	regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|api[_-]?key|csrf|refresh_token)\s*[=:]\s*["']?[^"'\s&<>]+`),
	regexp.MustCompile(`(?i)(postgres|postgresql|mysql|mongodb|redis)://[^:\s/@]+:[^@\s]+@[^\s]+`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}
