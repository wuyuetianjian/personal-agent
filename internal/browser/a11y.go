package browser

import (
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
)

func SummarizeA11yNodes(nodes []*accessibility.Node, limit int) string {
	var builder strings.Builder
	for _, node := range nodes {
		if node == nil || node.Ignored {
			continue
		}
		role := axValue(node.Role)
		name := axValue(node.Name)
		if role == "" && name == "" {
			continue
		}
		line := strings.TrimSpace(fmt.Sprintf("%s %s", role, name))
		if line == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
		if limit > 0 && builder.Len() >= limit {
			return builder.String()[:limit]
		}
	}
	return builder.String()
}

func axValue(value *accessibility.Value) string {
	if value == nil || value.Value == nil {
		return ""
	}
	return fmt.Sprint(value.Value)
}
