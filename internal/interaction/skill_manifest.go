package interaction

// This profile mirrors the data fields in skill.Manifest, not its mutable
// loader, normalizer, registry, evaluator or executor. It grants no authority.
import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const MaxSkillManifestBytes = 64 << 10
const SkillParserVersion = "strict-manifest-v1"
const SkillReviewVersion = "catalog-no-execution-v1"

// SkillManifest deliberately excludes legacy CreatedAt and execution objects.
type SkillManifest struct {
	ID           string                `yaml:"id" json:"id"`
	Version      string                `yaml:"version" json:"version"`
	Name         string                `yaml:"name" json:"name"`
	Description  string                `yaml:"description" json:"description"`
	Status       string                `yaml:"status" json:"status"`
	Inputs       map[string]SkillField `yaml:"inputs" json:"inputs"`
	Requires     SkillRequirements     `yaml:"requires" json:"requires"`
	Permissions  SkillPermissions      `yaml:"permissions" json:"permissions"`
	Privacy      SkillPrivacy          `yaml:"privacy" json:"privacy"`
	Budget       SkillBudget           `yaml:"budget" json:"budget"`
	Verification SkillVerification     `yaml:"verification" json:"verification"`
	Workflow     SkillWorkflow         `yaml:"workflow" json:"workflow"`
}
type SkillField struct {
	Type string `yaml:"type" json:"type"`
}
type SkillRequirements struct {
	Capabilities []string `yaml:"capabilities" json:"capabilities"`
}
type SkillPermissions struct {
	MaxLevel string `yaml:"max_level" json:"max_level"`
}
type SkillPrivacy struct {
	MaxExternalTrust string `yaml:"max_external_trust" json:"max_external_trust"`
}
type SkillBudget struct {
	MaxIterations int `yaml:"max_iterations" json:"max_iterations"`
	MaxToolCalls  int `yaml:"max_tool_calls" json:"max_tool_calls"`
}
type SkillVerification struct {
	MinCoverage    float64 `yaml:"min_coverage" json:"min_coverage"`
	AllowConflicts bool    `yaml:"allow_conflicts" json:"allow_conflicts"`
}
type SkillWorkflow struct {
	Nodes []SkillNode `yaml:"nodes" json:"nodes"`
}
type SkillNode struct {
	ID         string   `yaml:"id" json:"id"`
	Capability string   `yaml:"capability" json:"capability"`
	Role       string   `yaml:"role" json:"role"`
	DependsOn  []string `yaml:"depends_on" json:"depends_on"`
}
type SkillTargetReview struct {
	NodeID  string `json:"node_id"`
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Support string `json:"support"`
}
type SkillReview struct {
	ParserVersion     string              `json:"parser_version"`
	ReviewVersion     string              `json:"review_version"`
	ArtifactHash      string              `json:"artifact_sha256"`
	ContentHash       string              `json:"content_sha256"`
	ContentBytes      int                 `json:"content_bytes"`
	CanonicalManifest string              `json:"canonical_manifest"`
	Manifest          SkillManifest       `json:"manifest"`
	Targets           []SkillTargetReview `json:"targets"`
	Evaluation        string              `json:"evaluation"`
	Execution         string              `json:"execution"`
	ExecutionReason   string              `json:"execution_reason"`
	Notice            string              `json:"notice"`
}

const skillNotice = "Service-local metadata only. Manifest text, labels and declared policies are untrusted descriptions, not grants. Input status is ignored for activation. Enabled means an explicitly selected immutable revision only; evaluation not_run; execution unavailable: bridge_not_implemented. No provider, tool, process, retrieval or workflow calls."

func skillBytesHash(s string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(s))) }
func skillInvalid(reason string) error {
	return fmt.Errorf("%w: skill manifest %s", ErrInvalid, reason)
}

