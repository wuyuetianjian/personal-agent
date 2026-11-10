package interaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestSkillOutputReservationsBoundAndDeduplicate(t *testing.T) {
	s, id := admitTestSkillRun(t)
	b, e := NewSkillRunBroker(s, id)
	if e != nil {
		t.Fatal(e)
	}
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	sink := skillOutputSink{store: s, runID: id}
	data := bytes.Repeat([]byte("x"), 65536)
	for i := 0; i < 2; i++ {
		if _, e = sink.artifact(t.Context(), p.ID, "raw_file", data); e != nil {
			t.Fatal(e)
		}
	}
	var spent int
	s.db.QueryRow(`SELECT output_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&spent)
	if spent != len(data) {
		t.Fatal(spent)
	}
	if _, e = sink.artifact(t.Context(), p.ID, "raw_file", []byte("changed")); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = sink.artifact(t.Context(), p.ID, "search_text", bytes.Repeat([]byte("x"), 1048576)); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	var count int
	s.db.QueryRow(`SELECT count(*) FROM interaction_artifacts`).Scan(&count)
	if count != 1 {
		t.Fatal(count)
	}
	oversized := ToolResult{Content: string(bytes.Repeat([]byte("\x00"), 12000))}
	raw, _ := json.Marshal(oversized)
	if len(raw) <= 65536 {
		t.Fatal("fixture")
	}
	if e = sink.inline(t.Context(), p.ID, raw); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
}
func TestSkillOutputArtifactFailureRollsBackReservation(t *testing.T) {
	s, id := admitTestSkillRun(t)
	b, _ := NewSkillRunBroker(s, id)
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec(`CREATE TRIGGER reject_test_artifact BEFORE INSERT ON interaction_artifacts BEGIN SELECT RAISE(ABORT,'test'); END`)
	sink := skillOutputSink{store: s, runID: id}
	if _, e = sink.artifact(t.Context(), p.ID, "raw_file", []byte("payload")); e == nil {
		t.Fatal("insert succeeded")
	}
	var spent, n int
	s.db.QueryRow(`SELECT output_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&spent)
	s.db.QueryRow(`SELECT count(*) FROM interaction_skill_output_slots`).Scan(&n)
	if spent != 0 || n != 0 {
		t.Fatal(spent, n)
	}
}
func TestSkillOutputExactEnvelopeAndAggregateBoundaries(t *testing.T) {
	s, id := admitTestSkillRun(t)
	b, _ := NewSkillRunBroker(s, id)
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	sink := skillOutputSink{store: s, runID: id}
	empty, _ := json.Marshal(ToolResult{})
	envelope, _ := json.Marshal(ToolResult{Content: strings.Repeat("a", 65536-len(empty))})
	if len(envelope) != 65536 {
		t.Fatal(len(envelope))
	}
	if e = sink.inline(t.Context(), p.ID, envelope); e != nil {
		t.Fatal(e)
	}
	if _, e = sink.artifact(t.Context(), p.ID, "raw_file", bytes.Repeat([]byte("b"), 1048576-len(envelope))); e != nil {
		t.Fatal(e)
	}
	if _, e = sink.artifact(t.Context(), p.ID, "search_text", []byte("x")); !errors.Is(e, ErrBudget) {
		t.Fatal(e)
	}
	var spent int
	s.db.QueryRow(`SELECT output_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&spent)
	if spent != 1048576 {
		t.Fatal(spent)
	}
}
func TestSkillOutputConcurrentSameSlotChargesOnce(t *testing.T) {
	s, id := admitTestSkillRun(t)
	b, _ := NewSkillRunBroker(s, id)
	cp, _ := s.EngineCheckpoint(t.Context(), id)
	p, e := b.Propose(t.Context(), id, cp.Calls[0])
	if e != nil {
		t.Fatal(e)
	}
	sink := skillOutputSink{store: s, runID: id}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sink.artifact(t.Context(), p.ID, "raw_file", []byte("data")); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var spent int
	s.db.QueryRow(`SELECT output_bytes FROM interaction_skill_runs WHERE run_id=?`, id).Scan(&spent)
	if spent != 4 {
		t.Fatal(spent)
	}
}
func TestSkillReadLongArtifactDescriptorIsNotWrappedTwice(t *testing.T) {
	s, in := skillRunSetup(t)
	w, _ := s.Workspace(t.Context(), "s")
	path := strings.Repeat(strings.Repeat("a", 55)+"/", 69) + "file" // safe components, near the native pathname bound
	if e := os.MkdirAll(filepath.Dir(filepath.Join(w.Policy.Root, path)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(w.Policy.Root, path), bytes.Repeat([]byte("x"), 5000), 0600); e != nil {
		t.Fatal(e)
	}
	inputs := map[string]any{"first": map[string]string{"path": path}, "second": map[string]any{"paths": []string{path}, "query": "install"}}
	in.Inputs, _ = json.Marshal(inputs)
	p, e := s.PrepareSkillRun(t.Context(), "s", in)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.DecideSkillRun(t.Context(), "s", OperatorDecision{RequestID: "confirm", OperationID: p.OperationID, Digest: p.Digest, Decision: "confirm"})
	if e != nil {
		t.Fatal(e)
	}
	engine, e := NewSkillRunEngine(s, a.RunID)
	if e != nil {
		t.Fatal(e)
	}
	if e = engine.Run(t.Context(), a.RunID); e != nil {
		t.Fatal(e)
	}
	r, e := s.SkillRun(t.Context(), "s", a.RunID)
	if e != nil || r.Execution != "completed" {
		t.Fatal(r, e)
	}
	if r.Nodes[0].Result == nil || len(r.Nodes[0].Result.Content) <= MaxInlineToolBytes {
		t.Fatal("fixture did not exercise long descriptor", r.Nodes[0])
	}
	var n int
	s.db.QueryRow(`SELECT count(*) FROM interaction_skill_output_slots WHERE run_id=? AND slot='search_text'`, a.RunID).Scan(&n)
	if n != 0 {
		t.Fatal("read descriptor wrapped again", n)
	}
}
