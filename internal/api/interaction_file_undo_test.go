package api

import (
	"agent/internal/interaction"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestFileUndoAPIUsesNewApprovalAndRecoverableIdentity(t *testing.T) {
	s, rt := serviceFixture(t, reviewProvider(nil, nil))
	if !s.broker.Capabilities().Files.Enabled {
		t.Skip("native files unavailable")
	}
	os.WriteFile(filepath.Join(s.options.Workspace, "note.txt"), []byte("old"), 0600)
	id := namedSession(t, s, "undo", "acceptEdits")
	postReviewMessage(t, s, id, "edit", "edit")
	awaitState(t, s, id, "completed")
	path := "/api/v1/sessions/" + id
	got := apiRequest(s, "GET", path+"/edit-checkpoints", "")
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	var inv interaction.EditCheckpointInventory
	json.Unmarshal(got.Body.Bytes(), &inv)
	if len(inv.Checkpoints) != 1 {
		t.Fatal(inv)
	}
	snap, _ := s.snapshot(s.ctx, id)
	body := fmt.Sprintf(`{"request_id":"undo","checkpoint_id":%q,"expected_cursor":%d}`, inv.Checkpoints[0].ID, snap.Cursor)
	got = apiRequest(s, "POST", path+"/file-undo", body)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	var ack struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(got.Body.Bytes(), &ack)
	awaitState(t, s, id, "waiting_approval")
	snap, _ = s.snapshot(s.ctx, id)
	a := snap.Runs[len(snap.Runs)-1].Approval
	if a == nil {
		t.Fatal(snap)
	}
	raw, _ := os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
	if string(raw) != "new" {
		t.Fatal(string(raw))
	}
	rt.DB.SQL.Exec("DELETE FROM interaction_api_actions WHERE request_id='undo'")
	got = apiRequest(s, "POST", path+"/file-undo", body)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	var duplicate struct {
		RunID string `json:"run_id"`
	}
	json.Unmarshal(got.Body.Bytes(), &duplicate)
	if duplicate.RunID != ack.RunID {
		t.Fatal("duplicate undo")
	}
	got = apiRequest(s, "GET", path+"/operations/undo", "")
	if got.Code != 200 {
		t.Fatal(got.Code, got.Body.String())
	}
	var record interaction.SessionOperationRecord
	json.Unmarshal(got.Body.Bytes(), &record)
	if record.ActionRunID != ack.RunID || record.Operation.Kind != "file_undo" {
		t.Fatal(record)
	}
	decision := fmt.Sprintf(`{"request_id":"undo-approve","digest":%q,"decision":"approved"}`, a.Digest)
	got = apiRequest(s, "POST", "/api/v1/approvals/"+a.ID+"/decision", decision)
	if got.Code != 202 {
		t.Fatal(got.Code, got.Body.String())
	}
	awaitState(t, s, id, "completed")
	raw, _ = os.ReadFile(filepath.Join(s.options.Workspace, "note.txt"))
	if string(raw) != "old" {
		t.Fatal(string(raw))
	}
	var n int
	rt.DB.SQL.QueryRow("SELECT count(*) FROM interaction_events WHERE run_id=? AND type LIKE 'model.%'", ack.RunID).Scan(&n)
	if n != 0 {
		t.Fatal("fabricated model events")
	}
	other := namedSession(t, s, "other", "default")
	got = apiRequest(s, "POST", "/api/v1/sessions/"+other+"/file-undo", fmt.Sprintf(`{"request_id":"cross","checkpoint_id":%q,"expected_cursor":0}`, inv.Checkpoints[0].ID))
	if got.Code != 409 {
		t.Fatal(got.Code, got.Body.String())
	}
}
