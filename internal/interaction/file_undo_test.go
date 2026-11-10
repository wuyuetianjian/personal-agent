package interaction

import (
	"agent/internal/model"
	"agent/internal/runtime"
	"agent/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func appliedEdit(t *testing.T, mode PermissionMode) (*Store, *WorkspaceBroker, WorkspaceBinding, string) {
	t.Helper()
	s, b, w, id := workspaceFixture(t, mode)
	c := context.Background()
	e := runWorkspaceTool(t, s, b, id, patchCall(w))
	err := e.Run(c, id)
	if mode == ModeDefault {
		if !errors.Is(err, ErrWaitingApproval) {
			t.Fatal(err)
		}
		cp, _ := e.Checkpoint(c, id)
		p := cp.Pending[0]
		if err = s.DecideApproval(c, p.ID, p.Digest, "approved"); err != nil {
			t.Fatal(err)
		}
		err = e.Run(c, id)
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, b, w, id
}
func undoIntent(t *testing.T, s *Store, id string) SessionOperation {
	t.Helper()
	var cursor int64
	if err := s.db.QueryRow("SELECT sequence FROM interaction_sessions WHERE id='s'").Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	return SessionOperation{Kind: "file_undo", RequestID: "undo-request", BoundaryRunID: id, ExpectedCursor: cursor}
}
func noModelEngine(t *testing.T, s *Store, b *WorkspaceBroker) *Engine {
	t.Helper()
	e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		t.Error("deterministic undo called model")
		return model.TurnResult{}, ErrInvalid
	}), b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestFileUndoFreshApprovalAndOnce(t *testing.T) {
	for _, mode := range []PermissionMode{ModeDefault, ModeAcceptEdits} {
		t.Run(string(mode), func(t *testing.T) {
			s, b, w, source := appliedEdit(t, mode)
			c := context.Background()
			inv, err := s.EditCheckpoints(c, "s")
			if err != nil || len(inv.Checkpoints) != 1 {
				t.Fatal(inv, err)
			}
			p := inv.Checkpoints[0]
			if p.SourceRunID != source || p.Disposition != "available" {
				t.Fatal(p)
			}
			op := undoIntent(t, s, p.ID)
			r, err := s.UndoEdit(c, "s", op)
			if err != nil {
				t.Fatal(err)
			}
			again, err := s.UndoEdit(c, "s", op)
			if err != nil || again.ID != r.ID {
				t.Fatal(again, err)
			}
			e := noModelEngine(t, s, b)
			if err = e.Run(c, r.ID); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(raw) != "new" {
				t.Fatal(string(raw))
			}
			cp, _ := e.Checkpoint(c, r.ID)
			u := cp.Pending[0]
			if !u.RequiresApproval || u.ID == p.ID || cp.Tokens != 0 {
				t.Fatal(cp)
			}
			review, err := b.ProposalReview(c, "s", u.ID)
			if err != nil || review.OldContent != "new" || review.NewContent != "old" || review.UndoOf != p.ID {
				t.Fatal(review, err)
			}
			if err = s.DecideApproval(c, u.ID, u.Digest, "approved"); err != nil {
				t.Fatal(err)
			}
			if err = e.Run(c, r.ID); err != nil {
				t.Fatal(err)
			}
			raw, _ = os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(raw) != "old" {
				t.Fatal(string(raw))
			}
			inv, err = s.EditCheckpoints(c, "s")
			if err != nil || len(inv.Checkpoints) != 1 || inv.Checkpoints[0].Disposition != "undone" {
				t.Fatal(inv, err)
			}
			op.RequestID = "new-undo"
			op.ExpectedCursor += 100
			if _, err = s.UndoEdit(c, "s", op); err == nil {
				t.Fatal("second undo admitted")
			}
			if err = s.DecideApproval(c, u.ID, u.Digest, "approved"); err == nil {
				t.Fatal("approval replay")
			}
			if _, err = s.UndoEdit(c, "other", undoIntent(t, s, p.ID)); err == nil {
				t.Fatal("cross-session")
			}
			cp, _ = e.Checkpoint(c, r.ID)
			if cp.Tokens != 0 {
				t.Fatal("fake usage")
			}
			events, _ := s.Events(c, "s", 0, 1000)
			for _, ev := range events {
				if ev.RunID == r.ID && (ev.Type == "model.started" || ev.Type == "model.completed") {
					t.Fatal(ev)
				}
			}
			// Context remains valid without invented assistant tool-call protocol.
			if _, err = s.SessionContextView(c, "s"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestFileUndoMismatchDenyAndUnknown(t *testing.T) {
	for _, scenario := range []string{"before", "after", "deny", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			s, b, w, _ := appliedEdit(t, ModeDefault)
			c := context.Background()
			inv, _ := s.EditCheckpoints(c, "s")
			id := inv.Checkpoints[0].ID
			if scenario == "before" {
				os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("user"), 0600)
				if _, err := s.UndoEdit(c, "s", undoIntent(t, s, id)); err == nil {
					t.Fatal("stale admitted")
				}
				return
			}
			if scenario == "unknown" {
				s.db.Exec("UPDATE side_effect_records SET status='started' WHERE node_id=?", id)
				inv, err := s.EditCheckpoints(c, "s")
				if err != nil || len(inv.Checkpoints) != 0 {
					t.Fatal(inv, err)
				}
				if _, err = s.UndoEdit(c, "s", undoIntent(t, s, id)); err == nil {
					t.Fatal("unknown reversible")
				}
				return
			}
			r, err := s.UndoEdit(c, "s", undoIntent(t, s, id))
			if err != nil {
				t.Fatal(err)
			}
			e := noModelEngine(t, s, b)
			if err = e.Run(c, r.ID); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, _ := e.Checkpoint(c, r.ID)
			p := cp.Pending[0]
			decision := "approved"
			want := "user"
			if scenario == "deny" {
				decision = "denied"
				want = "new"
			} else {
				os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte(want), 0600)
			}
			if err = s.DecideApproval(c, p.ID, p.Digest, decision); err != nil {
				t.Fatal(err)
			}
			err = e.Run(c, r.ID)
			if scenario == "deny" && !errors.Is(err, ErrConflict) {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(raw) != want {
				t.Fatal(string(raw))
			}
			inv, _ = s.EditCheckpoints(c, "s")
			if inv.Checkpoints[0].Disposition == "available" || inv.Checkpoints[0].Disposition == "undone" {
				t.Fatal(inv)
			}
		})
	}
}

func TestFileUndoRejectsLaterNativeOperationEvenSameBytes(t *testing.T) {
	s, b, w, _ := appliedEdit(t, ModeAcceptEdits)
	c := context.Background()
	inv, _ := s.EditCheckpoints(c, "s")
	source := inv.Checkpoints[0].ID
	r, err := s.CreateRun(c, "s", "later", Message{Content: "another native operation"})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(PatchArguments{Scope: w.ScopeID, Path: "note.txt", Preimage: contentHash([]byte("new")), Content: "new"})
	e := runWorkspaceTool(t, s, b, r.ID, ToolCall{ID: "later", Name: "apply_patch", Arguments: string(args)})
	if err = e.Run(c, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UndoEdit(c, "s", undoIntent(t, s, source)); err == nil {
		t.Fatal("old checkpoint regained authority after another native write")
	}
}

func TestFileUndoConcurrentRequestsAndDecisions(t *testing.T) {
	s, b, _, _ := appliedEdit(t, ModeAcceptEdits)
	ctx := context.Background()
	inv, _ := s.EditCheckpoints(ctx, "s")
	op := undoIntent(t, s, inv.Checkpoints[0].ID)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.UndoEdit(ctx, "s", op)
			if err != nil {
				errs <- err
			} else {
				ids <- r.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	id := ""
	for got := range ids {
		if id != "" && got != id {
			t.Fatal("duplicate action")
		}
		id = got
	}
	e := noModelEngine(t, s, b)
	if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(ctx, id)
	p := cp.Pending[0]
	wins := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); wins <- s.DecideApproval(ctx, p.ID, p.Digest, "approved") == nil }()
	}
	wg.Wait()
	close(wins)
	count := 0
	for win := range wins {
		if win {
			count++
		}
	}
	if count != 1 {
		t.Fatal(count)
	}
	if err := e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
}
func TestFileUndoRestartBoundariesNeverReplay(t *testing.T) {
	for _, stage := range []string{"admitted", "waiting", "consumed", "started", "effect_unjournaled", "completed_journal", "cancel_started"} {
		t.Run(stage, func(t *testing.T) {
			s, b, w, _ := appliedEdit(t, ModeDefault)
			ctx := context.Background()
			inv, _ := s.EditCheckpoints(ctx, "s")
			op := undoIntent(t, s, inv.Checkpoints[0].ID)
			r, err := s.UndoEdit(ctx, "s", op)
			if err != nil {
				t.Fatal(err)
			}
			e := noModelEngine(t, s, b)
			var proposal ToolProposal
			if stage != "admitted" {
				if err = e.Run(ctx, r.ID); !errors.Is(err, ErrWaitingApproval) {
					t.Fatal(err)
				}
				cp, _ := e.Checkpoint(ctx, r.ID)
				proposal = cp.Pending[0]
				if stage != "waiting" {
					if err = s.DecideApproval(ctx, proposal.ID, proposal.Digest, "approved"); err != nil {
						t.Fatal(err)
					}
					if err = s.ConsumeApproval(ctx, proposal.ID, proposal.Digest); err != nil {
						t.Fatal(err)
					}
					cp.Phase = PhaseExecuting
					if err = e.save(ctx, r, "", &cp, "", nil); err != nil {
						t.Fatal(err)
					}
					if stage != "consumed" {
						journal := runtime.SQLiteSideEffectStore{DB: s.db}
						if _, _, err = journal.Claim(ctx, runtime.SideEffectRecord{IdempotencyKey: effectKey(r.ID, proposal.ID), WorkflowID: r.ID, NodeID: proposal.ID, CapabilityID: "apply_patch"}); err != nil {
							t.Fatal(err)
						}
						if stage == "effect_unjournaled" || stage == "completed_journal" {
							result, x := b.Execute(ctx, proposal.ID)
							if x != nil || result.IsError {
								t.Fatal(result, x)
							}
							if stage == "completed_journal" {
								raw, _ := json.Marshal(result)
								if err = journal.Complete(ctx, effectKey(r.ID, proposal.ID), string(raw)); err != nil {
									t.Fatal(err)
								}
							}
						}
						if stage == "cancel_started" {
							if err = e.Cancel(ctx, r.ID); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			var seq int
			var name, path string
			if err = s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
				t.Fatal(err)
			}
			s.db.Close()
			disk, err := storage.OpenSQLite(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer disk.Close()
			s = NewStore(disk.SQL)
			b = NewWorkspaceBroker(s)
			e = noModelEngine(t, s, b)
			duplicate, err := s.UndoEdit(ctx, "s", op)
			if err != nil || duplicate.ID != r.ID {
				t.Fatal(duplicate, err)
			}
			err = e.Recover(ctx, r.ID)
			switch stage {
			case "admitted", "waiting":
				if err != nil {
					t.Fatal(err)
				}
				if err = e.Run(ctx, r.ID); !errors.Is(err, ErrWaitingApproval) {
					t.Fatal(err)
				}
			case "completed_journal":
				if err != nil {
					t.Fatal(err)
				}
				got, _ := s.GetRun(ctx, r.ID)
				if got.State != RunCompleted {
					t.Fatal(got)
				}
			default:
				if !errors.Is(err, ErrReconciliation) {
					t.Fatal(err)
				}
				if err = e.Run(ctx, r.ID); !errors.Is(err, ErrReconciliation) {
					t.Fatal("replayed unknown", err)
				}
			}
			want := "new"
			if stage == "effect_unjournaled" || stage == "completed_journal" {
				want = "old"
			}
			raw, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(raw) != want {
				t.Fatal(string(raw))
			}
			inv, err = s.EditCheckpoints(ctx, "s")
			if err != nil || len(inv.Checkpoints) != 1 {
				t.Fatal(inv, err)
			}
			if stage == "effect_unjournaled" && inv.Checkpoints[0].Disposition != "unknown" {
				t.Fatal(inv)
			}
		})
	}
}
func TestFileUndoCancelledBeforeApprovalAndRootChange(t *testing.T) {
	for _, kind := range []string{"cancel", "root"} {
		t.Run(kind, func(t *testing.T) {
			s, b, w, _ := appliedEdit(t, ModeDefault)
			ctx := context.Background()
			inv, _ := s.EditCheckpoints(ctx, "s")
			op := undoIntent(t, s, inv.Checkpoints[0].ID)
			if kind == "root" {
				if err := os.Rename(w.Policy.Root, w.Policy.Root+"-saved"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(w.Policy.Root + "-saved") })
				os.Mkdir(w.Policy.Root, 0700)
				os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("new"), 0600)
				if _, err := s.UndoEdit(ctx, "s", op); err == nil {
					t.Fatal("changed root accepted")
				}
				inventory, err := s.EditCheckpoints(ctx, "s")
				if err != nil || inventory.Supported {
					t.Fatal(inventory, err)
				}
				return
			}
			r, err := s.UndoEdit(ctx, "s", op)
			if err != nil {
				t.Fatal(err)
			}
			e := noModelEngine(t, s, b)
			if err = e.Run(ctx, r.ID); !errors.Is(err, ErrWaitingApproval) {
				t.Fatal(err)
			}
			cp, _ := e.Checkpoint(ctx, r.ID)
			p := cp.Pending[0]
			if err = e.Cancel(ctx, r.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err == nil {
				t.Fatal("approval after cancel")
			}
			if err = e.Run(ctx, r.ID); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
			if string(raw) != "new" {
				t.Fatal(string(raw))
			}
		})
	}
}

func TestFileUndoOnlySuccessfulNativeEffectsAreCheckpoints(t *testing.T) {
	for _, kind := range []string{"plan", "waiting", "denied", "refused", "read"} {
		t.Run(kind, func(t *testing.T) {
			mode := ModeDefault
			if kind == "plan" {
				mode = ModePlan
			}
			s, b, w, id := workspaceFixture(t, mode)
			ctx := context.Background()
			call := patchCall(w)
			if kind == "read" {
				raw, _ := json.Marshal(ReadArguments{Scope: w.ScopeID, Path: "note.txt"})
				call = ToolCall{ID: "read", Name: "read", Arguments: string(raw)}
			}
			e := runWorkspaceTool(t, s, b, id, call)
			err := e.Run(ctx, id)
			if kind == "denied" || kind == "refused" {
				if !errors.Is(err, ErrWaitingApproval) {
					t.Fatal(err)
				}
				cp, _ := e.Checkpoint(ctx, id)
				p := cp.Pending[0]
				decision := "denied"
				if kind == "refused" {
					decision = "approved"
					os.WriteFile(filepath.Join(w.Policy.Root, "note.txt"), []byte("user"), 0600)
				}
				if err = s.DecideApproval(ctx, p.ID, p.Digest, decision); err != nil {
					t.Fatal(err)
				}
				e.Run(ctx, id)
			}
			inv, err := s.EditCheckpoints(ctx, "s")
			if err != nil || len(inv.Checkpoints) != 0 {
				t.Fatal(inv, err)
			}
			if kind == "plan" && (inv.Supported || !strings.Contains(inv.Reason, "plan mode")) {
				t.Fatal(inv)
			}
		})
	}
}

func TestFileUndoLegacySingleEditJournalRemainsUsable(t *testing.T) {
	s, b, w, _ := appliedEdit(t, ModeAcceptEdits)
	ctx := context.Background()
	if _, err := s.db.Exec("DELETE FROM interaction_file_heads"); err != nil {
		t.Fatal(err)
	}
	inv, err := s.EditCheckpoints(ctx, "s")
	if err != nil || len(inv.Checkpoints) != 1 || inv.Checkpoints[0].Disposition != "available" {
		t.Fatal(inv, err)
	}
	r, err := s.UndoEdit(ctx, "s", undoIntent(t, s, inv.Checkpoints[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	e := noModelEngine(t, s, b)
	if err = e.Run(ctx, r.ID); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, _ := e.Checkpoint(ctx, r.ID)
	p := cp.Pending[0]
	if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(w.Policy.Root, "note.txt"))
	if string(raw) != "old" {
		t.Fatal("legacy undo refused", string(raw))
	}
}

func TestFileUndoReservationAndRequestLedgerCommitTogether(t *testing.T) {
	s, _, _, _ := appliedEdit(t, ModeAcceptEdits)
	ctx := context.Background()
	inv, _ := s.EditCheckpoints(ctx, "s")
	op := undoIntent(t, s, inv.Checkpoints[0].ID)
	if _, err := s.db.Exec("CREATE TRIGGER fail_undo_ledger BEFORE INSERT ON interaction_session_operations BEGIN SELECT RAISE(ABORT,'injected commit boundary'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UndoEdit(ctx, "s", op); err == nil {
		t.Fatal("expected transaction refusal")
	}
	var runs, reservations int
	s.db.QueryRow("SELECT count(*) FROM interaction_runs").Scan(&runs)
	s.db.QueryRow("SELECT count(*) FROM interaction_edit_undos").Scan(&reservations)
	if runs != 1 || reservations != 0 {
		t.Fatal(runs, reservations)
	}
	s.db.Exec("DROP TRIGGER fail_undo_ledger")
	r, err := s.UndoEdit(ctx, "s", op)
	if err != nil {
		t.Fatal(err)
	}
	record, err := s.ReadSessionOperation(ctx, "s", op.RequestID)
	if err != nil || record.ActionRunID != r.ID {
		t.Fatal(record, err)
	}
}

func TestFileUndoRejectsUnattestedCompletedJournal(t *testing.T) {
	s, _, _, _ := appliedEdit(t, ModeAcceptEdits)
	ctx := context.Background()
	inv, _ := s.EditCheckpoints(ctx, "s")
	id := inv.Checkpoints[0].ID
	if _, err := s.db.Exec("UPDATE side_effect_records SET result_ref='{}' WHERE node_id=?", id); err != nil {
		t.Fatal(err)
	}
	inv, err := s.EditCheckpoints(ctx, "s")
	if err != nil || len(inv.Checkpoints) != 0 {
		t.Fatal("empty result is not an observed success", inv, err)
	}
}
