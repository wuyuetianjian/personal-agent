package apifixture

import (
	"agent/internal/model"
	"context"
	"testing"
)

func TestDeterministicFixtureStreamsAndOffersEdit(t *testing.T) {
	p := Provider{}
	text := ""
	r, e := p.Stream(context.Background(), model.TurnRequest{ChatRequest: model.ChatRequest{Messages: []model.ChatMessage{{Role: "system", Content: "Workspace scope: scope. Permission mode: default."}, {Role: "user", Content: "edit"}}}, Tools: []model.ToolDefinition{{}}}, func(ev model.ModelEvent) error { text += ev.Text; return nil })
	if e != nil || text == "" || len(r.ToolCalls) != 1 {
		t.Fatal(r, e, text)
	}
}

func TestExecFixtureRequiresOwnedWorkspaceAndAdvertisedTool(t *testing.T) {
	for _, tc := range []struct {
		workspace  string
		advertised bool
		want       bool
	}{{"", true, false}, {t.TempDir(), false, false}, {t.TempDir(), true, true}} {
		p := Provider{ExecWorkspace: tc.workspace}
		tools := []model.ToolDefinition{}
		if tc.advertised {
			tools = append(tools, model.ToolDefinition{Function: model.ToolFunction{Name: "exec"}})
		}
		r, err := p.Stream(context.Background(), model.TurnRequest{ChatRequest: model.ChatRequest{Messages: []model.ChatMessage{{Role: "system", Content: "Workspace scope: scope. Permission mode: default."}, {Role: "user", Content: "exec"}}}, Tools: tools}, func(model.ModelEvent) error { return nil })
		if err != nil || (len(r.ToolCalls) == 1) != tc.want {
			t.Fatalf("got %+v %v; want exec %v", r, err, tc.want)
		}
	}
}

func TestMCPFixtureOnlyCallsAdvertisedAlias(t *testing.T) {
	for _, name := range []string{"read", "mcp_owned"} {
		r, e := (Provider{}).Stream(context.Background(), model.TurnRequest{ChatRequest: model.ChatRequest{Messages: []model.ChatMessage{{Role: "user", Content: "mcp"}}}, Tools: []model.ToolDefinition{{Function: model.ToolFunction{Name: name}}}}, func(model.ModelEvent) error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		if (len(r.ToolCalls) == 1) != (name == "mcp_owned") {
			t.Fatal(name, r)
		}
	}
}

func TestArtifactReadFixtureRequestsRealNativeRead(t *testing.T) {
	p := Provider{}
	r, e := p.Stream(context.Background(), model.TurnRequest{ChatRequest: model.ChatRequest{Messages: []model.ChatMessage{{Role: "user", Content: "artifact-read"}}}, Tools: []model.ToolDefinition{{Function: model.ToolFunction{Name: "read"}}}}, func(model.ModelEvent) error { return nil })
	if e != nil {
		t.Fatal(e)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "read" {
		t.Fatal("owned artifact fixture must call real native read", r)
	}
}
