//go:build darwin || windows

package api

import (
	"agent/internal/interaction"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTrustedWorkspaceNativeSessionAndHostRevocation(t *testing.T) {
	ctx := context.Background()
	rt, e := OpenInteractionRuntime(ctx, t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer rt.Close()
	root := t.TempDir()
	if e = os.WriteFile(filepath.Join(root, "note.txt"), []byte("trusted native fixture"), 0600); e != nil {
		t.Fatal(e)
	}
	p := testSelection()
	p.ToolsSupported = true
	opts := InteractionOptions{Token: "secret-token", Host: "127.0.0.1:7319", Workspace: root, TrustedWorkspace: true, DefaultModel: "test", Resolve: func(context.Context, string) (ProviderSelection, error) { return p, nil }}
	s, e := NewInteractionService(rt, opts)
	if e != nil {
		t.Fatalf("TRUSTED_INTEGRATION_PENDING_QUALIFICATION: %v", e)
	}
	defer func() { s.Close() }()
	w := apiRequest(s, "POST", "/api/v1/sessions", `{"request_id":"trusted-native","mode":"default","trusted_workspace":true}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var session SessionView
	if e = json.Unmarshal(w.Body.Bytes(), &session); e != nil {
		t.Fatal(e)
	}
	if !session.TrustedWorkspace || session.TrustedWorkspaceWarning == "" || session.NonSandboxExec {
		t.Fatalf("incorrect trusted view: %+v", session)
	}
	bound, e := s.store.Workspace(ctx, session.ID)
	if e != nil {
		t.Fatal(e)
	}
	turn, e := interaction.NewContextAssembler(s.store).Build(ctx, session.ID, interaction.TokenBudget{Total: 32000, Output: 1000})
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for _, tool := range turn.Tools {
		names = append(names, tool.Function.Name)
	}
	if !reflect.DeepEqual(names, []string{"read", "search"}) {
		t.Fatalf("trusted tool authority: %v", names)
	}
	run, e := s.store.CreateRun(ctx, session.ID, "read", interaction.Message{Content: "read fixture"})
	if e != nil {
		t.Fatal(e)
	}
	args, _ := json.Marshal(interaction.ReadArguments{Scope: bound.ScopeID, Path: "note.txt"})
	call := interaction.ToolCall{ID: "read-one", Name: "read", Arguments: string(args)}
	if _, e = s.broker.Propose(ctx, run.ID, call); e != nil {
		t.Fatalf("trusted native read proposal: %v", e)
	}
	if _, e = s.broker.Propose(ctx, run.ID, interaction.ToolCall{ID: "forged-write", Name: "apply_patch", Arguments: `{}`}); e == nil {
		t.Fatal("trusted adapter allowed patch")
	}
	if _, e = s.store.PrepareKnowledge(ctx, session.ID, interaction.KnowledgeIntent{RequestID: "baseline-ingest", Kind: "ingest", SourceKind: "file", Source: "note.txt"}); e != nil {
		t.Fatalf("baseline knowledge read: %v", e)
	}
	s.Close()
	for _, change := range []string{"host-off", "root-changed"} {
		changed := opts
		if change == "host-off" {
			changed.TrustedWorkspace = false
		} else {
			changed.Workspace = t.TempDir()
		}
		s, e = NewInteractionService(rt, changed)
		if e != nil {
			t.Fatal(change, e)
		}
		if view := apiRequest(s, "GET", "/api/v1/sessions/"+session.ID+"/snapshot", ""); view.Code != 200 {
			t.Fatalf("history unavailable after %s: %d %s", change, view.Code, view.Body.String())
		}
		turn, e = interaction.NewContextAssembler(s.store).Build(ctx, session.ID, interaction.TokenBudget{Total: 32000, Output: 1000})
		if e == nil && len(turn.Tools) != 0 {
			t.Fatalf("%s retained tools: %v", change, turn.Tools)
		}
		fresh, fe := s.store.CreateRun(ctx, session.ID, "after-"+change, interaction.Message{Content: "check host revocation"})
		if fe != nil {
			t.Fatal(fe)
		}
		if _, e = s.broker.Propose(ctx, fresh.ID, call); !errors.Is(e, interaction.ErrCapabilityUnavailable) {
			t.Fatalf("%s did not specifically refuse host read authority: %v", change, e)
		}
		if _, e = s.store.PrepareKnowledge(ctx, session.ID, interaction.KnowledgeIntent{RequestID: change, Kind: "ingest", SourceKind: "file", Source: "note.txt"}); !errors.Is(e, interaction.ErrCapabilityUnavailable) {
			t.Fatalf("%s did not specifically refuse host knowledge read: %v", change, e)
		}
		s.Close()
	}
	s, e = NewInteractionService(rt, opts)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := s.store.CreateRun(ctx, session.ID, "host-restored", interaction.Message{Content: "restored host positive control"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.broker.Propose(ctx, restored.ID, call); e != nil {
		t.Fatalf("restored matching host cannot read: %v", e)
	}
	if _, e = s.store.PrepareKnowledge(ctx, session.ID, interaction.KnowledgeIntent{RequestID: "host-restored-knowledge", Kind: "ingest", SourceKind: "file", Source: "note.txt"}); e != nil {
		t.Fatalf("restored knowledge positive control: %v", e)
	}

}