// ValidateSkillManifest reads only the given bounded UTF-8 text. Errors do not
// echo untrusted content. No path, URL, provider, capability registry or scripts.
func ValidateSkillManifest(raw string) (out SkillReview, err error) {
	if len(raw) > MaxSkillManifestBytes {
		return out, ErrBudget
	}
	if !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) || strings.TrimSpace(raw) == "" {
		return out, skillInvalid("must be UTF-8 text")
	}
	d := yaml.NewDecoder(strings.NewReader(raw))
	var doc, extra yaml.Node
	if d.Decode(&doc) != nil || len(doc.Content) != 1 {
		return out, skillInvalid("must be one YAML document")
	}
	if d.Decode(&extra) != io.EOF {
		return out, skillInvalid("must be one YAML document")
	}
	count := 0
	if err = skillYAMLShape(doc.Content[0], reflect.TypeOf(SkillManifest{}), 1, &count); err != nil {
		return out, err
	}
	if doc.Content[0].Decode(&out.Manifest) != nil {
		return out, skillInvalid("has invalid typed values")
	}
	m := out.Manifest
	if !skillIdentity(m.ID, 128) || !skillIdentity(m.Version, 64) || strings.TrimSpace(m.Name) == "" || len(m.Name) > 256 || len(m.Description) > 4096 {
		return out, skillInvalid("identity or label bounds")
	}
	if !skillEnum(m.Status, "draft", "active", "deprecated", "disabled") || !skillEnum(m.Permissions.MaxLevel, "read_only", "write") || !skillEnum(m.Privacy.MaxExternalTrust, "local_private", "local_sandboxed", "trusted_remote", "public_remote") {
		return out, skillInvalid("status, permission or privacy enum")
	}
	if m.Budget.MaxIterations < 1 || m.Budget.MaxIterations > 1000 || m.Budget.MaxToolCalls < 0 || m.Budget.MaxToolCalls > 10000 || math.IsNaN(m.Verification.MinCoverage) || math.IsInf(m.Verification.MinCoverage, 0) || m.Verification.MinCoverage < 0 || m.Verification.MinCoverage > 1 {
		return out, skillInvalid("budget or verification bounds")
	}
	if len(m.Inputs) > 32 || len(m.Requires.Capabilities) > 64 || len(m.Workflow.Nodes) == 0 || len(m.Workflow.Nodes) > 64 {
		return out, skillInvalid("collection bounds")
	}
	for key, f := range m.Inputs {
		if !skillIdentity(key, 64) || !skillEnum(f.Type, "string", "integer", "number", "boolean", "object", "array") {
			return out, skillInvalid("input declaration")
		}
	}
	required, used := map[string]bool{}, map[string]bool{}
	for _, cap := range m.Requires.Capabilities {
		if !skillIdentity(cap, 128) || required[cap] {
			return out, skillInvalid("duplicate or invalid requirement")
		}
		required[cap] = true
	}
	nodes := map[string]SkillNode{}
	out.Targets = []SkillTargetReview{}
	for _, n := range m.Workflow.Nodes {
		if !skillIdentity(n.ID, 64) || nodes[n.ID].ID != "" || (n.Capability == "") == (n.Role == "") || len(n.DependsOn) > 64 {
			return out, skillInvalid("ambiguous or invalid node")
		}
		target, kind := n.Capability, "capability"
		if n.Role != "" {
			target, kind = n.Role, "role"
		}
		if !skillIdentity(target, 128) {
			return out, skillInvalid("invalid target")
		}
		if n.Capability != "" {
			if !required[target] {
				return out, skillInvalid("undeclared capability")
			}
			used[target] = true
		}
		nodes[n.ID] = n
		out.Targets = append(out.Targets, SkillTargetReview{NodeID: n.ID, Kind: kind, ID: target, Support: "unsupported"})
	}
	if len(used) != len(required) {
		return out, skillInvalid("unused requirement")
	}
	for _, n := range m.Workflow.Nodes {
		seen := map[string]bool{}
		for _, dep := range n.DependsOn {
			if nodes[dep].ID == "" || seen[dep] {
				return out, skillInvalid("invalid or duplicate dependency")
			}
			seen[dep] = true
		}
	}
	colors := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if colors[id] == 1 {
			return false
		}
		if colors[id] == 2 {
			return true
		}
		colors[id] = 1
		for _, dep := range nodes[id].DependsOn {
			if !visit(dep) {
				return false
			}
		}
		colors[id] = 2
		return true
	}
	for id := range nodes {
		if !visit(id) {
			return out, skillInvalid("cyclic workflow")
		}
	}
	canonical, e := json.Marshal(m)
	if e != nil {
		return out, skillInvalid("canonical encoding")
	}
	out.ParserVersion, out.ReviewVersion = SkillParserVersion, SkillReviewVersion
	out.ArtifactHash, out.ContentHash, out.ContentBytes = skillBytesHash(raw), skillBytesHash(string(canonical)), len(raw)
	out.CanonicalManifest = string(canonical)
	out.Evaluation, out.Execution, out.ExecutionReason, out.Notice = "not_run", "unavailable", "bridge_not_implemented", skillNotice
	return out, nil
}
func skillIdentity(s string, max int) bool { return len(s) <= max && operatorRequestID(s) }
func skillEnum(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}

