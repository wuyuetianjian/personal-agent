package interaction

import (
	"fmt"
	"strings"
	"testing"
)

const catalogManifest = `id: local.lookup
version: "1.0.0"
name: Local lookup
description: Untrusted descriptive text
status: active
inputs:
  query: {type: string}
requires:
  capabilities: [memory.search]
permissions: {max_level: read_only}
privacy: {max_external_trust: local_private}
budget: {max_iterations: 3, max_tool_calls: 4}
verification: {min_coverage: 0.8, allow_conflicts: false}
workflow:
  nodes:
    - {id: search, capability: memory.search}
    - {id: explain, role: reasoning, depends_on: [search]}
`

func TestSkillManifestStrictProfile(t *testing.T) {
	r, e := ValidateSkillManifest(catalogManifest)
	if e != nil {
		t.Fatal(e)
	}
	if r.ParserVersion != SkillParserVersion || r.Evaluation != "not_run" || r.Execution != "unavailable" || r.ExecutionReason != "bridge_not_implemented" || r.Manifest.Status != "active" || len(r.Targets) != 2 || r.Targets[0].Support != "unsupported" || len(r.ArtifactHash) != 64 || len(r.ContentHash) != 64 {
		t.Fatalf("%+v", r)
	}
	r2, e := ValidateSkillManifest(catalogManifest + "\n# exact bytes matter\n")
	if e != nil || r.ArtifactHash == r2.ArtifactHash || r.ContentHash != r2.ContentHash {
		t.Fatal(r2, e)
	}
	cases := map[string]string{
		"oversize": strings.Repeat(" ", 65537), "unicode": catalogManifest + string([]byte{0xff}), "nul": catalogManifest + "\x00", "empty": "", "path": "/tmp/SKILL.md", "url": "https://example.invalid/skill.yaml",
		"duplicate": catalogManifest + "id: other\n", "unknown": catalogManifest + "script: exec\n", "second": catalogManifest + "---\nid: second\n", "empty_second": catalogManifest + "---\n", "alias": strings.Replace(catalogManifest, "[memory.search]", "[&cap memory.search, *cap]", 1), "anchor": strings.Replace(catalogManifest, "Local lookup", "&label Local lookup", 1), "tag": strings.Replace(catalogManifest, "Local lookup", "!!str Local lookup", 1),
		"unknown_nested": strings.Replace(catalogManifest, "type: string", "type: string, secret: true", 1), "duplicate_nested": strings.Replace(catalogManifest, "type: string", "type: string, type: number", 1), "numeric_string": strings.Replace(catalogManifest, "version: \"1.0.0\"", "version: 1", 1), "null": strings.Replace(catalogManifest, "description: Untrusted descriptive text", "description: null", 1), "bool_coercion": strings.Replace(catalogManifest, "allow_conflicts: false", "allow_conflicts: 0", 1),
		"status": strings.Replace(catalogManifest, "status: active", "status: approved", 1), "privacy": strings.Replace(catalogManifest, "local_private}", "secret}", 1), "permission": strings.Replace(catalogManifest, "read_only}", "admin}", 1), "negative_budget": strings.Replace(catalogManifest, "max_tool_calls: 4", "max_tool_calls: -1", 1), "large_budget": strings.Replace(catalogManifest, "max_iterations: 3", "max_iterations: 1001", 1), "nan": strings.Replace(catalogManifest, "min_coverage: 0.8", "min_coverage: .nan", 1),
		"ambiguous": strings.Replace(catalogManifest, "capability: memory.search}", "capability: memory.search, role: reasoning}", 1), "undeclared": strings.Replace(catalogManifest, "capabilities: [memory.search]", "capabilities: []", 1), "unused_requirement": strings.Replace(catalogManifest, "[memory.search]", "[memory.search, net.fetch]", 1), "duplicate_requirement": strings.Replace(catalogManifest, "[memory.search]", "[memory.search, memory.search]", 1),
		"missing_dependency": strings.Replace(catalogManifest, "depends_on: [search]", "depends_on: [absent]", 1), "cycle": strings.Replace(catalogManifest, "capability: memory.search}", "capability: memory.search, depends_on: [explain]}", 1), "selfcycle": strings.Replace(catalogManifest, "depends_on: [search]", "depends_on: [explain]", 1), "duplicate_node": strings.Replace(catalogManifest, "id: explain", "id: search", 1), "duplicate_dep": strings.Replace(catalogManifest, "depends_on: [search]", "depends_on: [search, search]", 1), "long_name": strings.Replace(catalogManifest, "Local lookup", strings.Repeat("a", 257), 1), "bad_identity": strings.Replace(catalogManifest, "id: local.lookup", "id: ../lookup", 1),
		"too_many_nodes": strings.Replace(catalogManifest, "    - {id: search, capability: memory.search}", strings.Repeat("    - {id: search, capability: memory.search}\n", 65), 1),
		"depth":          catalogManifest + "extra: " + strings.Repeat("[", 17) + "x" + strings.Repeat("]", 17),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := ValidateSkillManifest(raw); e == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}
func TestSkillManifestUnknownTargetsAreInert(t *testing.T) {
	raw := strings.ReplaceAll(catalogManifest, "memory.search", "unknown.remote.command")
	raw = strings.ReplaceAll(raw, "role: reasoning", "role: unknown_agent")
	r, e := ValidateSkillManifest(raw)
	if e != nil || len(r.Targets) != 2 || r.Targets[0].ID != "unknown.remote.command" || r.Targets[1].ID != "unknown_agent" || r.Targets[1].Support != "unsupported" {
		t.Fatal(r, e)
	}
}

func TestSkillManifestBoundaryAcceptanceAndRejection(t *testing.T) {
	// At the artifact limit, comments stay part of the exact artifact hash.
	raw := catalogManifest + "#" + strings.Repeat("x", MaxSkillManifestBytes-len(catalogManifest)-1)
	if r, e := ValidateSkillManifest(raw); e != nil || r.ContentBytes != MaxSkillManifestBytes {
		t.Fatal(e)
	}
	boundary := strings.Replace(catalogManifest, "Local lookup", strings.Repeat("字", 85)+"x", 1)
	if _, e := ValidateSkillManifest(boundary); e != nil {
		t.Fatal(e)
	}
	var caps, nodes []string
	for i := 0; i < 64; i++ {
		cap := fmt.Sprintf("cap%d", i)
		caps = append(caps, cap)
		nodes = append(nodes, fmt.Sprintf("    - {id: n%d, capability: %s}", i, cap))
	}
	raw = strings.Replace(catalogManifest, "[memory.search]", "["+strings.Join(caps, ",")+"]", 1)
	raw = raw[:strings.Index(raw, "    - {id:")] + strings.Join(nodes, "\n") + "\n"
	if r, e := ValidateSkillManifest(raw); e != nil || len(r.Targets) != 64 {
		t.Fatal(e)
	}
	for _, invalid := range []string{
		strings.Replace(raw, "cap63]", "cap63, cap64]", 1),
		strings.Replace(catalogManifest, "type: string", "type: arbitrary", 1),
		strings.Replace(catalogManifest, "name: Local lookup", "name: \"\\uD800\"", 1),
		strings.Replace(catalogManifest, "description: Untrusted descriptive text", "description: \"\\x00\"", 1),
		strings.Replace(catalogManifest, "description: Untrusted descriptive text", "description: "+strings.Repeat("x", 4097), 1),
		strings.Replace(catalogManifest, "status: active\n", "", 1),
		strings.Replace(catalogManifest, "max_tool_calls: 4", "max_tool_calls: 4.0", 1),
		strings.Replace(catalogManifest, "max_iterations: 3", "max_iterations: 0", 1),
		strings.Replace(catalogManifest, "max_tool_calls: 4", "max_tool_calls: 10001", 1),
		strings.Replace(catalogManifest, "min_coverage: 0.8", "min_coverage: 1.01", 1),
		strings.Replace(catalogManifest, "min_coverage: 0.8", "min_coverage: .inf", 1),
		strings.Replace(catalogManifest, "[memory.search]", "[!custom memory.search]", 1),
		strings.Replace(catalogManifest, "privacy: {max_external_trust: local_private}", "privacy: {<<: {max_external_trust: public_remote}, max_external_trust: local_private}", 1),
	} {
		if _, e := ValidateSkillManifest(invalid); e == nil {
			t.Fatal("accepted out-of-profile manifest")
		}
	}
}
func FuzzSkillManifest(f *testing.F) {
	f.Add(catalogManifest)
	f.Add("&a [*a]")
	f.Add("id: \"\\uD800\"")
	f.Add(strings.Repeat("[", 17))
	f.Fuzz(func(t *testing.T, raw string) {
		r, e := ValidateSkillManifest(raw)
		if e == nil {
			if r.Execution != "unavailable" || r.Evaluation != "not_run" || r.ArtifactHash != skillBytesHash(raw) || r.ContentHash != skillBytesHash(r.CanonicalManifest) {
				t.Fatal("invalid review")
			}
		}
	})
}
