package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"encoding/json"
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
			var admitted interaction.SkillRunOperation
			if err := json.Unmarshal(w.Body.Bytes(), &admitted); err != nil {
				t.Fatal(err)
			}
			awaitState(t, s, session, "completed")
			before, err := s.store.SkillRun(t.Context(), session, admitted.RunID)
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
			after, err := s.store.SkillRun(t.Context(), session, admitted.RunID)
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
