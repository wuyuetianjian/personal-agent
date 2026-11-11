package interaction

import (
	"encoding/json"
	"sort"
)

const HookEvaluator = "declarative-hooks-v1"
const hookPolicyLimit = 8192

type HookPolicy struct {
	SchemaVersion int        `json:"schema_version"`
	Evaluator     string     `json:"evaluator"`
	Rules         []HookRule `json:"rules"`
}
type HookRule struct {
	ID           string   `json:"id"`
	Event        string   `json:"event"`
	NodeIDs      []string `json:"node_ids"`
	Capabilities []string `json:"capabilities"`
	Statuses     []string `json:"statuses"`
	Effect       string   `json:"effect"`
}
type HookMetadata struct{ Event, NodeID, Capability, Status string }
type HookMatch struct {
	RuleID string `json:"rule_id"`
	Effect string `json:"effect"`
}
type HookMatchResult struct {
	Decision string      `json:"decision"`
	Matches  []HookMatch `json:"matches"`
	Reason   string      `json:"reason"`
}

func hookID(s string) bool { return len(s) <= 64 && operatorRequestID(s) }
func hookStatuses(event string) []string {
	switch event {
	case "before_node":
		return []string{"ready"}
	case "after_node":
		return []string{"success", "failed"}
	case "run_terminal":
		return []string{"completed", "failed", "cancelled", "revoked"}
	}
	return nil
}
func hookSelectors(values []string, max int, valid func(string) bool) bool {
	if values == nil || len(values) > max {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] || !valid(v) {
			return false
		}
		seen[v] = true
	}
	return true
}

// validateHookPolicy never canonicalizes/mutates its caller's slices. The only
// zero-rule policy is the immutable internal disabled snapshot.
func validateHookPolicy(p HookPolicy, disabled bool) error {
	if p.SchemaVersion != 1 || p.Evaluator != HookEvaluator || p.Rules == nil || len(p.Rules) > 8 || (!disabled && len(p.Rules) == 0) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, r := range p.Rules {
		statuses := hookStatuses(r.Event)
		if !hookID(r.ID) || seen[r.ID] || statuses == nil || !skillEnum(r.Effect, "audit", "deny") || (r.Effect == "deny" && r.Event != "before_node") || !hookSelectors(r.NodeIDs, 16, hookID) || !hookSelectors(r.Capabilities, 2, func(v string) bool { return skillEnum(v, "workspace.read", "workspace.search") }) || !hookSelectors(r.Statuses, len(statuses), func(v string) bool { return skillEnum(v, statuses...) }) {
			return ErrInvalid
		}
		if r.Event == "run_terminal" && (len(r.NodeIDs) != 0 || len(r.Capabilities) != 0) {
			return ErrInvalid
		}
		seen[r.ID] = true
	}
	raw, e := json.Marshal(p)
	if e != nil || len(raw) > hookPolicyLimit {
		return ErrInvalid
	}
	return nil
}
func hookJSONDepth(raw []byte, max int) bool {
	depth, inString, escaped := 0, false, false
	for _, c := range raw {
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
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
			if depth > max {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}
func ParseHookPolicy(raw []byte) (out HookPolicy, canonical []byte, err error) {
	obj, e := skillJSONObjectLimit(raw, hookPolicyLimit)
	if e != nil || !hookJSONDepth(raw, 5) || len(obj) != 3 || obj["schema_version"] != json.Number("1") || obj["evaluator"] != HookEvaluator {
		return out, nil, ErrInvalid
	}
	rules, ok := obj["rules"].([]any)
	if !ok || len(rules) < 1 || len(rules) > 8 {
		return out, nil, ErrInvalid
	}
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if !ok || len(m) != 6 {
			return out, nil, ErrInvalid
		}
		for _, k := range []string{"id", "event", "effect"} {
			if _, ok := m[k].(string); !ok {
				return out, nil, ErrInvalid
			}
		}
		for _, k := range []string{"node_ids", "capabilities", "statuses"} {
			if _, ok := m[k].([]any); !ok {
				return out, nil, ErrInvalid
			}
		}
	}
	if json.Unmarshal(raw, &out) != nil || validateHookPolicy(out, false) != nil {
		return HookPolicy{}, nil, ErrInvalid
	}
	sort.Slice(out.Rules, func(i, j int) bool { return out.Rules[i].ID < out.Rules[j].ID })
	for i := range out.Rules {
		sort.Strings(out.Rules[i].NodeIDs)
		sort.Strings(out.Rules[i].Capabilities)
		sort.Strings(out.Rules[i].Statuses)
	}
	canonical, err = json.Marshal(out)
	if len(canonical) > hookPolicyLimit {
		return HookPolicy{}, nil, ErrInvalid
	}
	return
}
func EvaluateHookPolicy(policy HookPolicy, metadata HookMetadata) (out HookMatchResult, err error) {
	if validateHookPolicy(policy, true) != nil || !skillEnum(metadata.Status, hookStatuses(metadata.Event)...) {
		return out, ErrInvalid
	}
	if metadata.Event == "run_terminal" {
		if metadata.NodeID != "" || metadata.Capability != "" {
			return out, ErrInvalid
		}
	} else if !hookID(metadata.NodeID) || !skillEnum(metadata.Capability, "workspace.read", "workspace.search") {
		return out, ErrInvalid
	}
	out = HookMatchResult{Decision: "audit", Matches: []HookMatch{}, Reason: "no_match"}
	if metadata.Event == "before_node" {
		out.Decision = "no_restriction"
	}
	matches := func(values []string, value string) bool { return len(values) == 0 || skillEnum(value, values...) }
	for _, r := range policy.Rules {
		if r.Event == metadata.Event && matches(r.NodeIDs, metadata.NodeID) && matches(r.Capabilities, metadata.Capability) && matches(r.Statuses, metadata.Status) {
			out.Matches = append(out.Matches, HookMatch{r.ID, r.Effect})
			out.Reason = "matched"
			if r.Effect == "deny" {
				out.Decision = "deny"
			}
		}
	}
	sort.Slice(out.Matches, func(i, j int) bool { return out.Matches[i].RuleID < out.Matches[j].RuleID })
	return out, nil
}
