package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPublicSkillContextThenChatAndQueue(t *testing.T) {
	for _, next := range []string{"messages", "queue"} {
		t.Run(next, func(t *testing.T) {
			requests := make(chan model.TurnRequest, 4)
			selection := testSelection()
			selection.Provider = interactionStreamFunc(func(_ context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
				requests <- r
				return model.TurnResult{Content: "fixture reply"}, nil
			})
			s, rt := serviceFixture(t, selection)
			if !s.broker.Capabilities().Files.Enabled {
				t.Skip("native unavailable")
			}
			session := namedSession(t, s, "context-skill", "plan")
			base := "/api/v1/sessions/" + session
			initial := apiRequest(s, "POST", base+"/messages", `{"request_id":"restriction","content":"Never disclose private files."}`)
			if initial.Code != 202 {
				t.Fatal(initial.Code, initial.Body.String())
			}
			awaitState(t, s, session, "completed")
			<-requests
			cat, rev := seedPublicRunSkill(t, s, session)
			if err := os.WriteFile(filepath.Join(s.options.Workspace, "README.md"), []byte("owned evidence"), 0600); err != nil {
				t.Fatal(err)
			}
			p := previewPublicRun(t, s, session, cat, rev, "context-preview", `{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"owned"}}`)
			w := apiRequest(s, "POST", base+"/skill-runs/decision", fmt.Sprintf(`{"request_id":"context-confirm","operation_id":"context-preview","digest":%q,"decision":"confirm"}`, p.Digest))
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var admitted interaction.SkillRunOperationV2
			if err := json.Unmarshal(w.Body.Bytes(), &admitted); err != nil {
				t.Fatal(err)
			}
			awaitState(t, s, session, "completed")
			beforeView, err := s.store.SkillRunWithHooks(t.Context(), session, admitted.RunID)
			before := beforeView.V2
			if err != nil || before.Execution != "completed" {
				t.Fatal(before, err)
			}
			cp, err := s.store.EngineCheckpoint(t.Context(), admitted.RunID)
			if err != nil || len(cp.Messages) != 0 || cp.Action != "skill_readonly_v1" {
				t.Fatal(cp, err)
			}
			if _, err = interaction.NewContextAssembler(s.store).Build(t.Context(), session, interaction.TokenBudget{Total: 32000, Output: 2048}); err != nil {
				t.Error("context assembly", err)
			}
			w = apiRequest(s, "GET", base+"/context", "")
			if w.Code != 200 {
				t.Error("context GET", w.Code, w.Body.String())
			}
			view := apiRequest(s, "GET", base+"/context-view", "")
			if view.Code != 200 || strings.Contains(view.Body.String(), "Explicit read-only") || !strings.Contains(view.Body.String(), "Never disclose private files.") {
				t.Error("context inspection", view.Code, view.Body.String())
			}
			w = apiRequest(s, "POST", base+"/"+next, `{"request_id":"after-skill","content":"hello after skill"}`)
			if w.Code != 202 {
				t.Fatal("ordinary input after Skill", w.Code, w.Body.String())
			}
			select {
			case got := <-requests:
				raw, _ := json.Marshal(got.Messages)
				if strings.Contains(string(raw), "Explicit read-only") || !strings.Contains(string(raw), "Never disclose private files.") || !strings.Contains(string(raw), "hello after skill") {
					t.Fatal("provider transcript", string(raw))
				}
			case <-time.After(3 * time.Second):
				t.Fatal("queued model preflight did not dispatch", w.Body.String())
			}
			awaitState(t, s, session, "completed")
			afterView, err := s.store.SkillRunWithHooks(t.Context(), session, admitted.RunID)
			after := afterView.V2
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("Skill evidence changed", err)
			}
			gotCP, err := s.store.EngineCheckpoint(t.Context(), admitted.RunID)
			if err != nil || !reflect.DeepEqual(cp, gotCP) {
				t.Fatal("Skill checkpoint converted", err)
			}
			var sessions, runs int
			if err = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_sessions`).Scan(&sessions); err != nil {
				t.Fatal(err)
			}
			if err = rt.DB.SQL.QueryRow(`SELECT count(*) FROM interaction_runs WHERE session_id=?`, session).Scan(&runs); err != nil {
				t.Fatal(err)
			}
			if sessions != 1 || runs != 3 {
				t.Fatal("hidden session or duplicated run", sessions, runs)
			}
		})
	}
}

func TestPublicSkillContextWithInternalHooksThenChatAndQueue(t *testing.T) {
	for _, next := range []string{"messages", "queue"} {
		for _, deny := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/deny=%v", next, deny), func(t *testing.T) {
				requests := make(chan model.TurnRequest, 4)
				selection := testSelection()
				selection.Provider = interactionStreamFunc(func(_ context.Context, r model.TurnRequest, _ func(model.ModelEvent) error) (model.TurnResult, error) {
					requests <- r
					return model.TurnResult{Content: "owned reply"}, nil
				})
				s, _ := serviceFixture(t, selection)
				if !s.broker.Capabilities().Files.Enabled {
					t.Skip("native unavailable")
				}
				session := namedSession(t, s, "hook-context", "plan")
				base := "/api/v1/sessions/" + session
				w := apiRequest(s, "POST", base+"/messages", `{"request_id":"real","content":"Never disclose private files."}`)
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				awaitState(t, s, session, "completed")
				<-requests
				cat, rev := seedPublicRunSkill(t, s, session)
				effect := "audit"
				if deny {
					effect = "deny"
				}
				policy := fmt.Sprintf(`{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[{"id":"owned","event":"before_node","node_ids":[],"capabilities":[],"statuses":[],"effect":%q}]}`, effect)
				p, e := s.store.PrepareHooks(t.Context(), session, interaction.HookIntent{RequestID: "policy", Kind: "import", PolicyText: policy})
				if e != nil {
					t.Fatal(e)
				}
				a, e := s.store.DecideHooks(t.Context(), session, interaction.OperatorDecision{RequestID: "policy-c", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
				if e != nil {
					t.Fatal(e)
				}
				p, e = s.store.PrepareHooks(t.Context(), session, interaction.HookIntent{RequestID: "enable", Kind: "enable", RevisionID: a.RevisionID})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = s.store.DecideHooks(t.Context(), session, interaction.OperatorDecision{RequestID: "enable-c", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}); e != nil {
					t.Fatal(e)
				}
				os.WriteFile(filepath.Join(s.options.Workspace, "README.md"), []byte("owned"), 0600)
				preview, e := s.store.PrepareSkillRunWithHooks(t.Context(), session, interaction.SkillRunIntent{RequestID: "v2", CatalogID: cat, RevisionID: rev, ExecutionProfile: interaction.SkillRunProfile, Inputs: json.RawMessage(`{"readme":{"path":"README.md"},"search":{"paths":["README.md"],"query":"owned"}}`)})
				if e != nil {
					t.Fatal(e)
				}
				admitted, e := s.store.DecideSkillRunWithHooks(t.Context(), session, interaction.OperatorDecision{RequestID: "v2-c", OperationID: preview.OperationID, Digest: preview.Digest, Decision: "confirm"})
				if e != nil {
					t.Fatal(e)
				}
				engine, e := interaction.NewSkillRunEngine(s.store, admitted.V2.RunID)
				if e != nil {
					t.Fatal(e)
				}
				e = engine.Run(t.Context(), admitted.V2.RunID)
				if (!deny && e != nil) || (deny && !errors.Is(e, interaction.ErrHookDenied)) {
					t.Fatal(e)
				}
				view := apiRequest(s, "GET", base+"/context-view", "")
				if view.Code != 200 || strings.Contains(view.Body.String(), "Explicit read-only") || strings.Contains(view.Body.String(), "declarative-hooks") || !strings.Contains(view.Body.String(), "Never disclose private files.") {
					t.Fatal(view.Code, view.Body.String())
				}
				w = apiRequest(s, "POST", base+"/"+next, `{"request_id":"after","content":"ordinary next input"}`)
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				select {
				case request := <-requests:
					raw, _ := json.Marshal(request.Messages)
					if strings.Contains(string(raw), "Explicit read-only") || !strings.Contains(string(raw), "Never disclose private files.") || !strings.Contains(string(raw), "ordinary next input") {
						t.Fatal(string(raw))
					}
				case <-time.After(3 * time.Second):
					t.Fatal("no same-session model dispatch")
				}
				awaitState(t, s, session, "completed")
			})
		}
	}
}
