package interaction

import (
	"agent/internal/model"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

const NonSandboxExecWarning = "NON-SANDBOXED: runs with current OS-user access, including files, network and credentials available to that user. Cwd is not confinement; a minimal environment is not secret isolation. Cancellation may leave descendants running; direct-child exit does not prove all descendants stopped."
const ExecEnvironmentPolicy = "replace environment; no host variables inherited; fixed PATH=/usr/bin:/bin and LANG=C.UTF-8, plus exact explicit changes"
const MaxExecOutputBytes = 4 << 10 // per stream
const MaxExecTimeoutMillis = 60000

type EnvironmentChange struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type ExecReview struct {
	EnvironmentChanges []EnvironmentChange `json:"environment_changes"`
	LaunchMechanism    string              `json:"launch_mechanism"`
	Warning            string              `json:"warning"`
	Scope              string              `json:"scope"`
	RootIdentity       string              `json:"root_identity"`
	Program            string              `json:"program"`
	ExecutableIdentity string              `json:"executable_identity"`
	ExecutableSHA256   string              `json:"executable_sha256"`
	Argv               []string            `json:"argv"`
	Cwd                string              `json:"cwd"`
	CwdIdentity        string              `json:"cwd_identity"`
	EnvironmentPolicy  string              `json:"environment_policy"`
	Environment        []EnvironmentChange `json:"environment"`
	TimeoutMillis      int                 `json:"timeout_ms"`
	OutputLimitBytes   int                 `json:"output_limit_bytes"`
	TerminationScope   string              `json:"termination_scope"`
}

func commandCapability() Capability {
	return Capability{Reason: "NON-SANDBOXED exec disabled by default; explicit immutable host and session opt-in required"}
}
func (b *WorkspaceBroker) execCapability() Capability {
	if !b.nonSandboxExec {
		return commandCapability()
	}
	return platformExecCapability()
}
func execDigest(callDigest string, review *ExecReview) string {
	raw, _ := json.Marshal(review)
	return contentHash(append([]byte(callDigest+"\x00"), raw...))
}
func prepareExec(w WorkspaceBinding, a ExecArguments) (*ExecReview, error) {
	if a.Scope != w.ScopeID || !filepath.IsAbs(a.Program) || !filepath.IsAbs(a.Cwd) || a.TimeoutMillis < 1 || a.TimeoutMillis > MaxExecTimeoutMillis || len(a.Argv) > 256 {
		return nil, ErrInvalid
	}
	if strings.IndexByte(a.Program, 0) >= 0 || strings.IndexByte(a.Cwd, 0) >= 0 {
		return nil, ErrInvalid
	}
	size := 0
	for _, v := range a.Argv {
		size += len(v)
		if strings.IndexByte(v, 0) >= 0 {
			return nil, ErrInvalid
		}
	}
	env := map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"}
	for k, v := range a.Environment {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.IndexByte(v, 0) >= 0 {
			return nil, ErrInvalid
		}
		for _, c := range k {
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return nil, ErrInvalid
			}
		}
		size += len(k) + len(v)
		env[k] = v
	}
	if size > 16<<10 {
		return nil, ErrBudget
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	r := &ExecReview{LaunchMechanism: "Linux pinned executable at /proc/self/fd/3; argv[0] is the canonical program for binaries; shebang scripts see the pinned path as their script name. Interpreter and shared-library dependencies are not pinned.", Warning: NonSandboxExecWarning, Scope: w.ScopeID, RootIdentity: w.RootIdentity, Program: a.Program, Cwd: a.Cwd, Argv: append([]string{}, a.Argv...), EnvironmentPolicy: ExecEnvironmentPolicy, TimeoutMillis: a.TimeoutMillis, OutputLimitBytes: MaxExecOutputBytes, TerminationScope: "direct child synchronously waited; cancellation kills direct child only; descendants are not tracked or attested stopped"}
	r.EnvironmentChanges = []EnvironmentChange{}
	for _, k := range keys {
		r.Environment = append(r.Environment, EnvironmentChange{k, env[k]})
		if v, ok := a.Environment[k]; ok {
			r.EnvironmentChanges = append(r.EnvironmentChanges, EnvironmentChange{k, v})
		}
	}
	if err := inspectExec(r); err != nil {
		return nil, fmt.Errorf("exec preparation: %w", err)
	}
	return r, nil
}

func execToolDefinition() model.ToolDefinition {
	str := func() map[string]any { return map[string]any{"type": "string"} }
	return model.ToolDefinition{Type: "function", Function: model.ToolFunction{Name: "exec", Description: NonSandboxExecWarning + " Exact absolute executable and cwd, argv without shell expansion, explicit environment changes and timeout; fresh approval always required, including acceptEdits.", Parameters: map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"scope", "program", "argv", "cwd", "environment", "timeout_ms"}, "properties": map[string]any{
			"scope": str(), "program": str(), "cwd": str(), "argv": map[string]any{"type": "array", "items": str(), "maxItems": 256}, "environment": map[string]any{"type": "object", "additionalProperties": str()}, "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": MaxExecTimeoutMillis},
		}}}}
}
