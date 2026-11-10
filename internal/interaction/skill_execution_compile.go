package interaction

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

const SkillRunProfile = "readonly-workspace-v1"
const skillRunAction = "skill_readonly_v1"

type SkillRunBudgets struct {
	MaxAttempts       int   `json:"max_attempts"`
	DurationMS        int64 `json:"duration_ms"`
	MaxOrdinaryEvents int   `json:"max_ordinary_events"`
	MaxControlEvents  int   `json:"max_control_events"`
	MaxInputBytes     int   `json:"max_input_bytes"`
	MaxResultBytes    int   `json:"max_result_bytes"`
	MaxOutputBytes    int   `json:"max_output_bytes"`
}
type SkillRunNode struct {
	NodeID     string   `json:"node_id"`
	Capability string   `json:"capability"`
	DependsOn  []string `json:"depends_on"`
	Call       ToolCall `json:"call"`
}
type SkillRunReview struct {
	ExecutionProfile   string          `json:"execution_profile"`
	Eligibility        string          `json:"eligibility"`
	Reason             string          `json:"reason"`
	InputsHash         string          `json:"inputs_sha256"`
	PlanHash           string          `json:"plan_sha256"`
	Nodes              []SkillRunNode  `json:"nodes"`
	Budgets            SkillRunBudgets `json:"budgets"`
	Evaluation         string          `json:"evaluation"`
	VerificationStatus string          `json:"verification_status"`
}

// compileSkillRun is pure: descriptions are inert and paths are never opened.
func compileSkillRun(manifest, profile string, inputs json.RawMessage, scope string) (SkillRunReview, json.RawMessage, error) {
	out := SkillRunReview{ExecutionProfile: profile, Eligibility: "blocked", Nodes: []SkillRunNode{}, Evaluation: "not_run", VerificationStatus: "not_evaluated",
		Budgets: SkillRunBudgets{MaxAttempts: 16, DurationMS: 120000, MaxOrdinaryEvents: 512, MaxControlEvents: 64, MaxInputBytes: 32768, MaxResultBytes: 65536, MaxOutputBytes: 1048576}}
	block := func(reason string) (SkillRunReview, json.RawMessage, error) {
		out.Reason = reason
		return out, nil, nil
	}
	if profile != SkillRunProfile {
		return block("unsupported_profile")
	}
	review, err := ValidateSkillManifest(manifest)
	if err != nil {
		return block("invalid_manifest")
	}
	m := review.Manifest
	if m.Permissions.MaxLevel != "read_only" {
		return block("unsupported_permission")
	}
	if m.Verification.MinCoverage != 0 {
		return block("unsupported_verification_requirement")
	}
	out.Budgets.MaxAttempts = min(16, m.Budget.MaxIterations, m.Budget.MaxToolCalls)
	if len(m.Workflow.Nodes) > out.Budgets.MaxAttempts {
		return block("insufficient_attempt_budget")
	}
	if len(m.Inputs) != len(m.Workflow.Nodes) {
		return block("unsupported_input_binding")
	}
	for _, node := range m.Workflow.Nodes {
		if node.Role != "" || !skillEnum(node.Capability, "workspace.read", "workspace.search") {
			return block("unsupported_capability")
		}
		if m.Inputs[node.ID].Type != "object" {
			return block("unsupported_input_binding")
		}
	}
	if len(inputs) > out.Budgets.MaxInputBytes {
		return block("input_budget_exceeded")
	}
	obj, err := skillRunJSONObject(inputs)
	if err != nil || len(obj) != len(m.Inputs) {
		return block("invalid_arguments")
	}
	canonical, err := json.Marshal(obj)
	if err != nil {
		return out, nil, ErrInvalid
	}
	if len(canonical) > out.Budgets.MaxInputBytes {
		return block("input_budget_exceeded")
	}
	calls := map[string]ToolCall{}
	for _, node := range m.Workflow.Nodes {
		a, ok := obj[node.ID].(map[string]any)
		if !ok {
			return block("invalid_arguments")
		}
		args := map[string]any{"scope": scope}
		name := "read"
		if node.Capability == "workspace.read" {
			path, ok := a["path"].(string)
			if len(a) != 1 || !ok || safeWorkspacePath(path) != nil {
				return block("invalid_arguments")
			}
			args["path"] = path
		} else {
			name = "search"
			query, ok := a["query"].(string)
			paths, pathsOK := a["paths"].([]any)
			if len(a) != 2 || !ok || !pathsOK || query == "" || len(query) > 1024 || strings.ContainsRune(query, 0) || len(paths) == 0 || len(paths) > MaxSearchFiles {
				return block("invalid_arguments")
			}
			for _, p := range paths {
				path, ok := p.(string)
				if !ok || safeWorkspacePath(path) != nil {
					return block("invalid_arguments")
				}
			}
			args["query"], args["paths"] = query, paths
		}
		raw, _ := json.Marshal(args)
		calls[node.ID] = ToolCall{ID: node.ID, Name: name, Arguments: string(raw)}
	}
	// Restart the original-order scan after each selection: ties are manifest order,
	// not map iteration order or depth-first dependency traversal order.
	done := map[string]bool{}
	for len(out.Nodes) < len(m.Workflow.Nodes) {
		selected := false
		for _, node := range m.Workflow.Nodes {
			if done[node.ID] {
				continue
			}
			ready := true
			for _, dep := range node.DependsOn {
				if !done[dep] {
					ready = false
				}
			}
			if !ready {
				continue
			}
			deps := append([]string{}, node.DependsOn...)
			out.Nodes = append(out.Nodes, SkillRunNode{NodeID: node.ID, Capability: node.Capability, DependsOn: deps, Call: calls[node.ID]})
			done[node.ID] = true
			selected = true
			break
		}
		if !selected {
			return block("invalid_manifest")
		}
	}
	out.Eligibility, out.Reason = "ready", "ready"
	out.InputsHash = skillBytesHash(string(canonical))
	out.PlanHash = operatorHash(out.Nodes)
	return out, canonical, nil
}

// Reuse the Engine's duplicate-key rejecting token decoder. Bound raw size and
// nesting first, because the generic decoder is recursive. Reject nulls and
// replacement-decoded invalid UTF-8 rather than silently canonicalizing them.
func skillRunJSONObject(raw []byte) (map[string]any, error) { return skillJSONObjectLimit(raw, 32768) }
func skillJSONObjectLimit(raw []byte, limit int) (map[string]any, error) {
	if len(raw) > limit || !utf8.Valid(raw) || !json.Valid(raw) || !skillJSONUnicode(raw) {
		return nil, ErrInvalid
	}
	depth, inString, escape := 0, false, false
	for _, c := range raw {
		if inString {
			if escape {
				escape = false
			} else if c == '\\' {
				escape = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
			if depth > 8 {
				return nil, ErrInvalid
			}
		case '}', ']':
			depth--
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, err := decodeArguments(d)
	if err != nil {
		return nil, ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, ErrInvalid
	}
	return obj, nil
}
