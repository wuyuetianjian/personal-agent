package interaction

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestSkillControlReservationSharedBoundaryAndConcurrency(t *testing.T) {
	s, id := admitTestSkillRun(t)
	tx, e := s.db.BeginTx(t.Context(), nil)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2045; i++ {
		if _, e = tx.Exec(`INSERT INTO interaction_skill_controls VALUES(?,?,?,4096)`, fmt.Sprintf("control-%d", i), id, skillBytesHash("fp")); e != nil {
			tx.Rollback()
			t.Fatal(e)
		}
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, req := range []string{"last-a", "last-b"} {
		wg.Add(1)
		go func(req string) {
			defer wg.Done()
			results <- s.ReserveSkillControl(t.Context(), id, req, skillBytesHash(req))
		}(req)
	}
	wg.Wait()
	close(results)
	wins, blocked := 0, 0
	for e := range results {
		if e == nil {
			wins++
		} else if errors.Is(e, ErrBudget) {
			blocked++
		} else {
			t.Fatal(e)
		}
	}
	if wins != 1 || blocked != 1 {
		t.Fatal(wins, blocked)
	}
	var cancelled bool
	s.db.QueryRow(`SELECT cancel_requested FROM interaction_engine WHERE run_id=?`, id).Scan(&cancelled)
	if cancelled {
		t.Fatal("reservation executed control")
	}
}
func TestSkillControlAmbiguousReservationNeverReplayed(t *testing.T) {
	s, id := admitTestSkillRun(t)
	fp := skillBytesHash("fp")
	if e := s.ReserveSkillControl(t.Context(), id, "control", fp); e != nil {
		t.Fatal(e)
	}
	if e := s.ReserveSkillControl(t.Context(), id, "control", fp); !errors.Is(e, ErrReconciliation) {
		t.Fatal(e)
	}
	if e := s.ReserveSkillControl(t.Context(), id, "control", skillBytesHash("different")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
}
