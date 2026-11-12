// This launcher only derives tokens from itself. Failure is blocked, never a
// request to change UAC, ACLs, accounts, privileges or integrity labels.
package main

import (
	"agent/scripts/windows-non-elevated-launcher/tokencheck"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var restricted = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

func qualify(t windows.Token) error {
	s, e := tokencheck.Inspect(t)
	if e != nil {
		return e
	}
	return tokencheck.Validate(s)
}
func selectToken() (windows.Token, string, error) {
	current, e := tokencheck.Current()
	if e != nil {
		return 0, "", e
	}
	if e = qualify(current); e == nil {
		return current, "current-qualified", nil
	}
	linked, le := current.GetLinkedToken()
	if le == nil {
		s, se := tokencheck.Inspect(linked)
		if se == nil && s.ElevationType == 3 && tokencheck.Validate(s) == nil {
			if ce := current.Close(); ce != nil {
				_ = linked.Close()
				return 0, "", ce
			}
			return linked, "linked-limited", nil
		}
		if ce := linked.Close(); ce != nil {
			_ = current.Close()
			return 0, "", ce
		}
	}
	admin, e := windows.StringToSid("S-1-5-32-544")
	if e != nil {
		_ = current.Close()
		return 0, "", e
	}
	disable := windows.SIDAndAttributes{Sid: admin}
	var out windows.Token
	r, _, callerr := restricted.Call(uintptr(current), 0x1|0x4, 1, uintptr(unsafe.Pointer(&disable)), 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)))
	ce := current.Close()
	if r == 0 {
		if out != 0 {
			ce = errors.Join(ce, out.Close())
		}
		return 0, "", errors.Join(callerr, ce)
	}
	if ce != nil {
		_ = out.Close()
		return 0, "", ce
	}
	if e = qualify(out); e != nil {
		return 0, "", errors.Join(e, out.Close())
	}
	return out, "restricted-filtered", nil
}

// Deliberately not *os.File: os/exec creates dedicated child stdout/stderr pipes.
type writer struct{ w io.Writer }

func (w writer) Write(b []byte) (int, error) { return w.w.Write(b) }
func run() error {
	exe := flag.String("exe", "", "guarded test executable already staged in user TEMP")
	flag.Parse()
	if *exe == "" || len(flag.Args()) == 0 {
		return fmt.Errorf("exe and explicit test arguments required")
	}
	abs, e := filepath.Abs(*exe)
	if e != nil {
		return e
	}
	temp, e := filepath.Abs(os.TempDir())
	if e != nil {
		return e
	}
	rel, e := filepath.Rel(temp, abs)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("test executable must be staged under TEMP")
	}
	t, source, e := selectToken()
	if e != nil {
		return fmt.Errorf("TOKEN_LAUNCH_BLOCKED: %w", e)
	}
	fmt.Fprintf(os.Stderr, "TOKEN_CANDIDATE qualified filtered non-elevated source=%s\n", source)
	cmd := exec.Command(abs, flag.Args()...)
	cmd.Dir = filepath.Dir(abs)
	// No ambient CI secrets, Go configuration, arbitrary PATH or extra handles.
	for _, key := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "USERPROFILE"} {
		if v := os.Getenv(key); v != "" {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "PERSONAL_AGENT_FILTERED_CHILD=1")
	cmd.Stdin = strings.NewReader("")
	cmd.Stdout = writer{os.Stdout}
	cmd.Stderr = writer{os.Stderr}
	// Go's PROC_THREAD_ATTRIBUTE_HANDLE_LIST includes only these dedicated stdio
	// handles. No AdditionalInheritedHandles or alternate ParentProcess is set.
	cmd.SysProcAttr = &syscall.SysProcAttr{Token: syscall.Token(t)}
	e = cmd.Run()
	return errors.Join(e, t.Close())
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
