package interaction

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChatOnlyProtectedHistoryWithoutWorkspace(t *testing.T) {
	s, ctx := setup(t)
	if err := s.BindChatOnly(ctx, "s", ChatOnlyPolicy{SystemConstraints: []string{"Keep private"}}); err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRun(ctx, "s", "one", Message{Content: "Never delete"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Transition(ctx, r.ID, r.Version, RunCancelled); err != nil {
		t.Fatal(err)
	}
	current, err := s.CreateRun(ctx, "s", "two", Message{Content: "你好"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20000, Output: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Tools) != 0 || c.PendingRunID != current.ID {
		t.Fatal(c)
	}
	found := false
	for _, m := range c.Messages {
		if m.Content == "Never delete" {
			found = true
		}
	}
	if !found {
		t.Fatal("lost protected history")
	}
	if _, err = s.Workspace(ctx, "s"); err == nil {
		t.Fatal("chat authorized a workspace")
	}
	if _, err = NewContextAssembler(s).Build(ctx, "s", TokenBudget{Total: 20, Output: 10}); err == nil {
		t.Fatal("budget bypass")
	}
	if err = s.BindChatOnly(ctx, "s", ChatOnlyPolicy{}); err == nil {
		t.Fatal("rebound policy")
	}
}
func TestApprovalReviewRetainsExactPreimage(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	_ = s
	p, err := b.Propose(context.Background(), id, patchCall(w))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	review, err := b.ProposalReview(context.Background(), "s", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if review.OldContent != "old" || review.NewContent != "new" || review.Path != "note.txt" {
		t.Fatal(review)
	}
	if _, err = b.ProposalReview(context.Background(), "other", p.ID); err == nil {
		t.Fatal("cross-session review")
	}
}
func TestApprovalDecisionPublishesInSameEventSequence(t *testing.T) {
	s, b, w, id := workspaceFixture(t, ModeDefault)
	ctx := context.Background()
	e := runWorkspaceTool(t, s, b, id, patchCall(w))
	_ = e.Run(ctx, id)
	cp, err := e.Checkpoint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := cp.Pending[0]
	if err = s.DecideApproval(ctx, p.ID, p.Digest, "denied"); err != nil {
		t.Fatal(err)
	}
	evs, err := s.Events(ctx, "s", 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if evs[len(evs)-1].Type != "approval.decided" {
		t.Fatal("missing durable decision event", evs[len(evs)-1].Type)
	}
}
