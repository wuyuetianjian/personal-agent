//go:build linux

package interaction

import (
	"agent/internal/model"
	"agent/internal/storage"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// New host/session opt-in must be required even when mode already allows edits.
func TestExecExplicitHostAndSessionOptIn(t *testing.T) {
	for _, mode := range []PermissionMode{ModeDefault, ModeAcceptEdits, ModePlan} {
		for _, host := range []bool{false, true} {
			for _, session := range []bool{false, true} {
				t.Run(string(mode)+stringBool(host)+stringBool(session), func(t *testing.T) {
					s, ctx := setup(t)
					root := t.TempDir()
					// JSON lets this test demonstrate the old implementation ignoring new policy.
					var policy WorkspacePolicy
					raw, _ := json.Marshal(map[string]any{"Root": root, "Mode": mode, "NonSandboxExec": session})
					if err := json.Unmarshal(raw, &policy); err != nil {
						t.Fatal(err)
					}
					w, err := s.BindWorkspace(ctx, "s", policy)
					if err != nil {
						t.Fatal(err)
					}
					r, err := s.CreateRun(ctx, "s", "r", Message{})
					if err != nil {
						t.Fatal(err)
					}
					b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: host})
					call := execCall(w, "/bin/echo", []string{"safe"}, nil, 1000)
					p, err := b.Propose(ctx, r.ID, call)
					allowed := host && session && mode != ModePlan
					if allowed && (err != nil || !p.RequiresApproval) {
						t.Fatalf("explicit opt-in must prepare exact approval: %+v %v", p, err)
					}
					if !allowed && err == nil {
						t.Fatal("exec prepared without opt-in or in plan")
					}
				})
			}
		}
	}
}
func stringBool(v bool) string {
	if v {
		return "-yes"
	}
	return "-no"
}
func execCall(w WorkspaceBinding, program string, argv []string, env map[string]string, timeout int) ToolCall {
	raw, _ := json.Marshal(ExecArguments{Scope: w.ScopeID, Program: program, Argv: argv, Cwd: w.Policy.Root, Environment: env, TimeoutMillis: timeout})
	return ToolCall{ID: "exec", Name: "exec", Arguments: string(raw)}
}

func TestExecNoApprovalNoProcess(t *testing.T) {
	s, b, w, id := execFixture(t, ModeAcceptEdits)
	target := filepath.Join(w.Policy.Root, "ran")
	call := execCall(w, "/bin/sh", []string{"-c", "printf ran > \"$1\"", "test", target}, nil, 1000)
	p, err := b.Propose(context.Background(), id, call)
	if err != nil {
		t.Fatal(err)
	}
	if got, e := b.Execute(context.Background(), p.ID); e != nil || !got.IsError {
		t.Fatal(got, e)
	}
	e := runWorkspaceTool(t, s, b, id, call)
	if err = e.Run(context.Background(), id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("process ran without approval", err)
	}
	if !strings.Contains(p.Call.Arguments, "printf ran") {
		t.Fatal(p)
	}
}

func execFixture(t *testing.T, mode PermissionMode) (*Store, *WorkspaceBroker, WorkspaceBinding, string) {
	t.Helper()
	s, ctx := setup(t)
	w, err := s.BindWorkspace(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: mode, NonSandboxExec: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRun(ctx, "s", "r", Message{})
	if err != nil {
		t.Fatal(err)
	}
	return s, NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: true}), w, r.ID
}

