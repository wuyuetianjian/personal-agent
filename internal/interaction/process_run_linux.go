//go:build linux

package interaction

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// The direct child is always synchronously reaped. Our separate pipe-drain cap
// does NOT bound kernel waitpid: an uninterruptible child can delay this return.
// No background waiter is abandoned and no whole-tree termination is claimed.
type execOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (b *execOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	left := MaxExecOutputBytes - len(b.data)
	if len(p) > left {
		p = p[:left]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return n, nil
}

type ExecObservation struct {
	OutputComplete     bool   `json:"output_complete"`
	StdoutBase64       string `json:"stdout_base64,omitempty"`
	StderrBase64       string `json:"stderr_base64,omitempty"`
	ExitSignal         string `json:"exit_signal,omitempty"`
	Stdout             string `json:"stdout"`
	Stderr             string `json:"stderr"`
	StdoutTruncated    bool   `json:"stdout_truncated"`
	StderrTruncated    bool   `json:"stderr_truncated"`
	ExitCode           int    `json:"exit_code"`
	DirectChildWaited  bool   `json:"direct_child_waited"`
	DescendantsStopped bool   `json:"descendants_stopped"` // Always false: never attested.
	Outcome            string `json:"outcome"`
	TerminationScope   string `json:"termination_scope"`
}

func executePreparedExec(ctx context.Context, w WorkspaceBinding, p preparedTool) (ToolResult, error) {
	var args ExecArguments
	if err := decodeTool(p.Proposal.Call, &args); err != nil {
		return refused(err)
	}
	fresh, err := prepareExec(w, args)
	if err != nil {
		return refused(err)
	}
	raw, _ := json.Marshal(fresh)
	old, _ := json.Marshal(p.ExecReview)
	if string(raw) != string(old) {
		return refused(fmt.Errorf("prepared executable/cwd/config changed"))
	}
	rootID, err := workspaceIdentity(w.Policy.Root)
	if err != nil {
		return refused(err)
	}
	if rootID != w.RootIdentity {
		return refused(fmt.Errorf("workspace root identity changed"))
	}
	// Pin exact executable and cwd for spawn. Recheck the opened handles, not a
	// second pathname lookup. This is not isolation from an OS-user peer writer.
	program, identity, hash, err := openExecFile(fresh.Program)
	if err != nil {
		return refused(err)
	}
	defer program.Close()
	if identity != fresh.ExecutableIdentity || hash != fresh.ExecutableSHA256 {
		return refused(ErrConflict)
	}
	dir, err := openExecDir(fresh.Cwd)
	if err != nil {
		return refused(err)
	}
	defer dir.Close()
	dirID, err := rootIdentity(int(dir.Fd()))
	if err != nil {
		return refused(err)
	}
	if dirID != fresh.CwdIdentity {
		return refused(ErrConflict)
	}
	if err = ctx.Err(); err != nil {
		return refused(err)
	}
	childCtx, cancel := context.WithTimeout(ctx, time.Duration(fresh.TimeoutMillis)*time.Millisecond)
	defer cancel()
	command := exec.CommandContext(childCtx, "/proc/self/fd/3", fresh.Argv...)
	command.Args[0] = fresh.Program
	command.ExtraFiles = []*os.File{program}
	// Go chdir happens before ExtraFiles mapping. Parent's pinned descriptor is
	// used for chdir; its lifetime includes synchronous Start and Wait.
	command.Dir = fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), dir.Fd())
	command.Env = make([]string, 0, len(fresh.Environment))
	for _, v := range fresh.Environment {
		command.Env = append(command.Env, v.Name+"="+v.Value)
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// CommandContext's default Process.Kill uses Go's PID-safe process handle.
	// Deliberately do not kill a numeric process group after concurrent reaping.
	command.WaitDelay = 150 * time.Millisecond
	// Own the pipes: Cmd.Wait prioritizes ExitError over its copy/WaitDelay
	// error. A nonzero or signalled child must not hide a forced output cutoff.
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		return refused(err)
	}
	defer stdoutRead.Close()
	defer stdoutWrite.Close()
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		return refused(err)
	}
	defer stderrRead.Close()
	defer stderrWrite.Close()
	command.Stdout = stdoutWrite
	command.Stderr = stderrWrite
	if err = command.Start(); err != nil {
		return refused(fmt.Errorf("process did not start: %w", err))
	}
	// Only the child/descendants retain write ends after Start. Copy pumps are
	// service-owned and always joined, including after reader closure.
	stdoutWrite.Close()
	stderrWrite.Close()
	out, stderr := &execOutput{}, &execOutput{}
	drains := make(chan error, 2)
	copyOutput := func(dst *execOutput, src *os.File) {
		_, copyErr := io.Copy(dst, src)
		drains <- copyErr
	}
	go copyOutput(out, stdoutRead)
	go copyOutput(stderr, stderrRead)
	err = command.Wait()
	outputComplete := true
	pending := 2
	timer := time.NewTimer(150 * time.Millisecond)
	for pending > 0 {
		select {
		case copyErr := <-drains:
			pending--
			if copyErr != nil {
				outputComplete = false
			}
		case <-timer.C:
			outputComplete = false
			stdoutRead.Close()
			stderrRead.Close()
			// Closing these pollable os.Pipe readers interrupts blocked reads.
			// No copier can outlive this operation or race output publication.
			for pending > 0 {
				<-drains
				pending--
			}
		}
	}
	timer.Stop()
	result := ExecObservation{OutputComplete: outputComplete, Stdout: string(out.data), Stderr: string(stderr.data), StdoutTruncated: out.truncated, StderrTruncated: stderr.truncated, ExitCode: -1, DirectChildWaited: command.ProcessState != nil, Outcome: "direct_child_exited", TerminationScope: fresh.TerminationScope}
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
		if status, ok := command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.ExitSignal = status.Signal().String()
		}
	}
	// JSON strings are UTF-8 display previews. Preserve exact retained bytes for
	// non-UTF-8 output in base64 rather than silently replacing binary data.
	if !utf8.Valid(out.data) {
		result.StdoutBase64 = base64.StdEncoding.EncodeToString(out.data)
	}
	if !utf8.Valid(stderr.data) {
		result.StderrBase64 = base64.StdEncoding.EncodeToString(stderr.data)
	}
	var exit *exec.ExitError
	ambiguous := !outputComplete || childCtx.Err() != nil || errors.Is(err, exec.ErrWaitDelay) || err != nil && !errors.As(err, &exit) || !result.DirectChildWaited
	if ambiguous {
		result.Outcome = "unknown_requires_reconciliation"
	}
	raw, _ = json.Marshal(result)
	observed := ToolResult{Content: string(raw), IsError: ambiguous || result.ExitCode != 0}
	if ambiguous {
		return observed, fmt.Errorf("NON-SANDBOXED exec outcome unknown; direct-child facts recorded, descendants not attested stopped; no automatic retry")
	}
	return observed, nil
}
