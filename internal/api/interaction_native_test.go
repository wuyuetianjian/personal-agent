package api

import (
	"agent/internal/model"
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"sync/atomic"
	"testing"
)

func TestNativeStatusWireUnchanged(t *testing.T) {
	rt, err := OpenInteractionRuntime(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var streams, resolutions atomic.Int32
	selection := testSelection()
	selection.Provider = interactionStreamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		streams.Add(1)
		t.Error("status invoked provider Stream")
		return model.TurnResult{}, nil
	})
	s, err := NewInteractionService(rt, InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: t.TempDir(), DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { resolutions.Add(1); return selection, nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	counts := func() []int {
		var out []int
		for _, table := range []string{"interaction_mcp_admissions", "interaction_sessions", "interaction_api_sessions", "interaction_workspaces", "interaction_runs"} {
			var n int
			if e := rt.DB.SQL.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
				t.Fatal(e)
			}
			out = append(out, n)
		}
		return out
	}
	before := counts()
	for i := 0; i < 3; i++ {
		response := apiRequest(s, "GET", "/api/v1/status", "")
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		var raw struct {
			Workspace map[string]map[string]json.RawMessage `json:"workspace"`
		}
		if err = json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
			t.Fatal(err)
		}
		keys := func(m map[string]map[string]json.RawMessage) []string {
			var k []string
			for key := range m {
				k = append(k, key)
			}
			sort.Strings(k)
			return k
		}
		if !reflect.DeepEqual(keys(raw.Workspace), []string{"Exec", "Files"}) {
			t.Fatal(raw.Workspace)
		}
		for name, cap := range raw.Workspace {
			if len(cap) != 2 || cap["Enabled"] == nil || cap["Reason"] == nil {
				t.Fatal(name, cap)
			}
			var enabled bool
			var reason string
			if json.Unmarshal(cap["Enabled"], &enabled) != nil || json.Unmarshal(cap["Reason"], &reason) != nil {
				t.Fatal(cap)
			}
		}
	}
	if !reflect.DeepEqual(before, counts()) || streams.Load() != 0 || resolutions.Load() != 3 {
		t.Fatalf("before=%v after=%v streams=%d resolutions=%d", before, counts(), streams.Load(), resolutions.Load())
	}
}
