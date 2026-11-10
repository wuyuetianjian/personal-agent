package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatOnlyDisplayFixturePreservesAuthAndNonStatus(t *testing.T) {
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/v1/status", 401}, {"/api/v1/status", 200}, {"/api/v1/sessions", 200}} {
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"protocol":"personal-agent-rebuild-v1","workspace":{"Files":{"Enabled":true}}}`))
		})
		out := httptest.NewRecorder()
		chatOnlyDisplayFixture(next).ServeHTTP(out, httptest.NewRequest("GET", tc.path, nil))
		if out.Code != tc.status || out.Header().Get("Content-Security-Policy") != "default-src 'none'" {
			t.Fatal(out)
		}
		var data struct {
			Workspace struct {
				Files struct {
					Enabled bool
					Reason  string
				}
			}
		}
		if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if tc.path == "/api/v1/status" && tc.status == 200 {
			if data.Workspace.Files.Enabled || data.Workspace.Files.Reason == "" {
				t.Fatal(data)
			}
		} else if !data.Workspace.Files.Enabled {
			t.Fatal("changed unauthorized or non-status response")
		}
	}
}
