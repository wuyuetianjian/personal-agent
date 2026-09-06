package security

import "strings"

type InjectionFinding struct {
	Source string `json:"source"`
	Rule   string `json:"rule"`
}

func DetectPromptInjection(source, text string) []InjectionFinding {
	lower := strings.ToLower(text)
	rules := map[string][]string{
		"override_instructions": {"ignore previous", "ignore all previous", "system prompt", "developer message"},
		"secret_exfiltration":   {"reveal", "exfiltrate", "api key", "password", "token"},
		"policy_bypass":         {"disable safety", "bypass policy", "do not ask permission", "act as root"},
	}
	var findings []InjectionFinding
	for rule, needles := range rules {
		for _, needle := range needles {
			if strings.Contains(lower, needle) {
				findings = append(findings, InjectionFinding{Source: source, Rule: rule})
				break
			}
		}
	}
	return findings
}

func IsUntrustedSource(source string) bool {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "rag", "web_page", "mcp", "tool_stdout", "coding_agent_stdout", "memory", "event", "skill_import":
		return true
	default:
		return false
	}
}
