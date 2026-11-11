package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func admitHookRun(t *testing.T, policy string) (*Store, string) {
	t.Helper()
	s, in := skillRunSetup(t)
	if policy != "" {
		hookEnable(t, s, policy)
	}
	p, e := s.PrepareSkillRunWithHooks(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V2 == nil || a.V2.State != "admitted" {
		t.Fatal(a, e)
	}
	return s, a.V2.RunID
}
func TestSkillHooksReviewPin(t *testing.T) {
	s, in := skillRunSetup(t)
	legacy, e := s.PrepareSkillRun(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	in.RequestID = "v2"
	p, e := s.PrepareSkillRunWithHooks(t.Context(), "s", in)
	if e != nil || p.SchemaVersion != 2 || operatorHash(p.Review) != operatorHash(legacy.Review) || p.Hooks.Generation != 0 || p.Hooks.Policy.Rules == nil {
		t.Fatal(p, e)
	}
	a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "v2-confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V2 == nil {
		t.Fatal(a, e)
	}
	r, e := s.SkillRunWithHooks(t.Context(), "s", a.V2.RunID)
	if e != nil || r.V2 == nil || r.V2.Pin.Hooks.SnapshotSHA256 != operatorHash(p.Hooks) {
		t.Fatal(r, e)
	}
	if _, _, _, e := skillRunData(t.Context(), s.db, a.V2.RunID); e != nil {
		t.Fatal(e)
	}
}
func TestSkillHooksLegacySelection(t *testing.T) {
	s, in := skillRunSetup(t)
	p, e := s.PrepareSkillRun(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "old-confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V1 == nil || a.V1.State != "stale" || a.V1.Reason != "hook_review_required" {
		t.Fatal(a, e)
	}
	hookEnable(t, s, hookPolicyExample)
	in.RequestID = "after"
	if _, e = s.PrepareSkillRun(t.Context(), "s", in); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
func TestSkillHooksWireHandoff(t *testing.T) {
	s, id := admitHookRun(t, "")
	if _, e := s.SkillRun(t.Context(), "s", id); !errors.Is(e, ErrConflict) {
		t.Fatal("v2 projected to v1", e)
	}
	if _, e := s.SkillRunOperation(t.Context(), "s", "preview"); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	r, e := s.SkillRunWithHooks(t.Context(), "s", id)
	if e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	if m["schema_version"] != float64(2) || m["V2"] != nil || m["pin"].(map[string]any)["hooks"] == nil {
		t.Fatal(string(b))
	}
	if _, e = json.Marshal(SkillRunView{}); e == nil {
		t.Fatal("empty union")
	}
	page, e := s.ListSkillRunsWithHooks(t.Context(), "s", "", 20)
	if e != nil || page.SchemaVersion != 2 || len(page.Runs) != 1 || page.Runs[0].V2 == nil {
		t.Fatal(page, e)
	}
}
func TestSkillHooksSelectionRace(t *testing.T) {
	s, in := skillRunSetup(t)
	p, e := s.PrepareSkillRunWithHooks(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	hookEnable(t, s, hookPolicyExample)
	a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "changed", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V2 == nil || a.V2.State != "stale" || a.V2.Reason != "selection_changed" {
		t.Fatal(a, e)
	}
	s, id := admitHookRun(t, "")
	hookEnable(t, s, hookPolicyExample)
	hookDecide(t, s, hookPrepare(t, s, "disable", "disable", "", ""), "disable-c", "confirm")
	r, e := s.SkillRunWithHooks(t.Context(), "s", id)
	if e != nil || !r.V2.Revoked {
		t.Fatal(r, e)
	}
}

func hookPending(t *testing.T, s *Store, id string) (*Engine, Run, Checkpoint) {
	t.Helper()
	e, err := NewSkillRunEngine(s, id)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetRun(t.Context(), id)
	cp, err := e.claim(t.Context(), r, "owned")
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.broker.Propose(t.Context(), id, cp.Calls[0])
	if err != nil {
		t.Fatal(err)
	}
	cp.Pending = []ToolProposal{p}
	cp.Calls = cp.Calls[1:]
	cp.Phase = PhaseRunning
	if err = e.save(t.Context(), r, "owned", &cp, "tool.proposed", p); err != nil {
		t.Fatal(err)
	}
	return e, r, cp
}
func TestHooksDenyBeforeClaims(t *testing.T) {
	s, id := admitHookRun(t, strings.Replace(hookPolicyExample, "workspace.search", "workspace.read", 1))
	e, r, cp := hookPending(t, s, id)
	if err := e.startSkillNode(t.Context(), r, "owned", &cp); !errors.Is(err, ErrHookDenied) {
		t.Fatal(err)
	}
	var journals, executed, started int
	s.db.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, id).Scan(&journals)
	s.db.QueryRow(`SELECT sum(executed) FROM interaction_tool_proposals WHERE run_id=?`, id).Scan(&executed)
	s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.started'`, id).Scan(&started)
	actual, _ := s.EngineCheckpoint(t.Context(), id)
	rec, err := s.SkillRunWithHooks(t.Context(), "s", id)
	receipts, re := s.SkillHookReceipts(t.Context(), "s", id)
	if err != nil || re != nil || journals != 0 || executed != 0 || started != 0 || actual.Steps != 0 || len(receipts.Receipts) != 1 || receipts.Receipts[0].Decision != "deny" || rec.V2.Nodes[0].Reason != "hook_denied" || rec.V2.Nodes[0].Result != nil || rec.V2.Nodes[0].ProposalID != "" {
		t.Fatal(journals, executed, started, actual, rec, receipts, err, re)
	}
}
func TestHooksAtomicAllowedStart(t *testing.T) {
	for _, fault := range []string{"receipt", "journal", "checkpoint", "event", "none"} {
		t.Run(fault, func(t *testing.T) {
			s, id := admitHookRun(t, "")
			e, r, cp := hookPending(t, s, id)
			table := map[string]string{"receipt": "interaction_skill_hook_receipts", "journal": "side_effect_records", "event": "interaction_events"}[fault]
			if table != "" {
				if _, err := s.db.Exec(`CREATE TRIGGER inject BEFORE INSERT ON ` + table + ` BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if fault == "checkpoint" {
				s.db.Exec(`CREATE TRIGGER inject BEFORE UPDATE OF checkpoint ON interaction_engine BEGIN SELECT RAISE(ABORT,'fixture'); END`)
			}
			err := e.startSkillNode(t.Context(), r, "owned", &cp)
			var receipts, journal, events int
			s.db.QueryRow(`SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=?`, id).Scan(&receipts)
			s.db.QueryRow(`SELECT count(*) FROM side_effect_records WHERE workflow_id=?`, id).Scan(&journal)
			s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='tool.started'`, id).Scan(&events)
			got, _ := s.EngineCheckpoint(t.Context(), id)
			if fault == "none" {
				if err != nil || receipts != 1 || journal != 1 || events != 1 || got.Steps != 1 || got.Phase != PhaseExecuting {
					t.Fatal(err, receipts, journal, events, got)
				}
				if err = e.startSkillNode(t.Context(), r, "owned", &cp); !errors.Is(err, ErrReconciliation) {
					t.Fatal("reclaim", err)
				}
			} else if err == nil || receipts != 0 || journal != 0 || events != 0 || got.Steps != 0 {
				t.Fatal(err, receipts, journal, events, got)
			}
		})
	}
}
func TestHooksNativeClaimReceiptIdentity(t *testing.T) {
	for _, attack := range []string{"missing_receipt", "action", "snapshot", "revoked"} {
		t.Run(attack, func(t *testing.T) {
			s, id := admitHookRun(t, "")
			e, r, cp := hookPending(t, s, id)
			if err := e.startSkillNode(t.Context(), r, "owned", &cp); err != nil {
				t.Fatal(err)
			}
			switch attack {
			case "missing_receipt":
				s.db.Exec(`DROP TRIGGER interaction_skill_hook_receipts_retain`)
				s.db.Exec(`DELETE FROM interaction_skill_hook_receipts WHERE run_id=?`, id)
			case "action":
				cp.Action = "file_undo"
				raw, _ := json.Marshal(cp)
				s.db.Exec(`UPDATE interaction_engine SET checkpoint=? WHERE run_id=?`, string(raw), id)
			case "snapshot":
				s.db.Exec(`DROP TRIGGER interaction_skill_hook_snapshot_immutable`)
				s.db.Exec(`UPDATE interaction_skill_run_hooks SET snapshot_hash=? WHERE run_id=?`, strings.Repeat("a", 64), id)
			case "revoked":
				hookEnable(t, s, hookPolicyExample)
			}
			b := e.broker.(*SkillRunBroker)
			p, err := b.native.loadPrepared(t.Context(), cp.Pending[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if err = b.native.skillDispatchClaim(t.Context(), p); err == nil {
				t.Fatal("tampered claim admitted")
			}
			var executed int
			s.db.QueryRow(`SELECT executed FROM interaction_tool_proposals WHERE id=?`, p.Proposal.ID).Scan(&executed)
			if executed != 0 {
				t.Fatal(executed)
			}
		})
	}
}

func TestSkillHooksStrictStoredReads(t *testing.T) {
	s, id := admitHookRun(t, "")
	s.db.Exec(`DROP TRIGGER interaction_skill_runs_immutable`)
	if _, e := s.db.Exec(`UPDATE interaction_skill_runs SET pin=json_set(pin,'$.scope_id','forged') WHERE run_id=?`, id); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SkillRunWithHooks(t.Context(), "s", id); !errors.Is(e, ErrReconciliation) {
		t.Fatal("forged pin accepted", e)
	}
}
func TestSkillHooksSelectionBeforeFinalCommit(t *testing.T) {
	s, id := admitHookRun(t, "")
	hookOwnedFile(t, s)
	e, r, cp := hookPending(t, s, id)
	if err := e.execute(t.Context(), r, "owned", &cp); err != nil {
		t.Fatal(err)
	}
	p, err := e.broker.Propose(t.Context(), id, cp.Calls[0])
	if err != nil {
		t.Fatal(err)
	}
	cp.Pending = []ToolProposal{p}
	cp.Calls = cp.Calls[1:]
	cp.Phase = PhaseRunning
	if err = e.save(t.Context(), r, "owned", &cp, "tool.proposed", p); err != nil {
		t.Fatal(err)
	}
	if err = e.execute(t.Context(), r, "owned", &cp); err != nil {
		t.Fatal(err)
	}
	hookEnable(t, s, hookPolicyExample)
	cp.Phase = PhaseCompleted
	if err = e.finish(t.Context(), r, "owned", &cp, RunCompleted); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	got, err := s.SkillRunWithHooks(t.Context(), "s", id)
	receipts, _ := s.SkillHookReceipts(t.Context(), "s", id)
	if err != nil || got.V2.Execution != "revoked" || got.V2.Nodes[1].Outcome != "success" || receipts.Receipts[4].Status != "revoked" {
		t.Fatal(got, receipts, err)
	}
}
func TestSkillHooksImportNoopAndLegacyPermanentRevocation(t *testing.T) {
	s, id := admitTestSkillRun(t)
	p := hookDecide(t, s, hookPrepare(t, s, "import-only", "import", "", hookPolicyExample), "import-only-c", "confirm")
	hookDecide(t, s, hookPrepare(t, s, "noop", "disable", "", ""), "noop-c", "confirm")
	r, e := s.SkillRun(t.Context(), "s", id)
	if e != nil || r.Revoked {
		t.Fatal(r, e)
	}
	for i := 0; i < 2; i++ {
		hookDecide(t, s, hookPrepare(t, s, fmt.Sprint("on", i), "enable", p.RevisionID, ""), fmt.Sprint("on-c", i), "confirm")
		hookDecide(t, s, hookPrepare(t, s, fmt.Sprint("off", i), "disable", "", ""), fmt.Sprint("off-c", i), "confirm")
	}
	r, e = s.SkillRun(t.Context(), "s", id)
	sel, _ := s.HookSelection(t.Context(), "s")
	if e != nil || !r.Revoked || sel.Generation != 4 {
		t.Fatal(r, sel, e)
	}
	engine, _ := NewSkillRunEngine(s, id)
	engine.Run(t.Context(), id)
	r, _ = s.SkillRun(t.Context(), "s", id)
	if r.Execution != "revoked" {
		t.Fatal(r)
	}
}

// hookForbiddenBroker guards accidental fallback to the caller's broker. It is
// supplementary: Run intentionally replaces it for Skills, so only the real
// compiler entry counters below attest the routed Skill/native callbacks.
type hookForbiddenBroker struct{ proposals, executions atomic.Int32 }

func (b *hookForbiddenBroker) Propose(context.Context, string, ToolCall) (ToolProposal, error) {
	b.proposals.Add(1)
	return ToolProposal{}, ErrInvalid
}
func (b *hookForbiddenBroker) Execute(context.Context, string) (ToolResult, error) {
	b.executions.Add(1)
	return ToolResult{}, ErrInvalid
}

// Go's coverage compiler inserts a counter at each real function entry. Resolve
// that block from the AST instead of fragile line numbers; absence is an error,
// never a zero. Production source and immutable Engine routing are not replaced.
func hookCallbackEntryCount(t *testing.T, profile []byte, source, receiver, name string) uint64 {
	t.Helper()
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, source, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	entry := ""
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		recv := ""
		if fn.Recv != nil {
			typ := fn.Recv.List[0].Type
			if ptr, ok := typ.(*ast.StarExpr); ok {
				typ = ptr.X
			}
			if id, ok := typ.(*ast.Ident); ok {
				recv = id.Name
			}
		}
		if recv != receiver {
			continue
		}
		if entry != "" {
			t.Fatalf("ambiguous callback %s.%s", receiver, name)
		}
		pos := positions.Position(fn.Body.Lbrace)
		entry = fmt.Sprintf("/%s:%d.%d,", source, pos.Line, pos.Column)
	}
	if entry == "" {
		t.Fatalf("missing callback %s.%s in %s", receiver, name, source)
	}
	found := false
	var count uint64
	for _, line := range strings.Split(string(profile), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 || !strings.Contains(fields[0], entry) {
			continue
		}
		if found {
			t.Fatalf("duplicate compiler entry block %s", entry)
		}
		found = true
		if statements, e := strconv.Atoi(fields[1]); e != nil || statements < 1 {
			t.Fatalf("invalid compiler entry block %q", line)
		}
		count, err = strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !found {
		t.Fatalf("compiler entry counter absent: %s.%s (%s)", receiver, name, entry)
	}
	return count
}

func TestHooksDenyBeforeClaimsEngineRunCallbacks(t *testing.T) {
	if !platformFileCapability().Enabled {
		t.Skip("native files unavailable")
	}
	for _, last := range []bool{false, true} {
		for _, resume := range []bool{false, true} {
			t.Run(fmt.Sprintf("last=%v/resume=%v", last, resume), func(t *testing.T) {
				// The fixed child selector runs only the existing real Run/recovery fixture,
				// never this parent counter test. All output belongs to this owned temp dir.
				dir := t.TempDir()
				profilePath := filepath.Join(dir, "callbacks.cover")
				selector := fmt.Sprintf("^TestHooksDeniedRecovery$/^last=%v$/^resume=%v$", last, resume)
				cmd := exec.CommandContext(t.Context(), "go", "test", ".", "-count=1", "-covermode=atomic", "-coverprofile="+profilePath, "-run="+selector)
				output, err := cmd.CombinedOutput()
				if writeErr := os.WriteFile(filepath.Join(dir, "go-test.log"), output, 0600); writeErr != nil {
					t.Fatal(writeErr)
				}
				if err != nil {
					t.Fatalf("owned callback fixture failed: %v\n%s", err, output)
				}
				profile, err := os.ReadFile(profilePath)
				if err != nil {
					t.Fatal(err)
				}
				prefix := uint64(0)
				if last {
					prefix = 1
				}
				runs := uint64(2)
				if resume {
					runs = 3
				}
				for _, probe := range []struct {
					source, receiver, name string
					want                   uint64
				}{
					{"engine.go", "Engine", "Run", runs},
					{"skill_execution_engine.go", "SkillRunBroker", "Propose", prefix + 1},
					{"tools.go", "WorkspaceBroker", "Propose", prefix + 1},
					{"skill_execution_engine.go", "SkillRunBroker", "Execute", prefix},
					{"tools.go", "WorkspaceBroker", "Execute", prefix},
					{"skill_execution_engine.go", "WorkspaceBroker", "skillDispatchClaim", prefix},
					{"workspace_linux.go", "", "readWorkspaceFile", prefix},
					{"workspace_linux.go", "", "readRegularAt", prefix},
					{"file_undo.go", "actionOnlyProvider", "Stream", 0},
				} {
					count := hookCallbackEntryCount(t, profile, probe.source, probe.receiver, probe.name)
					t.Logf("Go compiler entry %s.%s=%d; authorized prefix=%d", probe.receiver, probe.name, count, prefix)
					if count != probe.want {
						t.Errorf("callback %s.%s entered %d times; want %d including only the authorized prefix, with no denied/recovery invocation", probe.receiver, probe.name, count, probe.want)
					}
				}
			})
		}
	}
}
