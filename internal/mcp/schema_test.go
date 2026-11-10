package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestSchemaValidationProfile(t *testing.T) {
	cases := []struct {
		name, schema, input string
		ok                  bool
	}{
		{"2020-composition", `{"type":"object","properties":{"x":{"oneOf":[{"type":"integer","minimum":2},{"type":"string","pattern":"^a+$"}]}},"required":["x"]}`, `{"x":"aaa"}`, true},
		{"composition-reject", `{"type":"object","properties":{"x":{"oneOf":[{"type":"integer","minimum":2},{"type":"string","pattern":"^a+$"}]}},"required":["x"]}`, `{"x":1}`, false},
		{"local-ref", `{"type":"object","$defs":{"value":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/value"}}}`, `{"x":"ok"}`, true},
		{"local-ref-invalid", `{"type":"object","$defs":{"value":{"type":"string"}},"properties":{"x":{"$ref":"#/$defs/value"}}}`, `{"x":5}`, false},
		{"network-ref", `{"type":"object","$ref":"https://must-not-fetch.invalid/schema"}`, `{}`, false},
		{"file-ref", `{"type":"object","$ref":"file:///must-not-read"}`, `{}`, false},
		{"unknown-dialect", `{"type":"object","$schema":"https://must-not-fetch.invalid/dialect"}`, `{}`, false},
		{"draft7", `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`, `{}`, true},
		{"bad-schema", `{"type":"object","required":true}`, `{}`, false},
		{"enum-annotation-keys", `{"type":"object","enum":[{"$schema":"ordinary-data"}]}`, `{"$schema":"ordinary-data"}`, true},
		{"property-keyword", `{"type":"object","properties":{"$schema":{"type":"string"}}}`, `{"$schema":"ordinary-data"}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, e := decodeJSON([]byte(tc.input))
			if e != nil {
				t.Fatal(e)
			}
			e = validateArguments(json.RawMessage(tc.schema), v)
			if (e == nil) != tc.ok {
				t.Fatalf("validation=%v want ok=%v", e, tc.ok)
			}
		})
	}
}
func TestStrictJSON(t *testing.T) {
	for _, s := range []string{`{"x":1,"x":2}`, `{"a":{"x":1,"x":2}}`, `{} {}`, `{"x":NaN}`, strings.Repeat("[", 40) + strings.Repeat("]", 40)} {
		if _, e := decodeJSON([]byte(s)); e == nil {
			t.Fatalf("accepted %s", s)
		}
	}
}
func TestCacheHintSemantics(t *testing.T) {
	for _, tc := range []struct {
		raw   string
		ttl   int64
		scope string
		bad   bool
	}{
		{`{}`, 0, "private", false}, {`{"ttlMs":-3}`, 0, "private", false}, {`{"ttlMs":0,"cacheScope":"public"}`, 0, "public", false}, {`{"ttlMs":1000,"cacheScope":"private"}`, 1000, "private", false}, {`{"ttlMs":0.5}`, 0, "", true}, {`{"cacheScope":"shared-secret"}`, 0, "", true},
	} {
		h, e := parseHints([]byte(tc.raw), "page")
		if (e != nil) != tc.bad {
			t.Fatalf("%s %v", tc.raw, e)
		}
		if tc.bad {
			continue
		}
		if h.TTLMillis != tc.ttl || h.CacheScope != tc.scope {
			t.Fatalf("%+v", h)
		}
		if h.Fresh(h.ReceivedAt) != (tc.ttl > 0) || h.Fresh(h.ReceivedAt.Add(time.Hour)) {
			t.Fatal(h)
		}
	}
}

// Run adversarial dependency regressions in an owned subprocess. A panic or
// runaway validator cannot crash/hang the parent suite; CommandContext reaps it.
func isolatedSchemaTest(t *testing.T) bool {
	t.Helper()
	if os.Getenv("MCP_SCHEMA_ISOLATED") == "1" {
		return false
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^"+t.Name()+"$", "-test.timeout=15s")
	cmd.Env = []string{"MCP_SCHEMA_ISOLATED=1"}
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("isolated schema regression: %v\n%s", e, out)
	}
	return true
}
func TestNumericDomainBoundaries(t *testing.T) {
	if isolatedSchemaTest(t) {
		return
	}
	normal := json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":0}}}`)
	for _, number := range []string{"1e1000001", "1e-1000001", "-1e1000001", "-1e-1000001", "1e309", "1e-309", strings.Repeat("9", 129)} {
		t.Run(number[:min(len(number), 18)], func(t *testing.T) {
			// This is also a boundary check against direct callers bypassing decoding.
			if e := validateArguments(normal, map[string]any{"x": json.Number(number)}); e == nil {
				t.Fatal("unsafe numeric input accepted")
			}
			if _, e := decodeJSON([]byte(`{"x":` + number + `}`)); e == nil {
				t.Fatal("numeric decode accepted unsupported domain")
			}
			if _, e := compileSchema(json.RawMessage(`{"type":"number","minimum":` + number + `}`)); e == nil {
				t.Fatal("unsafe numeric schema constraint accepted")
			}
			schema, e := compileSchema(normal)
			if e != nil {
				t.Fatal(e)
			}
			if e = schema.Validate(map[string]any{"x": json.Number(number)}); e == nil {
				t.Fatal("unsafe structured output accepted")
			}
		})
	}
	for _, number := range []string{"1e308", "1e-308", strings.Repeat("9", 128)} {
		v, e := decodeJSON([]byte(`{"x":` + number + `}`))
		if e != nil {
			t.Fatal(e)
		}
		if e = validateArguments(normal, v); e != nil {
			t.Fatalf("safe numeric boundary: %v", e)
		}
	}
}
func hiddenReferenceDAG(levels int) json.RawMessage {
	defs := map[string]any{}
	for i := 0; i < levels; i++ {
		r := map[string]any{"$ref": fmt.Sprintf("#/hidden/d%d", i+1)}
		defs[fmt.Sprintf("d%d", i)] = map[string]any{"allOf": []any{r, r}}
	}
	defs[fmt.Sprintf("d%d", levels)] = map[string]any{}
	raw, _ := json.Marshal(map[string]any{"type": "object", "$ref": "#/hidden/d0", "hidden": defs})
	return raw
}
func TestReferenceTargetProfileCannotBeBypassed(t *testing.T) {
	if isolatedSchemaTest(t) {
		return
	}
	cases := []json.RawMessage{
		hiddenReferenceDAG(24),
		json.RawMessage(`{"type":"object","$ref":"#/hidden","hidden":{"$schema":"https://not-supported.invalid/schema"}}`),
		json.RawMessage(`{"type":"object","$ref":"#/hidden","hidden":{"$dynamicRef":"#"}}`),
		json.RawMessage(`{"type":"object","$ref":"#/hidden","hidden":{"$ref":"#"}}`),
		json.RawMessage(`{"type":"object","$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`),
		json.RawMessage(`{"type":"object","$ref":"#"}`),
		json.RawMessage(`{"type":"object","$id":"urn:rebased","$ref":"#/hidden","hidden":{}}`),
	}
	for i, raw := range cases {
		if _, e := compileSchema(raw); e == nil {
			t.Errorf("unchecked reference graph %d accepted (%d bytes)", i, len(raw))
		}
	}
	// Annotations/property names remain data, and escaped known schema locations work.
	for _, raw := range []string{
		`{"type":"object","default":{"$ref":"#/hidden","$id":"data"},"properties":{"$ref":{"type":"string"}}}`,
		`{"type":"object","$defs":{"a/b~c":{"type":"object"}},"$ref":"#/$defs/a~1b~0c"}`,
		`{"type":"object","$defs":{"a b":{"type":"object"}},"$ref":"#/$defs/a%20b"}`,
	} {
		if _, e := compileSchema([]byte(raw)); e != nil {
			t.Errorf("checked annotation/reference compatibility: %v", e)
		}
	}
}

func TestSchemaDependencyPanicFailsClosed(t *testing.T) {
	err := schemaOperation(func() error { panic("SECRET dependency detail") })
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("panic boundary: %v", err)
	}
	if err := (&checkedSchema{}).Validate(map[string]any{}); err == nil {
		t.Fatal("unguarded dependency validator panic")
	}
	expected := errors.New("ordinary validation failure")
	if !errors.Is(schemaOperation(func() error { return expected }), expected) {
		t.Fatal("ordinary validation error lost")
	}
}