func approveExec(t *testing.T, s *Store, e *Engine, id string) ToolProposal {
	t.Helper()
	ctx := context.Background()
	if err := e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	cp, err := e.Checkpoint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	p := cp.Pending[0]
	if err = s.DecideApproval(ctx, p.ID, p.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	return p
}
func execObservation(t *testing.T, s *Store, id string) map[string]any {
	t.Helper()
	cp, err := s.EngineCheckpoint(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range cp.Messages {
		if m.Role == "tool" {
			var v map[string]any
			if err = json.Unmarshal([]byte(m.Content), &v); err != nil {
				t.Fatal(m.Content, err)
			}
			return v
		}
	}
	t.Fatal("missing tool result")
	return nil
}
func TestExecExactArgumentsEnvironmentOutputAndExit(t *testing.T) {
	s, b, w, id := execFixture(t, ModeAcceptEdits)
	t.Setenv("PA_AGENT_TOKEN", "test-only-not-inherited")
	t.Setenv("OPENAI_API_KEY", "test-only-not-inherited")
	script := `printf '%s|%s|%s|%s|%s' "$1" "$2" "$CUSTOM" "${PA_AGENT_TOKEN-unset}" "${OPENAI_API_KEY-unset}"; printf '错误 stderr' >&2; exit 7`
	call := execCall(w, "/bin/sh", []string{"-c", script, "fixture", "space value", "你好"}, map[string]string{"CUSTOM": "value 空格"}, 1000)
	e := runWorkspaceTool(t, s, b, id, call)
	p := approveExec(t, s, e, id)
	e.broker = NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: true})
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	v := execObservation(t, s, id)
	if v["stdout"] != "space value|你好|value 空格|unset|unset" || v["stderr"] != "错误 stderr" || v["exit_code"] != float64(7) || v["direct_child_waited"] != true || v["output_complete"] != true || v["descendants_stopped"] != false {
		t.Fatal(v)
	}
	if got, err := b.Execute(context.Background(), p.ID); err != nil || !got.IsError {
		t.Fatal("replay", got, err)
	}
}
func TestExecBoundedOutput(t *testing.T) {
	s, b, w, id := execFixture(t, ModeDefault)
	call := execCall(w, "/bin/sh", []string{"-c", `i=0; while [ "$i" -lt 2000 ]; do printf '12345678901234567890'; printf '12345678901234567890' >&2; i=$((i+1)); done`}, nil, 3000)
	e := runWorkspaceTool(t, s, b, id, call)
	approveExec(t, s, e, id)
	if err := e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	v := execObservation(t, s, id)
	if len(v["stdout"].(string)) != MaxExecOutputBytes || len(v["stderr"].(string)) != MaxExecOutputBytes || v["stdout_truncated"] != true || v["stderr_truncated"] != true {
		t.Fatal(v)
	}
}
func TestExecRevalidatesApprovalAndFiles(t *testing.T) {
	for _, mutation := range []string{"args", "root", "cwd", "executable", "host"} {
		t.Run(mutation, func(t *testing.T) {
			s, b, w, id := execFixture(t, ModeDefault)
			program := filepath.Join(t.TempDir(), "script")
			if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf ran"), 0700); err != nil {
				t.Fatal(err)
			}
			cwd := t.TempDir()
			call := execCall(w, program, nil, nil, 1000)
			var a ExecArguments
			json.Unmarshal([]byte(call.Arguments), &a)
			a.Cwd = cwd
			raw, _ := json.Marshal(a)
			call.Arguments = string(raw)
			e := runWorkspaceTool(t, s, b, id, call)
			p := approveExec(t, s, e, id)
			switch mutation {
			case "args":
				cp, _ := e.Checkpoint(context.Background(), id)
				cp.Pending[0].Call.Arguments = strings.Replace(call.Arguments, "1000", "1001", 1)
				raw, _ := json.Marshal(cp)
				if _, err := s.db.Exec("UPDATE interaction_engine SET checkpoint=? WHERE run_id=?", string(raw), id); err != nil {
					t.Fatal(err)
				}
			case "root", "cwd":
				dir := w.Policy.Root
				if mutation == "cwd" {
					dir = cwd
				}
				if err := os.Rename(dir, dir+"-old"); err != nil {
					t.Fatal(err)
				}
				defer os.RemoveAll(dir + "-old")
				if err := os.Mkdir(dir, 0700); err != nil {
					t.Fatal(err)
				}
			case "executable":
				if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "host":
				e.broker = NewWorkspaceBroker(s)
			}
			_ = e.Run(context.Background(), id)
			cp, _ := e.Checkpoint(context.Background(), id)
			for _, m := range cp.Messages {
				if m.Role == "tool" && !strings.Contains(m.Content, "Tool refused:") {
					t.Fatal("mutated proposal executed", m.Content)
				}
			}
			if got, err := b.Execute(context.Background(), p.ID); err != nil || !got.IsError {
				t.Fatal(got, err)
			}
		})
	}
}
func TestExecTimeoutRequiresReconciliationAndNeverRepeats(t *testing.T) {
	s, b, w, id := execFixture(t, ModeDefault)
	marker := filepath.Join(w.Policy.Root, "count")
	call := execCall(w, "/bin/sh", []string{"-c", `printf once >> "$1"; exec /bin/sleep 3`, "test", marker}, nil, 80)
	e := runWorkspaceTool(t, s, b, id, call)
	approveExec(t, s, e, id)
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal("timeout must be unknown", err)
	}
	if err := e.Run(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal("retry allowed", err)
	}
	if err := e.Recover(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal("recovery allowed retry", err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "once" {
		t.Fatal(string(raw), err)
	}
}

func TestExecReviewBindingAndUncertainObservation(t *testing.T) {
	s, b, w, id := execFixture(t, ModeDefault)
	call := execCall(w, "/bin/sh", []string{"-c", "exec /bin/sleep 2"}, map[string]string{"CUSTOM": "explicit"}, 50)
	e := runWorkspaceTool(t, s, b, id, call)
	p := approveExec(t, s, e, id)
	saved, err := b.loadPrepared(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewJSON, _ := json.Marshal(saved.ExecReview)
	if !strings.Contains(string(reviewJSON), `"environment_changes":[{"name":"CUSTOM","value":"explicit"}]`) {
		t.Fatal("explicit changes not separately reviewable", string(reviewJSON))
	}
	if saved.ExecReview == nil || saved.ExecReview.Warning != NonSandboxExecWarning || saved.ExecReview.Argv[1] != "exec /bin/sleep 2" {
		t.Fatal(saved)
	}
	base, _ := ProposalDigest(id, call)
	if p.Digest == base || p.Digest != execDigest(base, saved.ExecReview) {
		t.Fatal("review not approval-bound", p)
	}
	if err = e.Run(context.Background(), id); !errors.Is(err, ErrReconciliation) {
		t.Fatal(err)
	}
	events, err := s.Events(context.Background(), "s", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Type == "tool.uncertain" {
			found = true
			if !strings.Contains(string(ev.Payload), "unknown_requires_reconciliation") || !strings.Contains(string(ev.Payload), `\"descendants_stopped\":false`) {
				t.Fatal(string(ev.Payload))
			}
		}
	}
	if !found {
		t.Fatal("unknown output facts discarded")
	}
}

func TestExecCancellationAndEscapedDescendantUncertainty(t *testing.T) {
	for _, variant := range []string{"cancel", "timeout_escape", "pipe_escape"} {
		t.Run(variant, func(t *testing.T) {
			s, b, w, id := execFixture(t, ModeDefault)
			ctx := context.Background()
			ready := filepath.Join(w.Policy.Root, "ready")
			late := filepath.Join(w.Policy.Root, "late")
			pidFile := filepath.Join(w.Policy.Root, "escaped.pid")
			script := `printf ready > "$1"; exec /bin/sleep 3`
			timeout := 2000
			if variant != "cancel" {
				if _, err := os.Stat("/usr/bin/setsid"); err != nil {
					t.Skip("fixture requires system setsid")
				}
				// Controlled descendant self-exits; additionally kill its recorded PID on cleanup.
				script = `/usr/bin/setsid /bin/sh -c 'echo $$ > "$1"; /bin/sleep .4; printf late > "$2"' fixture "$3" "$2" & printf ready > "$1"; `
				if variant == "timeout_escape" {
					script += `exec /bin/sleep 3`
					timeout = 80
				} else {
					script += `exit 0`
				}
				t.Cleanup(func() {
					raw, _ := os.ReadFile(pidFile)
					pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
					if pid > 0 {
						_ = syscall.Kill(-pid, syscall.SIGKILL)
					}
				})
			}
			call := execCall(w, "/bin/sh", []string{"-c", script, "fixture", ready, late, pidFile}, nil, timeout)
			e := runWorkspaceTool(t, s, b, id, call)
			approveExec(t, s, e, id)
			done := make(chan error, 1)
			go func() { done <- e.Run(ctx, id) }()
			until := time.Now().Add(2 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(until) {
					t.Fatal("child did not start")
				}
				time.Sleep(time.Millisecond)
			}
			if variant == "cancel" {
				if err := e.Cancel(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if !errors.Is(err, ErrReconciliation) {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("wait not bounded")
			}
			if variant != "cancel" {
				until = time.Now().Add(time.Second)
				for {
					if _, err := os.Stat(late); err == nil {
						break
					}
					if time.Now().After(until) {
						t.Fatal("fixture escaped descendant did not make late effect")
					}
					time.Sleep(time.Millisecond)
				}
			}
			if err := e.Run(ctx, id); !errors.Is(err, ErrReconciliation) {
				t.Fatal("uncertain rerun accepted", err)
			}
		})
	}
}

func TestExecDurableApprovalAcrossReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = storage.Migrate(ctx, db.SQL); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db.SQL)
	if err = s.CreateSession(ctx, "s"); err != nil {
		t.Fatal(err)
	}
	w, err := s.BindWorkspace(ctx, "s", WorkspacePolicy{Root: t.TempDir(), Mode: ModeAcceptEdits, NonSandboxExec: true})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateRun(ctx, "s", "r", Message{})
	if err != nil {
		t.Fatal(err)
	}
	b := NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: true})
	call := execCall(w, "/bin/echo", []string{"restart 你好"}, nil, 1000)
	e := runWorkspaceTool(t, s, b, r.ID, call)
	p := approveExec(t, s, e, r.ID)
	review, err := b.ApprovalReview(ctx, "s", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.ApprovalReview(ctx, "other", p.ID); err == nil {
		t.Fatal("cross-session review")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = NewStore(db.SQL)
	b = NewWorkspaceBrokerWithOptions(s, WorkspaceBrokerOptions{NonSandboxExec: true})
	p2, err := b.Propose(ctx, r.ID, call)
	if err != nil || p2.Digest != p.Digest || p2.ID != p.ID || !p2.ExpiresAt.Equal(p.ExpiresAt) {
		t.Fatal(p, p2, err)
	}
	review2, err := b.ApprovalReview(ctx, "s", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(review)
	raw2, _ := json.Marshal(review2)
	if string(raw) != string(raw2) {
		t.Fatal("review drift")
	}
	e, err = NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		return model.TurnResult{Content: "done"}, nil
	}), b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	v := execObservation(t, s, r.ID)
	if v["stdout"] != "restart 你好\n" {
		t.Fatal(v)
	}
	if result, err := b.Execute(ctx, p.ID); err != nil || !result.IsError {
		t.Fatal("replayed", result, err)
	}
}

func TestExecReviewDeclaresPinnedLaunchAndBinaryOutputIsLossless(t *testing.T) {
	s, b, w, id := execFixture(t, ModeDefault)
	call := execCall(w, "/bin/sh", []string{"-c", `printf '\377\000'`}, nil, 1000)
	e := runWorkspaceTool(t, s, b, id, call)
	p := approveExec(t, s, e, id)
	review, err := b.ApprovalReview(context.Background(), "s", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(review)
	if !strings.Contains(string(raw), "/proc/self/fd/3") {
		t.Fatal("pinned executable/script launch not declared", string(raw))
	}
	if err = e.Run(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	v := execObservation(t, s, id)
	if v["stdout_base64"] != "/wA=" {
		t.Fatal("lost binary stdout", v)
	}
}

func TestExecFreshApprovalPerInvocation(t *testing.T) {
	s, b, w, id := execFixture(t, ModeAcceptEdits)
	ctx := context.Background()
	marker := filepath.Join(w.Policy.Root, "count")
	one := execCall(w, "/bin/sh", []string{"-c", `printf x >> "$1"`, "fixture", marker}, nil, 1000)
	two := one
	two.ID = "second"
	turns := 0
	e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
		turns++
		if turns == 1 {
			return model.TurnResult{ToolCalls: []model.ToolCall{one, two}}, nil
		}
		return model.TurnResult{Content: "done"}, nil
	}), b, EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	first := approveExec(t, s, e, id)
	if err = e.Run(ctx, id); !errors.Is(err, ErrWaitingApproval) {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != "x" {
		t.Fatal(string(raw), err)
	}
	cp, _ := e.Checkpoint(ctx, id)
	second := cp.Pending[0]
	if first.ID == second.ID || first.Digest == second.Digest {
		t.Fatal("reused approval")
	}
	if err = s.DecideApproval(ctx, second.ID, second.Digest, "approved"); err != nil {
		t.Fatal(err)
	}
	if err = e.Run(ctx, id); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(marker)
	if err != nil || string(raw) != "xx" {
		t.Fatal(string(raw), err)
	}
}

func TestExecCwdNonDirectoryNeverBlocksPreparation(t *testing.T) {
	_, _, w, _ := execFixture(t, ModeDefault)
	fifo := filepath.Join(w.Policy.Root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := prepareExec(w, ExecArguments{Scope: w.ScopeID, Program: "/bin/echo", Cwd: fifo, TimeoutMillis: 1000})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted FIFO cwd")
		}
	case <-time.After(100 * time.Millisecond):
		// Unblock the old implementation before failing, so the regression leaks no goroutine.
		writer, err := os.OpenFile(fifo, os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		writer.Close()
		<-done
		t.Fatal("cwd inspection blocked on FIFO")
	}
}

// Cmd.Wait prioritizes ExitError over its internal pipe-drain error. Neither
// nonzero nor signal exit may therefore be used as proof output reached EOF.
func TestExecAbnormalExitWithRetainedPipesRequiresReconciliation(t *testing.T) {
	for _, variant := range []struct {
		name, finish string
		code         int
		signal       string
	}{
		{"exit7", "exit 7", 7, ""},
		{"signal", "kill -TERM $$", -1, "terminated"},
	} {
		t.Run(variant.name, func(t *testing.T) {
			s, b, w, id := execFixture(t, ModeDefault)
			ctx := context.Background()
			count := filepath.Join(w.Policy.Root, "count")
			late := filepath.Join(w.Policy.Root, "late")
			// All descendant work is local and finite. It keeps both inherited pipes
			// open longer than the drain cap but finishes well before command timeout.
			script := `printf once >> "$1"; (/bin/sleep .4; printf late > "$2") & printf 'early 你好'; printf 'stderr fact' >&2; ` + variant.finish
			t.Cleanup(func() {
				until := time.Now().Add(2 * time.Second)
				for time.Now().Before(until) {
					if data, err := os.ReadFile(late); err == nil && string(data) == "late" {
						return
					}
					time.Sleep(time.Millisecond)
				}
				t.Error("finite pipe-retaining descendant failed to finish")
			})
			call := execCall(w, "/bin/sh", []string{"-c", script, "fixture", count, late}, nil, 2000)
			turns := 0
			e, err := NewEngine(s, streamFunc(func(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
				turns++
				if turns == 1 {
					return model.TurnResult{ToolCalls: []model.ToolCall{call}}, nil
				}
				return model.TurnResult{Content: "must not continue"}, nil
			}), b, EngineOptions{})
			if err != nil {
				t.Fatal(err)
			}
			p := approveExec(t, s, e, id)
			start := time.Now()
			err = e.Run(ctx, id)
			if !errors.Is(err, ErrReconciliation) {
				t.Fatalf("abnormal exit hid incomplete pipes: err=%v, model turns=%d", err, turns)
			}
			if time.Since(start) > time.Second {
				t.Fatal("pipe drain was not bounded")
			}
			if turns != 1 {
				t.Fatalf("model continued after unknown outcome: %d turns", turns)
			}
			events, err := s.Events(ctx, "s", 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Type == "tool.completed" {
					t.Fatal("unknown result published as completed")
				}
				if event.Type != "tool.uncertain" {
					continue
				}
				found = true
				var observation ToolObservation
				if err = json.Unmarshal(event.Payload, &observation); err != nil {
					t.Fatal(err)
				}
				var result map[string]any
				if err = json.Unmarshal([]byte(observation.Content), &result); err != nil {
					t.Fatal(err)
				}
				if result["exit_code"] != float64(variant.code) || result["stdout"] != "early 你好" || result["stderr"] != "stderr fact" || result["direct_child_waited"] != true || result["descendants_stopped"] != false || result["outcome"] != "unknown_requires_reconciliation" || result["output_complete"] != false {
					t.Fatal(result)
				}
				if variant.signal != "" && result["exit_signal"] != variant.signal {
					t.Fatal(result)
				}
			}
			if !found {
				t.Fatal("uncertain direct-child/output facts missing")
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err = e.Run(ctx, id); !errors.Is(err, ErrReconciliation) {
					t.Fatal("unknown run resumed", err)
				}
				if err = e.Recover(ctx, id); !errors.Is(err, ErrReconciliation) {
					t.Fatal("unknown effect recovered as known", err)
				}
			}
			if turns != 1 {
				t.Fatalf("model continued after recovery: %d turns", turns)
			}
			data, err := os.ReadFile(count)
			if err != nil || string(data) != "once" {
				t.Fatal("effect repeated", string(data), err)
			}
			var status string
			if err = s.db.QueryRow("SELECT status FROM side_effect_records WHERE idempotency_key=?", effectKey(id, p.ID)).Scan(&status); err != nil || status == "completed" {
				t.Fatal("unknown journal marked completed", status, err)
			}
		})
	}
}
