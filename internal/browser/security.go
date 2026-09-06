package browser

import (
	"net/url"
	"strings"

	"agent/internal/security"
)

type SecurityFinding string

const (
	FindingCrossDomainNavigation SecurityFinding = "cross_domain_navigation"
	FindingUnsafeDownloadPath    SecurityFinding = "unsafe_download_path"
	FindingCredentialPage        SecurityFinding = "credential_page"
	FindingUnexpectedPopup       SecurityFinding = "unexpected_popup"
	FindingOAuth                 SecurityFinding = "oauth"
	FindingPromptInjection       SecurityFinding = "prompt_injection"
)

type BrowserSecurityPolicy struct {
	Network      security.NetworkPolicy
	DownloadRoot string
}

func (p BrowserSecurityPolicy) CheckNavigation(from, to string) []SecurityFinding {
	var findings []SecurityFinding
	if err := p.Network.ValidateRedirect(from, to); err != nil {
		findings = append(findings, FindingCrossDomainNavigation)
	}
	parsed, _ := url.Parse(to)
	lower := strings.ToLower(parsed.Path + " " + parsed.RawQuery)
	if strings.Contains(lower, "login") || strings.Contains(lower, "password") || strings.Contains(lower, "credential") {
		findings = append(findings, FindingCredentialPage)
	}
	if strings.Contains(lower, "oauth") || strings.Contains(lower, "authorize") {
		findings = append(findings, FindingOAuth)
	}
	return findings
}

func (p BrowserSecurityPolicy) CheckDownload(path string) []SecurityFinding {
	if _, err := (security.PathPolicy{Root: p.DownloadRoot}).Resolve(path); err != nil {
		return []SecurityFinding{FindingUnsafeDownloadPath}
	}
	return nil
}

func CheckPageText(source, text string) []SecurityFinding {
	if len(security.DetectPromptInjection(source, text)) > 0 {
		return []SecurityFinding{FindingPromptInjection}
	}
	return nil
}