func checkedReferenceDAG(levels int) json.RawMessage {
	defs := map[string]any{}
	for i := 0; i < levels; i++ {
		ref := map[string]any{"$ref": fmt.Sprintf("#/$defs/d%d", i+1)}
		defs[fmt.Sprintf("d%d", i)] = map[string]any{"dependentSchemas": map[string]any{"a": ref, "b": ref}}
	}
	defs[fmt.Sprintf("d%d", levels)] = map[string]any{}
	raw, _ := json.Marshal(map[string]any{"type": "object", "$defs": defs, "$ref": "#/$defs/d0"})
	return raw
}
func TestCheckedReferenceExpansionBudget(t *testing.T) {
	if isolatedSchemaTest(t) {
		return
	}
	if _, err := compileSchema(checkedReferenceDAG(9)); err == nil {
		t.Fatal("acyclic ref expansion exceeds budget but compiled")
	}
	schema, err := compileSchema(checkedReferenceDAG(3))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(map[string]any{"a": true, "b": true}); err != nil {
		t.Fatal(err)
	}
}
func TestRPCZeroErrorIdentity(t *testing.T) {
	rpc := &CallError{Certainty: KnownResponse, Kind: "RPC error", Code: 0, IsRPC: true}
	if !strings.Contains(rpc.Error(), "RPC 0") {
		t.Fatal("zero RPC code lost", rpc)
	}
	local := failure(NotSent, "local failure", nil)
	if local.IsRPC || strings.Contains(local.Error(), "RPC") {
		t.Fatal("local error treated as remote", local)
	}
}

func TestSchemaCountConstraintNumericDomain(t *testing.T) {
	for _, keyword := range []string{"minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties", "minContains", "maxContains"} {
		raw := json.RawMessage(fmt.Sprintf(`{"%s":18446744073709551616}`, keyword))
		if _, err := compileSchema(raw); err == nil {
			t.Errorf("%s silently overflows dependency int conversion", keyword)
		}
	}
	for _, value := range []string{"2147483648", "1e308", "1.5", "-1"} {
		if _, err := compileSchema(json.RawMessage(`{"minItems":` + value + `}`)); err == nil {
			t.Errorf("unsupported count constraint accepted: %s", value)
		}
	}
	if _, err := compileSchema(json.RawMessage(`{"minItems":2147483647}`)); err != nil {
		t.Fatal(err)
	}
}
