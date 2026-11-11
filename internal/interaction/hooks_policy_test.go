package interaction

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const hookPolicyExample = `{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[{"id":"deny-search","event":"before_node","node_ids":[],"capabilities":["workspace.search"],"statuses":[],"effect":"deny"}]}`

func TestHookPolicyStrictSchema(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown":            strings.Replace(hookPolicyExample, `"rules":`, `"script":"x","rules":`, 1),
		"duplicate":          strings.Replace(hookPolicyExample, `"id":"deny-search"`, `"id":"a","id":"b"`, 1),
		"null":               strings.Replace(hookPolicyExample, `"node_ids":[]`, `"node_ids":null`, 1),
		"missing":            strings.Replace(hookPolicyExample, `"node_ids":[],`, ``, 1),
		"duplicate-selector": strings.Replace(hookPolicyExample, `["workspace.search"]`, `["workspace.search","workspace.search"]`, 1),
		"capability":         strings.Replace(hookPolicyExample, `workspace.search`, `shell.execute`, 1),
		"status":             strings.Replace(hookPolicyExample, `"statuses":[]`, `"statuses":["completed"]`, 1),
		"after-deny":         strings.Replace(hookPolicyExample, `before_node`, `after_node`, 1),
		"terminal-deny":      strings.Replace(hookPolicyExample, `before_node`, `run_terminal`, 1),
		"terminal-filter":    strings.ReplaceAll(strings.Replace(hookPolicyExample, `before_node`, `run_terminal`, 1), `"deny"`, `"audit"`),
		"depth-six":          strings.Replace(hookPolicyExample, `"node_ids":[]`, `"node_ids":[[[[]]]]`, 1),
		"surrogate":          strings.Replace(hookPolicyExample, `deny-search`, `\ud800`, 1),
		"utf8":               strings.Replace(hookPolicyExample, `deny-search`, string([]byte{0xff}), 1),
		"trailing":           hookPolicyExample + `{}`,
		"8193":               hookPolicyExample + strings.Repeat(" ", 8193-len(hookPolicyExample)),
		"version-float":      strings.Replace(hookPolicyExample, `"schema_version":1`, `"schema_version":1.0`, 1),
		"empty":              `{"schema_version":1,"evaluator":"declarative-hooks-v1","rules":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, e := ParseHookPolicy([]byte(raw)); e == nil {
				t.Fatal("accepted", raw)
			}
		})
	}
	var p HookPolicy
	if e := json.Unmarshal([]byte(hookPolicyExample), &p); e != nil {
		t.Fatal(e)
	}
	p.Rules = append(p.Rules, p.Rules[0])
	b, _ := json.Marshal(p)
	if _, _, e := ParseHookPolicy(b); e == nil {
		t.Fatal("duplicate IDs")
	}
	p.Rules = nil
	for i := 0; i < 9; i++ {
		var q HookPolicy
		json.Unmarshal([]byte(hookPolicyExample), &q)
		q.Rules[0].ID = string(rune('a' + i))
		p.Rules = append(p.Rules, q.Rules[0])
	}
	b, _ = json.Marshal(p)
	if _, _, e := ParseHookPolicy(b); e == nil {
		t.Fatal("ninth rule")
	}
	for _, raw := range []string{hookPolicyExample + strings.Repeat(" ", 8192-len(hookPolicyExample)), strings.Replace(hookPolicyExample, `deny-search`, `deny\u002dsearch`, 1)} {
		if _, _, e := ParseHookPolicy([]byte(raw)); e != nil {
			t.Fatal("valid boundary/escape", e)
		}
	}
}
func TestHookPolicyCanonicalMatching(t *testing.T) {
	raw := `{"rules":[{"id":"z","event":"before_node","effect":"audit","statuses":["ready"],"capabilities":["workspace.search","workspace.read"],"node_ids":["second","first"]},{"id":"a","event":"before_node","effect":"deny","statuses":[],"capabilities":[],"node_ids":[]}],"evaluator":"declarative-hooks-v1","schema_version":1}`
	p, c, e := ParseHookPolicy([]byte(raw))
	if e != nil {
		t.Fatal(e)
	}
	p.Rules[0], p.Rules[1] = p.Rules[1], p.Rules[0]
	b, _ := json.Marshal(p)
	p, c2, e := ParseHookPolicy(b)
	if e != nil || string(c) != string(c2) {
		t.Fatal(string(c), string(c2), e)
	}
	got, e := EvaluateHookPolicy(p, HookMetadata{Event: "before_node", NodeID: "first", Capability: "workspace.read", Status: "ready"})
	if e != nil || got.Decision != "deny" || len(got.Matches) != 2 || got.Matches[0].RuleID != "a" || got.Matches[1].RuleID != "z" {
		t.Fatal(got, e)
	}
	got, e = EvaluateHookPolicy(p, HookMetadata{Event: "before_node", NodeID: "other", Capability: "workspace.read", Status: "ready"})
	if e != nil || len(got.Matches) != 1 {
		t.Fatal(got, e)
	}
	got, e = EvaluateHookPolicy(p, HookMetadata{Event: "after_node", NodeID: "first", Capability: "workspace.read", Status: "success"})
	if e != nil || got.Decision != "audit" || len(got.Matches) != 0 || got.Reason != "no_match" {
		t.Fatal(got, e)
	}
	p.Rules = p.Rules[1:]
	got, e = EvaluateHookPolicy(p, HookMetadata{Event: "before_node", NodeID: "other", Capability: "workspace.read", Status: "ready"})
	if e != nil || got.Decision != "no_restriction" {
		t.Fatal(got, e)
	}
	if _, e = EvaluateHookPolicy(p, HookMetadata{Event: "before_node", NodeID: "first", Capability: "shell", Status: "ready"}); e == nil {
		t.Fatal("invalid metadata")
	}
}

func TestHookPolicyExactCanonicalByteBoundary(t *testing.T) {
	p := HookPolicy{SchemaVersion: 1, Evaluator: HookEvaluator, Rules: []HookRule{}}
	for i := 0; i < 8; i++ {
		r := HookRule{ID: fmt.Sprintf("r%d", i), Event: "before_node", NodeIDs: []string{}, Capabilities: []string{}, Statuses: []string{}, Effect: "audit"}
		for j := 0; j < 16; j++ {
			r.NodeIDs = append(r.NodeIDs, fmt.Sprintf("n%02d", j)+strings.Repeat("a", 37))
		}
		p.Rules = append(p.Rules, r)
	}
	raw, _ := json.Marshal(p)
	for i := range p.Rules {
		for j := range p.Rules[i].NodeIDs {
			for len(raw) < 8192 && len(p.Rules[i].NodeIDs[j]) < 64 {
				p.Rules[i].NodeIDs[j] += "a"
				raw, _ = json.Marshal(p)
			}
		}
	}
	if len(raw) != 8192 {
		t.Fatal(len(raw))
	}
	_, canonical, e := ParseHookPolicy(raw)
	if e != nil || len(canonical) != 8192 {
		t.Fatal(len(canonical), e)
	}
	if _, _, e = ParseHookPolicy(append(raw, ' ')); e == nil {
		t.Fatal("8193 byte policy accepted")
	}
}
