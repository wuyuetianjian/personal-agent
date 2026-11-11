package interaction

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func hookPrepare(t *testing.T, s *Store, request, kind, revision, policy string) HookOperation {
	t.Helper()
	p, e := s.PrepareHooks(t.Context(), "s", HookIntent{RequestID: request, Kind: kind, RevisionID: revision, PolicyText: policy})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func hookDecide(t *testing.T, s *Store, p HookOperation, request, decision string) HookOperation {
	t.Helper()
	a, e := s.DecideHooks(t.Context(), p.SessionID, OperatorDecision{RequestID: request, OperationID: p.OperationID, Digest: p.Digest, Decision: decision})
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func hookEnable(t *testing.T, s *Store, policy string) HookOperation {
	t.Helper()
	p := hookPrepare(t, s, "hook-import", "import", "", policy)
	a := hookDecide(t, s, p, "hook-import-confirm", "confirm")
	return hookDecide(t, s, hookPrepare(t, s, "hook-enable", "enable", a.RevisionID, ""), "hook-enable-confirm", "confirm")
}
func TestHooksPrepareConfirmReplay(t *testing.T) {
	s, c := setup(t)
	initial, e := s.HookSelection(c, "s")
	if e != nil || initial.Generation != 0 || initial.State != "disabled" {
		t.Fatal(initial, e)
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM interaction_hook_sessions`).Scan(&count)
	if count != 0 {
		t.Fatal("read created configuration")
	}
	p := hookPrepare(t, s, "import", "import", "", hookPolicyExample)
	sel, _ := s.HookSelection(c, "s")
	if sel.Generation != 0 || sel.State != "disabled" {
		t.Fatal(sel)
	}
	raw, _ := json.Marshal(HookIntent{RequestID: "import", Kind: "import", PolicyText: hookPolicyExample})
	if _, e = DecodeHookIntent(raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{string(raw[:len(raw)-1]) + `,"script":"x"}`, `{"request_id":"a","kind":"disable","revision_id":"","policy_text":null}`} {
		if _, e = DecodeHookIntent([]byte(bad)); e == nil {
			t.Fatal("bad intent")
		}
	}
	a := hookDecide(t, s, p, "confirm", "confirm")
	if a.Reason != "imported" || a.State != "applied" {
		t.Fatal(a)
	}
	replay, e := s.HookOperation(c, "s", "import")
	if e != nil || operatorHash(replay) != operatorHash(a) {
		t.Fatal(replay, e)
	}
	replay = hookDecide(t, s, p, "confirm", "confirm")
	if operatorHash(replay) != operatorHash(a) {
		t.Fatal("replay changed")
	}
	for _, d := range []OperatorDecision{{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "cancel"}, {RequestID: "different", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}} {
		if _, e = s.DecideHooks(c, "s", d); !errors.Is(e, ErrConflict) {
			t.Fatal(e)
		}
	}
	if _, e = s.HookRevision(c, "foreign", a.RevisionID); e == nil {
		t.Fatal("foreign revision")
	}
	if _, e = s.HookOperation(c, "foreign", "import"); e == nil {
		t.Fatal("foreign operation")
	}
	enabled := hookDecide(t, s, hookPrepare(t, s, "enable", "enable", a.RevisionID, ""), "enable-confirm", "confirm")
	if enabled.Reason != "enabled" {
		t.Fatal(enabled)
	}
	sel, _ = s.HookSelection(c, "s")
	if sel.Generation != 1 || sel.RevisionID != a.RevisionID {
		t.Fatal(sel)
	}
	same := hookDecide(t, s, hookPrepare(t, s, "same", "enable", a.RevisionID, ""), "same-confirm", "confirm")
	if same.Reason != "unchanged" {
		t.Fatal(same)
	}
	hookDecide(t, s, hookPrepare(t, s, "disable", "disable", "", ""), "disable-confirm", "confirm")
	sel, _ = s.HookSelection(c, "s")
	if sel.Generation != 2 || sel.State != "disabled" {
		t.Fatal(sel)
	}
}
func TestHooksSelectionCAS(t *testing.T) {
	s, c := setup(t)
	a := hookDecide(t, s, hookPrepare(t, s, "import", "import", "", hookPolicyExample), "import-c", "confirm")
	p := hookPrepare(t, s, "one", "enable", a.RevisionID, "")
	q := hookPrepare(t, s, "two", "enable", a.RevisionID, "")
	var wg sync.WaitGroup
	for _, p := range []HookOperation{p, q} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.DecideHooks(c, "s", OperatorDecision{RequestID: p.OperationID + "-c", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
			if e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	sel, _ := s.HookSelection(c, "s")
	if sel.Generation != 1 {
		t.Fatal(sel)
	}
	x, _ := s.HookOperation(c, "s", "one")
	y, _ := s.HookOperation(c, "s", "two")
	if (x.State == "applied") == (y.State == "applied") {
		t.Fatal(x, y)
	}
}
func TestHooksManagementCapacity(t *testing.T) {
	s, c := setup(t)
	for i := 0; i < 4; i++ {
		hookPrepare(t, s, fmt.Sprint("p", i), "disable", "", "")
	}
	if _, e := s.PrepareHooks(c, "s", HookIntent{RequestID: "overflow", Kind: "disable"}); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	p, _ := s.HookOperation(c, "s", "p0")
	hookDecide(t, s, p, "p0-c", "cancel")
	for i := 4; i < 64; i++ {
		p := hookPrepare(t, s, fmt.Sprint("p", i), "disable", "", "")
		hookDecide(t, s, p, fmt.Sprint("c", i), "cancel")
	}
	if _, e := s.PrepareHooks(c, "s", HookIntent{RequestID: "overflow-lifetime", Kind: "disable"}); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	for i := 1; i < 4; i++ {
		p, _ := s.HookOperation(c, "s", fmt.Sprint("p", i))
		hookDecide(t, s, p, fmt.Sprint("last", i), "cancel")
	}
	var ops, requests, bytes int
	s.db.QueryRow(`SELECT count(*),sum(reserved_bytes) FROM interaction_hook_operations`).Scan(&ops, &bytes)
	s.db.QueryRow(`SELECT count(*) FROM interaction_hook_requests`).Scan(&requests)
	if ops != 64 || requests != 128 || bytes != 64*65536 {
		t.Fatal(ops, requests, bytes)
	}
}
func TestHooksManagementRollback(t *testing.T) {
	s, c := setup(t)
	p := hookPrepare(t, s, "import", "import", "", hookPolicyExample)
	s.db.Exec(`CREATE TRIGGER fault_hook_audit BEFORE INSERT ON interaction_hook_audits BEGIN SELECT RAISE(ABORT,'fixture'); END`)
	if _, e := s.DecideHooks(c, "s", OperatorDecision{RequestID: "c", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"}); e == nil {
		t.Fatal("fault ignored")
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_hook_revisions`).Scan(&n)
	if n != 0 {
		t.Fatal(n)
	}
	a, _ := s.HookOperation(c, "s", p.OperationID)
	if a.State != "pending" {
		t.Fatal(a)
	}
}

func TestHooksManagementStrictCursorAndExpiry(t *testing.T) {
	s, c := setup(t)
	p := hookDecide(t, s, hookPrepare(t, s, "import", "import", "", hookPolicyExample), "import-c", "confirm")
	for _, v := range []any{[]string{"s", p.RevisionID, "ignored"}, []string{"foreign", p.RevisionID}} {
		b, _ := json.Marshal(v)
		if _, e := s.ListHookRevisions(c, "s", base64.RawURLEncoding.EncodeToString(b), 1); e == nil {
			t.Fatal("non-exact cursor")
		}
	}
	q := hookPrepare(t, s, "expire", "disable", "", "")
	q.ExpiresAtMS = time.Now().Add(-time.Second).UnixMilli()
	q.Digest = hookOperationDigest(q)
	raw, _ := json.Marshal(q)
	s.db.Exec(`DROP TRIGGER interaction_hook_operation_transition`)
	if _, e := s.db.Exec(`UPDATE interaction_hook_operations SET preview=?,result=?,expires_at_ms=? WHERE id=?`, string(raw), string(raw), q.ExpiresAtMS, q.OperationID); e != nil {
		t.Fatal(e)
	}
	a := hookDecide(t, s, q, "expire-c", "confirm")
	if a.State != "stale" || a.Reason != "preview_expired" {
		t.Fatal(a)
	}
}
