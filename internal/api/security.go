package api

import "agent/internal/observability"

func redactForAPI(value string) string {
	return observability.Redact(value)
}