// Shape validation precedes decoding: yaml.v3's string coercions are not used.
// All struct keys are required except node capability/role/depends_on; exactly
// one target is checked above. No implicit defaults or null values.
func skillYAMLShape(n *yaml.Node, t reflect.Type, depth int, count *int) error {
	*count++
	if depth > 16 || *count > 2048 {
		return skillInvalid("nesting or syntax node bounds")
	}
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Style&yaml.TaggedStyle != 0 {
		return skillInvalid("aliases, anchors and explicit tags forbidden")
	}
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		if n.Kind != yaml.MappingNode || n.Tag != "!!map" {
			return skillInvalid("expected mapping")
		}
		fields := map[string]reflect.Type{}
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				fields[f.Tag.Get("yaml")] = f.Type
			}
		}
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			if e := skillYAMLShape(k, reflect.TypeOf(""), depth+1, count); e != nil {
				return e
			}
			if seen[k.Value] {
				return skillInvalid("duplicate key")
			}
			seen[k.Value] = true
			var ft reflect.Type
			if t.Kind() == reflect.Map {
				ft = t.Elem()
			} else {
				ft = fields[k.Value]
				if ft == nil {
					return skillInvalid("unknown key")
				}
			}
			if e := skillYAMLShape(n.Content[i+1], ft, depth+1, count); e != nil {
				return e
			}
		}
		if t.Kind() == reflect.Struct {
			for key := range fields {
				if t == reflect.TypeOf(SkillNode{}) && (key == "capability" || key == "role" || key == "depends_on") {
					continue
				}
				if !seen[key] {
					return skillInvalid("missing explicit field")
				}
			}
		}
	case reflect.Slice:
		if n.Kind != yaml.SequenceNode || n.Tag != "!!seq" || len(n.Content) > 64 {
			return skillInvalid("sequence bounds or shape")
		}
		for _, v := range n.Content {
			if e := skillYAMLShape(v, t.Elem(), depth+1, count); e != nil {
				return e
			}
		}
	default:
		if n.Kind != yaml.ScalarNode || !utf8.ValidString(n.Value) || strings.ContainsRune(n.Value, 0) {
			return skillInvalid("invalid scalar")
		}
		tag := ""
		switch t.Kind() {
		case reflect.String:
			tag = "!!str"
		case reflect.Int:
			tag = "!!int"
		case reflect.Bool:
			tag = "!!bool"
		case reflect.Float64:
			if n.Tag == "!!int" {
				return nil
			}
			tag = "!!float"
		}
		if n.Tag != tag {
			return skillInvalid("strict scalar type")
		}
	}
	return nil
}
