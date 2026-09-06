package verification

import (
	"regexp"
	"strings"
)

var evidenceMarker = regexp.MustCompile(`(?i)\[evidence:([^\]]+)\]`)

func ExtractClaims(text string) []Claim {
	parts := splitClaimText(text)
	claims := make([]Claim, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		evidenceIDs := extractEvidenceIDs(part)
		clean := strings.TrimSpace(evidenceMarker.ReplaceAllString(part, ""))
		if clean == "" {
			continue
		}
		claims = append(claims, Claim{
			ID:          "claim_" + stableID(clean),
			Text:        clean,
			Confidence:  1,
			EvidenceIDs: evidenceIDs,
		})
	}
	return claims
}

func splitClaimText(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var parts []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "*"))
		if line == "" {
			continue
		}
		segments := strings.Split(line, ". ")
		for _, segment := range segments {
			parts = append(parts, strings.TrimSpace(strings.TrimSuffix(segment, ".")))
		}
	}
	return parts
}

func extractEvidenceIDs(text string) []string {
	match := evidenceMarker.FindStringSubmatch(text)
	if len(match) != 2 {
		return nil
	}
	rawIDs := strings.Split(match[1], ",")
	ids := make([]string, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id := strings.TrimSpace(raw)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func stableID(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r == ' ' || r == '-' || r == '_' {
			return '_'
		}
		return -1
	}, text)
	text = strings.Trim(text, "_")
	if len(text) > 48 {
		text = text[:48]
	}
	if text == "" {
		return "empty"
	}
	return text
}
