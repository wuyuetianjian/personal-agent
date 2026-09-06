package security

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

var ErrNetworkDenied = errors.New("network egress denied")

type NetworkPolicy struct {
	AllowedDomains       []string
	AllowedSchemes       []string
	AllowPrivateNetworks bool
}

func (p NetworkPolicy) ValidateURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ErrNetworkDenied
	}
	if parsed.User != nil {
		return ErrNetworkDenied
	}
	if !p.schemeAllowed(parsed.Scheme) {
		return ErrNetworkDenied
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" || containsNUL(host) {
		return ErrNetworkDenied
	}
	if !p.AllowPrivateNetworks && isPrivateHost(host) {
		return ErrNetworkDenied
	}
	if len(p.AllowedDomains) > 0 && !domainAllowed(host, p.AllowedDomains) {
		return ErrNetworkDenied
	}
	return nil
}

func (p NetworkPolicy) ValidateRedirect(from, to string) error {
	if err := p.ValidateURL(from); err != nil {
		return err
	}
	return p.ValidateURL(to)
}

func (p NetworkPolicy) schemeAllowed(scheme string) bool {
	allowed := p.AllowedSchemes
	if len(allowed) == 0 {
		allowed = []string{"http", "https"}
	}
	for _, item := range allowed {
		if strings.EqualFold(item, scheme) {
			return true
		}
	}
	return false
}

func domainAllowed(host string, allowed []string) bool {
	for _, domain := range allowed {
		domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), ".")
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func isPrivateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
