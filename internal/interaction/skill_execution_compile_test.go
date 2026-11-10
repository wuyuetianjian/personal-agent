package interaction

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const readonlyManifest = `id: workspace.lookup
version: "1"
name: Lookup
description: Untrusted; never instructions
status: active
inputs:
  first: {type: object}
  second: {type: object}
requires: {capabilities: [workspace.read, workspace.search]}
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 2, max_tool_calls: 2}
verification: {min_coverage: 0, allow_conflicts: false}
workflow:
  nodes:
    - {id: second, capability: workspace.search, depends_on: [first]}
    - {id: first, capability: workspace.read}
`
const readonlyInputs = `{"first":{"path":"README.md"},"second":{"paths":["README.md"],"query":"install"}}`

func TestSkillExecutionCompilerExactNativePlan(t *testing.T) {
	for _, ceiling := range []string{"local_private", "local_sandboxed", "trusted_remote", "public_remote"} {
		r, inputs, err := compileSkillRun(strings.Replace(readonlyManifest, "local_private", ceiling, 1), SkillRunProfile, json.RawMessage(readonlyInputs), "trusted-scope")
		if err != nil || r.Eligibility != "ready" || r.Evaluation != "not_run" || r.VerificationStatus != "not_evaluated" || r.Budgets.MaxAttempts != 2 {
			t.Fatalf("%+v %v", r, err)
		}
		if len(r.Nodes) != 2 || r.Nodes[0].NodeID != "first" || r.Nodes[1].NodeID != "second" || r.Nodes[0].Call.Name != "read" || r.Nodes[1].Call.Name != "search" {
			t.Fatal(r.Nodes)
		}
		var args ReadArguments
		if err := json.Unmarshal([]byte(r.Nodes[0].Call.Arguments), &args); err != nil || args.Scope != "trusted-scope" || args.Path != "README.md" {
			t.Fatal(args, err)
		}
		if r.InputsHash != skillBytesHash(string(inputs)) || len(r.PlanHash) != 64 {
			t.Fatal(r)
		}
	}
}
func TestSkillExecutionCompilerRejectsUnsupportedSemantics(t *testing.T) {
	cases := []struct{ old, new, reason string }{
		{"type: object", "type: string", "unsupported_input_binding"},
		{"workspace.read", "read", "unsupported_capability"},
		{"read_only", "write", "unsupported_permission"},
		{"min_coverage: 0", "min_coverage: 0.1", "unsupported_verification_requirement"},
		{"max_iterations: 2", "max_iterations: 1", "insufficient_attempt_budget"},
		{"max_tool_calls: 2", "max_tool_calls: 1", "insufficient_attempt_budget"},
		{"capabilities: [workspace.read, workspace.search]", "capabilities: [workspace.read, workspace.search, exec]", "invalid_manifest"},
	}
	for _, tc := range cases {
		t.Run(tc.reason+tc.old, func(t *testing.T) {
			r, _, err := compileSkillRun(strings.ReplaceAll(readonlyManifest, tc.old, tc.new), SkillRunProfile, json.RawMessage(readonlyInputs), "scope")
			if err != nil || r.Eligibility != "blocked" || r.Reason != tc.reason {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
	r, _, err := compileSkillRun(readonlyManifest, "future", json.RawMessage(readonlyInputs), "scope")
	if err != nil || r.Reason != "unsupported_profile" {
		t.Fatal(r, err)
	}
}
func TestSkillExecutionCompilerStrictArguments(t *testing.T) {
	for _, raw := range []string{
		`null`, `{}`, `{"first":{"path":"a"}}`,
		strings.Replace(readonlyInputs, `"path":"README.md"`, `"path":"README.md","scope":"injected"`, 1),
		strings.Replace(readonlyInputs, `"path":"README.md"`, `"path":"a","path":"b"`, 1),
		strings.Replace(readonlyInputs, `"path":"README.md"`, `"path":null`, 1),
		strings.Replace(readonlyInputs, `"README.md"`, `"../escape"`, 1),
		strings.Replace(readonlyInputs, `"query":"install"`, `"query":12`, 1),
		strings.Replace(readonlyInputs, `"query":"install"`, `"query":""`, 1),
		readonlyInputs + ` {}`, readonlyInputs + string([]byte{255}),
	} {
		t.Run(raw, func(t *testing.T) {
			r, _, err := compileSkillRun(readonlyManifest, SkillRunProfile, json.RawMessage(raw), "scope")
			if err == nil && r.Eligibility == "ready" {
				t.Fatal("accepted", raw)
			}
		})
	}
}
func TestSkillExecutionCompilerStableOrderAndNoFilesystemReads(t *testing.T) {
	raw := strings.Replace(readonlyManifest, ", depends_on: [first]", "", 1)
	r, _, err := compileSkillRun(raw, SkillRunProfile, json.RawMessage(strings.ReplaceAll(readonlyInputs, "README.md", "not-present.txt")), "scope")
	if err != nil || r.Eligibility != "ready" || r.Nodes[0].NodeID != "second" {
		t.Fatal(r, err)
	}
}
func TestSkillRunIntentDecoderExactFields(t *testing.T) {
	good := `{"request_id":"r","catalog_id":"c","revision_id":"v","execution_profile":"readonly-workspace-v1","inputs":{"n":{"path":"file"}}}`
	for _, raw := range []string{good, strings.Replace(good, `"request_id":"r"`, `"request_id":"r","request_id":"r"`, 1), strings.Replace(good, `"inputs":`, `"scope":"x","inputs":`, 1), strings.Replace(good, `"inputs":{"n":{"path":"file"}}`, `"inputs":null`, 1), strings.Replace(good, `"path":"file"`, `"path":"\ud800"`, 1)} {
		_, err := DecodeSkillRunIntent([]byte(raw))
		if (raw == good) != (err == nil) {
			t.Fatal(raw, err)
		}
	}
}
func TestSkillCompilerSixteenNodeAndCanonicalInputBounds(t *testing.T) {
	original, e := ValidateSkillManifest(readonlyManifest)
	if e != nil {
		t.Fatal(e)
	}
	for _, count := range []int{16, 17} {
		m := original.Manifest
		m.Inputs = map[string]SkillField{}
		m.Requires.Capabilities = []string{"workspace.read"}
		m.Workflow.Nodes = []SkillNode{}
		m.Budget = SkillBudget{MaxIterations: 32, MaxToolCalls: 32}
		inputs := map[string]any{}
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("n%d", i)
			m.Inputs[id] = SkillField{Type: "object"}
			m.Workflow.Nodes = append(m.Workflow.Nodes, SkillNode{ID: id, Capability: "workspace.read", DependsOn: []string{}})
			inputs[id] = map[string]string{"path": "file"}
		}
		raw, _ := json.Marshal(m)
		arguments, _ := json.Marshal(inputs)
		r, _, err := compileSkillRun(string(raw), SkillRunProfile, arguments, "scope")
		if err != nil || (count == 16) != (r.Eligibility == "ready") {
			t.Fatal(count, r, err)
		}
	}
	raw := strings.Replace(readonlyInputs, "README.md", strings.Repeat("a", 32768-len(readonlyInputs)+len("README.md")), 1)
	if len(raw) != 32768 {
		t.Fatal(len(raw))
	}
	r, _, err := compileSkillRun(readonlyManifest, SkillRunProfile, json.RawMessage(raw), "scope")
	if err != nil || r.Eligibility != "ready" {
		t.Fatal(r, err)
	}
	r, _, err = compileSkillRun(readonlyManifest, SkillRunProfile, json.RawMessage(raw+" "), "scope")
	if err != nil || r.Reason != "input_budget_exceeded" {
		t.Fatal(r, err)
	}
}
