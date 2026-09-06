package browser

import "strings"

func summarizeText(text string, limit int) string {
	normalized := strings.Join(strings.Fields(text), " ")
	if limit > 0 && len(normalized) > limit {
		return normalized[:limit]
	}
	return normalized
}
