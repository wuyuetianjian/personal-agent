package interaction

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestHooksManagementAllLifetimeBoundaries(t *testing.T) {
	for _, kind := range []string{"sessions", "operations", "revisions", "local_revisions", "previews"} {
		t.Run(kind, func(t *testing.T) {
			s, c := setup(t)
			prepare := func(session, id, kind string) (HookOperation, error) {
				in := HookIntent{RequestID: id, Kind: kind}
				if kind == "import" {
					in.PolicyText = hookPolicyExample
				}
				return s.PrepareHooks(c, session, in)
			}
			decide := func(p HookOperation, decision string) {
				t.Helper()
				_, e := s.DecideHooks(c, p.SessionID, OperatorDecision{RequestID: p.OperationID + "-c", OperationID: p.OperationID, Digest: p.Digest, Decision: decision})
				if e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "sessions":
				for i := 0; i < 129; i++ {
					id := fmt.Sprint("s", i)
					if e := s.CreateSession(c, id); e != nil {
						t.Fatal(e)
					}
					p, e := prepare(id, fmt.Sprint("p", i), "disable")
					if i == 128 {
						if !errors.Is(e, ErrBudget) {
							t.Fatal(i, e)
						}
						break
					}
					if e != nil {
						t.Fatal(i, e)
					}
					decide(p, "cancel")
				}
			case "operations":
				for j := 0; j < 5; j++ {
					session := fmt.Sprint("s", j)
					s.CreateSession(c, session)
					for i := 0; i < 64; i++ {
						p, e := prepare(session, fmt.Sprintf("p-%d-%d", j, i), "disable")
						if j == 4 {
							if !errors.Is(e, ErrBudget) {
								t.Fatal(e)
							}
							return
						}
						if e != nil {
							t.Fatal(j, i, e)
						}
						if i < 63 {
							decide(p, "cancel")
						}
					}
				}
			case "revisions", "local_revisions":
				limit := 128
				if kind == "local_revisions" {
					limit = 32
				}
				for i := 0; i <= limit; i++ {
					session := "s"
					if kind == "revisions" {
						session = fmt.Sprint("s", i/32)
						if i%32 == 0 {
							s.CreateSession(c, session)
						}
					}
					p, e := prepare(session, fmt.Sprint("r", i), "import")
					if i == limit {
						if !errors.Is(e, ErrBudget) {
							t.Fatal(i, e)
						}
						break
					}
					if e != nil {
						t.Fatal(i, e)
					}
					decide(p, "confirm")
				}
			case "previews":
				var first HookOperation
				for i := 0; i < 9; i++ {
					session := fmt.Sprint("s", i/4)
					if i%4 == 0 {
						s.CreateSession(c, session)
					}
					p, e := prepare(session, fmt.Sprint("p", i), "disable")
					if i == 8 {
						if !errors.Is(e, ErrBudget) {
							t.Fatal(e)
						}
						break
					}
					if e != nil {
						t.Fatal(e)
					}
					if i == 0 {
						first = p
					}
				}
				decide(first, "cancel")
			}
		})
	}
}
func hookManyRun(t *testing.T) (*Store, string) {
	t.Helper()
	s, _ := skillRunSetup(t)
	original, e := ValidateSkillManifest(readonlyManifest)
	if e != nil {
		t.Fatal(e)
	}
	m := original.Manifest
	m.ID = "hooks.slab"
	m.Inputs = map[string]SkillField{}
	m.Workflow.Nodes = []SkillNode{}
	m.Budget = SkillBudget{MaxIterations: 16, MaxToolCalls: 16}
	inputs := map[string]any{}
	for i := 0; i < 16; i++ {
		id := fmt.Sprintf("n%02d", i)
		m.Inputs[id] = SkillField{Type: "object"}
		capability := "workspace.read"
		inputs[id] = map[string]any{"path": "README.md"}
		if i == 15 {
			capability = "workspace.search"
			inputs[id] = map[string]any{"paths": []string{"README.md"}, "query": "install"}
		}
		m.Workflow.Nodes = append(m.Workflow.Nodes, SkillNode{ID: id, Capability: capability, DependsOn: []string{}})
	}
	raw, _ := json.Marshal(m)
	imported := skillDecide(t, s, skillPrepare(t, s, "slab-import", string(raw)), "slab-import-c", "confirm")
	enable, e := s.PrepareSkill(t.Context(), "s", SkillIntent{RequestID: "slab-enable", Kind: "enable", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID})
	if e != nil {
		t.Fatal(e)
	}
	skillDecide(t, s, enable, "slab-enable-c", "confirm")
	args, _ := json.Marshal(inputs)
	p, e := s.PrepareSkillRunWithHooks(t.Context(), "s", SkillRunIntent{RequestID: "slab-run", CatalogID: imported.CatalogID, RevisionID: imported.RevisionID, ExecutionProfile: SkillRunProfile, Inputs: args})
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.DecideSkillRunWithHooks(t.Context(), "s", OperatorDecision{RequestID: "slab-run-c", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil || a.V2 == nil || a.V2.RunID == "" {
		t.Fatal(a, e)
	}
	return s, a.V2.RunID
}
func TestHooksReceiptSlabBoundaries(t *testing.T) {
	s, id := hookManyRun(t)
	var slab, retained, reserved int
	if e := s.db.QueryRow(`SELECT h.reserved_bytes,s.retained_bytes,s.reserved_bytes FROM interaction_skill_run_hooks h JOIN interaction_skill_runs s USING(run_id) WHERE h.run_id=?`, id).Scan(&slab, &retained, &reserved); e != nil {
		t.Fatal(e)
	}
	if slab != 163840 || retained < slab || reserved != 8<<20 {
		t.Fatal(slab, retained, reserved)
	}
	hookOwnedFile(t, s)
	e, _ := NewSkillRunEngine(s, id)
	if err := e.Run(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.SkillHookReceipts(t.Context(), "s", id)
	if err != nil || len(receipts.Receipts) != 33 {
		t.Fatal(len(receipts.Receipts), err)
	}
	var max int
	s.db.QueryRow(`SELECT max(row_bytes) FROM interaction_skill_hook_receipts WHERE run_id=?`, id).Scan(&max)
	if max > 4096 {
		t.Fatal(max)
	}
	for _, query := range []string{`UPDATE interaction_skill_hook_receipts SET payload=payload WHERE run_id=?`, `DELETE FROM interaction_skill_hook_receipts WHERE run_id=?`, `INSERT OR REPLACE INTO interaction_skill_hook_receipts SELECT * FROM interaction_skill_hook_receipts WHERE run_id=? LIMIT 1`, `UPDATE interaction_skill_run_hooks SET snapshot=snapshot WHERE run_id=?`, `DELETE FROM interaction_skill_run_hooks WHERE run_id=?`, `UPDATE interaction_skill_runs SET reserved_bytes=8388608 WHERE run_id=?`} {
		if _, err = s.db.Exec(query, id); err == nil {
			t.Fatal("SQL guard bypass", query)
		}
	}
	if _, err = s.SkillHookReceipts(t.Context(), "foreign", id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
}
func TestHooksReceiptSlabExactSQLSizes(t *testing.T) {
	for _, size := range []int{4096, 4097} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, id := admitHookRun(t, "")
			e, r, cp := hookPending(t, s, id)
			if err := e.startSkillNode(t.Context(), r, "owned", &cp); err != nil {
				t.Fatal(err)
			}
			h, _, _ := hookRunSnapshotAt(t.Context(), s.db, id)
			receipt := hookReceipt(h, "s", id, HookMetadata{Event: "run_terminal", Status: "failed"}, ToolProposal{})
			raw, _ := json.Marshal(receipt)
			raw = append(raw, []byte(strings.Repeat(" ", size-hookReceiptBytes(receipt, raw)))...)
			_, err := s.db.Exec(`INSERT INTO interaction_skill_hook_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, "s", "run_terminal", "", 16, receipt.ReceiptID, "", "", receipt.SnapshotSHA256, string(raw), size)
			if (size == 4096) != (err == nil) {
				t.Fatal(size, err)
			}
			if size == 4096 {
				tx, _ := s.db.BeginTx(t.Context(), nil)
				err = writeHookReceipt(t.Context(), tx, receipt, 16)
				tx.Rollback()
				if !errors.Is(err, ErrReconciliation) {
					t.Fatal("noncanonical padded receipt trusted", err)
				}
			}
		})
	}
	for _, size := range []int{12288, 12289} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			s, id := admitHookRun(t, "")
			s.db.Exec(`DROP TRIGGER interaction_skill_hook_snapshot_retain`)
			var session, raw, hash, pinHash string
			s.db.QueryRow(`SELECT session_id,snapshot,snapshot_hash,pin_hash FROM interaction_skill_run_hooks WHERE run_id=?`, id).Scan(&session, &raw, &hash, &pinHash)
			s.db.Exec(`DELETE FROM interaction_skill_run_hooks WHERE run_id=?`, id)
			raw += strings.Repeat(" ", size-len(id)-len(session)-len(raw)-128-64)
			_, err := s.db.Exec(`INSERT INTO interaction_skill_run_hooks VALUES(?,?,?,?,?,163840)`, id, session, raw, hash, pinHash)
			if (size == 12288) != (err == nil) {
				t.Fatal(size, err)
			}
			if size == 12288 {
				if _, _, err = hookRunSnapshotAt(t.Context(), s.db, id); !errors.Is(err, ErrReconciliation) {
					t.Fatal("padded snapshot trusted", err)
				}
			}
		})
	}
}
func TestHooksTerminalAtEventCapacity(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			s, id := admitHookRun(t, "")
			result := ToolResult{Content: "durable observation", IsError: failed}
			e, _ := hookJournal(t, s, id, &result)
			if !failed {
				hookEnable(t, s, hookPolicyExample)
			}
			if _, err := s.db.Exec(`UPDATE interaction_skill_runs SET ordinary_events=512,control_events=60 WHERE run_id=?`, id); err != nil {
				t.Fatal(err)
			}
			if err := e.Recover(t.Context(), id); err != nil {
				t.Fatal(err)
			}
			var ordinary, controls, bytes, rows int
			read := func() string {
				s.db.QueryRow(`SELECT ordinary_events,control_events,retained_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&ordinary, &controls, &bytes)
				s.db.QueryRow(`SELECT count(*) FROM interaction_skill_hook_receipts WHERE run_id=?`, id).Scan(&rows)
				return fmt.Sprint(ordinary, controls, bytes, rows)
			}
			before := read()
			if ordinary != 512 || controls != 62 || rows != 3 {
				t.Fatal(before)
			}
			for i := 0; i < 20; i++ {
				if err := e.Recover(t.Context(), id); err != nil {
					t.Fatal(err)
				}
			}
			if before != read() {
				t.Fatal(before, read())
			}
		})
	}
}
func TestHooksReceiptAloneNeverClaims(t *testing.T) {
	s, id := admitHookRun(t, "")
	e, r, cp := hookPending(t, s, id)
	h, _, _ := hookRunSnapshotAt(t.Context(), s.db, id)
	receipt := hookReceipt(h, "s", id, HookMetadata{"before_node", cp.Pending[0].Call.ID, "workspace.read", "ready"}, cp.Pending[0])
	tx, _ := s.db.BeginTx(t.Context(), nil)
	if err := writeHookReceipt(t.Context(), tx, receipt, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := e.startSkillNode(t.Context(), r, "owned", &cp); !errors.Is(err, ErrReconciliation) {
		t.Fatal("orphan receipt authorized start", err)
	}
}

func TestHooksManagementConcurrentFinalReservedSlots(t *testing.T) {
	s, c := setup(t)
	for j := 0; j < 4; j++ {
		session := fmt.Sprint("session", j)
		if e := s.CreateSession(c, session); e != nil {
			t.Fatal(e)
		}
		n := 64
		if j == 3 {
			n = 62
		}
		for i := 0; i < n; i++ {
			p, e := s.PrepareHooks(c, session, HookIntent{RequestID: fmt.Sprintf("p%d-%d", j, i), Kind: "disable"})
			if e != nil {
				t.Fatal(e)
			}
			if _, e = s.DecideHooks(c, session, OperatorDecision{RequestID: p.OperationID + "c", OperationID: p.OperationID, Digest: p.Digest, Decision: "cancel"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	type result struct {
		p HookOperation
		e error
	}
	results := make(chan result, 3)
	for i := 0; i < 3; i++ {
		go func(i int) {
			p, e := s.PrepareHooks(c, "session3", HookIntent{RequestID: fmt.Sprint("final", i), Kind: "disable"})
			results <- result{p, e}
		}(i)
	}
	var admitted []HookOperation
	refused := 0
	for i := 0; i < 3; i++ {
		v := <-results
		if errors.Is(v.e, ErrBudget) {
			refused++
		} else if v.e != nil {
			t.Fatal(v.e)
		} else {
			admitted = append(admitted, v.p)
		}
	}
	if len(admitted) != 2 || refused != 1 {
		t.Fatal(len(admitted), refused)
	}
	for _, p := range admitted {
		if _, e := s.DecideHooks(c, "session3", OperatorDecision{RequestID: p.OperationID + "c", OperationID: p.OperationID, Digest: p.Digest, Decision: "cancel"}); e != nil {
			t.Fatal("reserved decision blocked", e)
		}
	}
	var ops, requests, bytes int
	s.db.QueryRow(`SELECT count(*),sum(reserved_bytes) FROM interaction_hook_operations`).Scan(&ops, &bytes)
	s.db.QueryRow(`SELECT count(*) FROM interaction_hook_requests`).Scan(&requests)
	if ops != 256 || requests != 512 || bytes != 16<<20 {
		t.Fatal(ops, requests, bytes)
	}
}
func TestHooksReceiptSlabConcurrentTerminalSlot(t *testing.T) {
	s, id := hookManyRun(t)
	hookOwnedFile(t, s)
	e, err := NewSkillRunEngine(s, id)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.GetRun(t.Context(), id)
	cp, err := e.claim(t.Context(), r, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.loop(t.Context(), r, "owner", &cp); err != nil {
		t.Fatal(err)
	}
	before, _ := s.SkillHookReceipts(t.Context(), "s", id)
	if len(before.Receipts) != 32 {
		t.Fatal(len(before.Receipts))
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			copy := cp
			copy.Phase = PhaseCompleted
			results <- e.finish(t.Context(), r, "owner", &copy, RunCompleted)
		}()
	}
	success, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	after, err := s.SkillHookReceipts(t.Context(), "s", id)
	var terminalEvents int
	s.db.QueryRow(`SELECT count(*) FROM interaction_events WHERE run_id=? AND type='run.completed'`, id).Scan(&terminalEvents)
	if success != 1 || conflicts != 1 || err != nil || len(after.Receipts) != 33 || terminalEvents != 1 {
		t.Fatal(success, conflicts, len(after.Receipts), terminalEvents, err)
	}
	last := after.Receipts[32]
	raw, _ := json.Marshal(last)
	if _, err = s.db.Exec(`INSERT INTO interaction_skill_hook_receipts VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, "s", "run_terminal", "", 16, last.ReceiptID, "", "", last.SnapshotSHA256, string(raw), hookReceiptBytes(last, raw)); err == nil {
		t.Fatal("SQL accepted 34th receipt")
	}
}
func TestHooksReceiptSlabUsesExistingGlobalPool(t *testing.T) {
	s, id := admitHookRun(t, "")
	seed := func(tx *sql.Tx, n int) error {
		run := fmt.Sprint("pool", n)
		if _, e := tx.Exec(`INSERT INTO interaction_runs(id,session_id,request_id,message,state,version,execution_kind) VALUES(?,'s',?,'{}','failed',1,'skill_readonly_v1')`, run, run); e != nil {
			return e
		}
		_, e := tx.Exec(`INSERT INTO interaction_skill_runs(run_id,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan,terminal,retained_bytes,reserved_bytes) SELECT ?,session_id,catalog_id,revision_id,pin,pin_hash,inputs,plan,terminal,retained_bytes,reserved_bytes FROM interaction_skill_runs WHERE run_id=?`, run, id)
		return e
	}
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i < 32; i++ {
		if e = seed(tx, i); e != nil {
			tx.Rollback()
			t.Fatal(i, e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	tx, _ = s.db.BeginTx(t.Context(), nil)
	e = seed(tx, 32)
	tx.Rollback()
	if e == nil {
		t.Fatal("33rd 8MiB reservation")
	}
	if _, e = s.db.Exec(`UPDATE interaction_skill_runs SET retained_bytes=8388609 WHERE run_id=?`, id); e == nil {
		t.Fatal("8MiB cap")
	}
}
