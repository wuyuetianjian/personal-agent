package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"agent/internal/config"
	"agent/internal/observability"
	"agent/internal/runtime"
)

func TestP13ServeHealthReadinessAndMetricsShapes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	dbPath := filepath.Join(dir, "agent.db")
	writeConfig(t, configPath, dbPath)
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtime.Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	metrics := observability.NewRegistry()
	handler := http.NewServeMux()
	handler.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, http.StatusOK, observability.Health())
	})
	handler.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, http.StatusOK, observability.Readiness(r.Context(), rt.Storage.SQL, true, readinessDependencies(cfg)))
	})
	handler.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, http.StatusOK, metrics.Snapshot(r.Context(), rt.Storage.SQL))
	})

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body = %s", path, response.Code, response.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s returned invalid json: %v", path, err)
		}
	}
}
