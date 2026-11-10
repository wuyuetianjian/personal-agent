package interaction

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"agent/internal/memory"
	"agent/internal/model"
	"agent/internal/rag"
	"agent/internal/runtime"
)

func TestContextProtectedConstraintsAndBudget(t *testing.T) {
	s, _, w, _ := workspaceFixture(t, ModeDefault)
	a := NewContextAssembler(s)
	got, err := a.Build(context.Background(), "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if got.Estimator != "conservative serialized-byte estimate" || got.EstimatedInput+1000 > 20000 {
		t.Fatal(got)
	}
	all := ""
	for _, m := range got.Messages {
		all += m.Content
	}
	for _, c := range append(w.Policy.SystemConstraints, w.Policy.UserConstraints...) {
		if !strings.Contains(all, c) {
			t.Fatal("constraint lost", c)
		}
	}
	if _, err = a.Build(context.Background(), "s", TokenBudget{Total: 10, Output: 1}); !errors.Is(err, ErrContextBudget) {
		t.Fatal(err)
	}
	for _, tool := range got.Tools {
		if tool.Function.Name == "exec" {
			t.Fatal("advertised unsupported exec")
		}
	}
}
func TestContextScopedRetrievalNeverLeaksLegacyOrOtherProject(t *testing.T) {
	s, _, w, _ := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	store := rag.NewSQLiteStore(s.db, rag.Chunker{MaxTokens: 100})
	revisions := map[string]string{}
	for _, id := range []string{"owned", "foreign", "legacy"} {
		indexed, err := store.IndexRevision(ctx, rag.Document{ID: id, Text: "note " + id + " document"})
		if err != nil {
			t.Fatal(err)
		}
		revisions[id] = indexed.Revision
	}
	if err := runtime.AssignDocumentProject(ctx, s.db, "owned", w.ScopeID, revisions["owned"]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssignDocumentProject(ctx, s.db, "foreign", "other-project", revisions["foreign"]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssignDocumentProject(ctx, s.db, "owned", "other-project", revisions["owned"]); err == nil {
		t.Fatal("document reassigned")
	}
	semantic := memory.NewSemanticStore(s.db, nil)
	for _, v := range []struct{ id, scope string }{{"own-memory", "project:" + w.ScopeID}, {"foreign-memory", "project:other"}, {"legacy-memory", "project"}} {
		if err := semantic.Upsert(ctx, memory.SemanticFact{ID: v.id, Scope: v.scope, Subject: "note", Predicate: "contains", Object: v.id}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	var text string
	for _, m := range got.Messages {
		text += m.Content
	}
	if !strings.Contains(text, "owned document") || strings.Contains(text, "foreign document") || strings.Contains(text, "legacy document") || strings.Contains(text, "legacy-memory") || strings.Contains(text, "foreign-memory") || !strings.Contains(text, "own-memory") {
		t.Fatal(text)
	}
	if len(got.Exclusions) == 0 {
		t.Fatal("no observable legacy exclusion")
	}
}
func TestContextLongToolArtifactAndIntactRounds(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeAcceptEdits)
	ctx := context.Background()
	// Construct a completed transcript through the real engine, using a bounded
	// fake broker solely to generate a long observation independent of file tools.
	call := ToolCall{ID: "read1", Name: "read", Arguments: `{"scope":"` + w.ScopeID + `","path":"note.txt"}`}
	long := strings.Repeat("untrusted tool text; ", 1000)
	fake := &testBroker{execute: func(context.Context) (ToolResult, error) { return ToolResult{Content: long}, nil }}
	turns := 0
	e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turns++
		if turns == 1 {
			return model.TurnResult{ToolCalls: []model.ToolCall{call}}, nil
		}
		return model.TurnResult{Content: "done"}, nil
	}), fake, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = b
	if err = e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for i, m := range got.Messages {
		if len(m.ToolCalls) > 0 {
			if i+1 >= len(got.Messages) || got.Messages[i+1].ToolCallID != m.ToolCalls[0].ID {
				t.Fatal("broken tool round")
			}
			if !strings.Contains(got.Messages[i+1].Content, "artifact_id") {
				t.Fatal("not artifact-backed")
			}
			found = true
		}
		if strings.Contains(m.Content, long) {
			t.Fatal("long output injected")
		}
	}
	if !found {
		t.Fatal("tool round unnecessarily omitted")
	}
	if len(got.Artifacts) == 0 {
		t.Fatal("no artifact sources")
	}
	raw, err := s.ReadArtifact(ctx, "s", got.Artifacts[0].ID)
	if err != nil || string(raw) != long {
		t.Fatal(len(raw), err)
	}
	if _, err = s.ReadArtifact(ctx, "another-session", got.Artifacts[0].ID); err == nil {
		t.Fatal("cross-session artifact leak")
	}
}
func TestContextGroupingRejectsOrphansAndPreservesUserAuthority(t *testing.T) {
	_, err := contextBundles([]model.ChatMessage{{Role: "tool", ToolCallID: "missing", Content: "fake"}})
	if err == nil {
		t.Fatal("accepted orphan")
	}
	_, err = contextBundles([]model.ChatMessage{{Role: "assistant", ToolCalls: []model.ChatToolCall{{ID: "a"}}}, {Role: "user", Content: "stop"}})
	if err == nil {
		t.Fatal("accepted partial tool round")
	}
}

func TestContextCompactionKeepsAllOriginalUserRestrictions(t *testing.T) {
	s, _, _, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	// First run contains an old user restriction and a large ordinary assistant
	// reply. Compaction may omit the latter, never the original user instruction.
	if _, err := s.db.Exec("UPDATE interaction_runs SET message=? WHERE id=?", `{"content":"Never modify the release directory."}`, id); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{Content: strings.Repeat("old assistant background ", 1500)}, nil
	}), nil, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 5000, Output: 500})
	if err != nil {
		t.Fatal(err)
	}
	preserved := false
	for _, m := range got.Messages {
		if m.Role == "user" && m.Content == "Never modify the release directory." {
			preserved = true
		}
		if strings.Contains(m.Content, "old assistant background") {
			t.Fatal("unbudgeted history")
		}
	}
	if !preserved || len(got.Artifacts) == 0 {
		t.Fatal("authority/original source lost")
	}
}
func TestContextRejectsStaleDocumentOwnership(t *testing.T) {
	s, _, w, _ := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	rs := rag.NewSQLiteStore(s.db, rag.Chunker{MaxTokens: 100})
	verified, err := rs.IndexRevision(ctx, rag.Document{ID: "reused", Text: "note safe"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssignDocumentProject(ctx, s.db, "reused", w.ScopeID, verified.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Index(ctx, rag.Document{ID: "reused", Text: "note FOREIGN replacement"}); err != nil {
		t.Fatal(err)
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Messages {
		if strings.Contains(m.Content, "FOREIGN") {
			t.Fatal("stale scope leaked replaced document")
		}
	}
}

func TestContextReservesPendingUserInputWithoutDuplicatingIt(t *testing.T) {
	s, _, _, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	if _, err := s.db.Exec("UPDATE interaction_runs SET message=? WHERE id=?", `{"content":"`+strings.Repeat("pending input ", 1000)+`"}`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 5000, Output: 500}); !errors.Is(err, ErrContextBudget) {
		t.Fatal("pending input was free", err)
	}
}

func TestContextDocumentSuppliedHashCannotPreserveOldOwnership(t *testing.T) {
	s, _, w, _ := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	rs := rag.NewSQLiteStore(s.db, rag.Chunker{MaxTokens: 100})
	verified, err := rs.IndexRevision(ctx, rag.Document{ID: "reused", Text: "note safe", ContentHash: "supplied-hash"})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.AssignDocumentProject(ctx, s.db, "reused", w.ScopeID, verified.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Index(ctx, rag.Document{ID: "reused", Text: "note FOREIGN replacement", ContentHash: "supplied-hash"}); err != nil {
		t.Fatal(err)
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range got.Messages {
		if strings.Contains(m.Content, "FOREIGN") {
			t.Fatal("source-supplied hash preserved stale scope")
		}
	}
}

func TestContextPreservesAdmittedAuthorityRegardlessOfRunOutcome(t *testing.T) {
	for _, outcome := range []string{"provider_failed", "cancelled_before_checkpoint", "cancelled_after_checkpoint", "completed"} {
		t.Run(outcome, func(t *testing.T) {
			s, _, _, id := workspaceFixture(t, ModeDefault)
			ctx := context.Background()
			original := "Never modify the release directory. " + strings.Repeat("Keep this original restriction. ", 300)
			raw, _ := json.Marshal(Message{Content: original})
			if _, err := s.db.Exec("UPDATE interaction_runs SET message=? WHERE id=?", string(raw), id); err != nil {
				t.Fatal(err)
			}
			provider := streamFunc(func(ctx context.Context, req model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
				switch outcome {
				case "provider_failed":
					if err := emit(model.ModelEvent{Kind: model.EventTextDelta, Text: "PARTIAL ASSISTANT"}); err != nil {
						return model.TurnResult{}, err
					}
					return model.TurnResult{}, errors.New("provider failed")
				case "cancelled_after_checkpoint":
					return toolTurn(), nil
				default:
					return model.TurnResult{Content: "finished"}, nil
				}
			})
			e, err := NewEngine(s, provider, &testBroker{approval: true}, EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "cancelled_before_checkpoint":
				r, err := s.GetRun(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.Transition(ctx, id, r.Version, RunCancelled); err != nil {
					t.Fatal(err)
				}
				if _, err = s.EngineCheckpoint(ctx, id); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("unexpected checkpoint", err)
				}
			case "cancelled_after_checkpoint":
				if err = e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
					t.Fatal(err)
				}
				if err = e.Cancel(ctx, id); err != nil {
					t.Fatal(err)
				}
				if err = e.Run(ctx, id); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "provider_failed":
				if err = e.Run(ctx, id); err == nil {
					t.Fatal("provider unexpectedly succeeded")
				}
			default:
				if err = e.Run(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			current, err := s.CreateRun(ctx, "s", "next", Message{Content: "Continue with the remaining work."})
			if err != nil {
				t.Fatal(err)
			}
			got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 50000, Output: 1000})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, m := range got.Messages {
				if m.Role == "user" && m.Content == original {
					count++
				}
				if m.Content == current.Message.Content {
					t.Fatal("duplicated pending input")
				}
				if m.Role == "tool" || len(m.ToolCalls) > 0 || strings.Contains(m.Content, "PARTIAL ASSISTANT") {
					t.Fatal("included incomplete protocol", m)
				}
			}
			if count != 1 {
				t.Fatalf("admitted original appears %d times", count)
			}
			sources := 0
			pendingSources := 0
			for _, source := range got.Sources {
				if source.Reference == "run:"+id && source.Original == original {
					sources++
				}
				if source.Reference == "run:"+current.ID && source.Original == current.Message.Content {
					pendingSources++
				}
			}
			if sources != 1 || pendingSources != 1 || got.PendingRunID != current.ID || got.ReservedCurrentInput <= 0 {
				t.Fatalf("source identity/reservation lost: %+v", got.Sources)
			}
			if _, err = NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 5000, Output: 500}); !errors.Is(err, ErrContextBudget) {
				t.Fatal("old authority not mandatory budget", err)
			}
		})
	}
}

func TestContextAdmittedMessagesKeepChronologyWithoutInjectedDuplicates(t *testing.T) {
	s, _, _, first := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	ids := []string{first}
	originals := []string{"note", "cancelled restriction", "failed restriction"}
	complete, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{Content: "completed assistant answer"}, nil
	}), nil, EngineOptions{Request: model.TurnRequest{ChatRequest: model.ChatRequest{Messages: []model.ChatMessage{{Role: "user", Content: "not an admitted message"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = complete.Run(ctx, first); err != nil {
		t.Fatal(err)
	}
	cancelled, err := s.CreateRun(ctx, "s", "cancelled", Message{Content: originals[1]})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, cancelled.ID)
	if _, err = s.Transition(ctx, cancelled.ID, cancelled.Version, RunCancelled); err != nil {
		t.Fatal(err)
	}
	failed, err := s.CreateRun(ctx, "s", "failed", Message{Content: originals[2]})
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, failed.ID)
	if _, err = s.Transition(ctx, failed.ID, failed.Version, RunFailed); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateRun(ctx, "s", "pending", Message{Content: "pending"}); err != nil {
		t.Fatal(err)
	}
	got, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	next := 0
	for _, m := range got.Messages {
		if m.Content == "not an admitted message" {
			t.Fatal("injected history duplicated")
		}
		for i, text := range originals {
			if m.Content == text {
				if i != next || m.Role != "user" {
					t.Fatal("admission order/role changed", got.Messages)
				}
				next++
			}
		}
	}
	if next != len(originals) {
		t.Fatal("admitted input missing")
	}
	next = 0
	for _, source := range got.Sources {
		if source.Kind == "protected original user message" {
			if next >= len(ids) || source.Reference != "run:"+ids[next] || source.Original != originals[next] {
				t.Fatal("source order/identity changed", got.Sources)
			}
			next++
		}
	}
	if next != len(ids) {
		t.Fatal("source missing")
	}
}
