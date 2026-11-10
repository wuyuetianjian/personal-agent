package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Reject duplicate keys, excessive nesting/nodes and trailing values before any
// typed decoding, hashing or validation can disagree about the same wire JSON.
// decodeJSON is the schema/value boundary. Keep exact decimal values, but do
// not give the dependency unbounded exponents or mantissas.
func decodeJSON(raw []byte) (any, error) {
	value, err := decodeWireJSON(raw)
	if err != nil {
		return nil, err
	}
	nodes := 0
	if err = boundNumbers(value, 0, &nodes); err != nil {
		return nil, err
	}
	return value, nil
}

// Wire structure is checked before applying value-domain restrictions, so a
// recognized tool response with an unsupported number remains known_response.
func decodeWireJSON(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	v, e := decodeValue(d, 0, &nodes)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return v, nil
}
func decodeValue(d *json.Decoder, depth int, nodes *int) (any, error) {
	*nodes++
	if depth > 32 || *nodes > 100000 {
		return nil, errors.New("JSON complexity limit")
	}
	tok, e := d.Token()
	if e != nil {
		return nil, e
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, e
			}
			key, ok := k.(string)
			if !ok {
				return nil, errors.New("invalid key")
			}
			if _, exists := m[key]; exists {
				return nil, errors.New("duplicate key")
			}
			v, e := decodeValue(d, depth+1, nodes)
			if e != nil {
				return nil, e
			}
			m[key] = v
		}
		_, e = d.Token()
		return m, e
	case '[':
		a := []any{}
		for d.More() {
			v, e := decodeValue(d, depth+1, nodes)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		_, e = d.Token()
		return a, e
	}
	return nil, errors.New("invalid delimiter")
}

type noExternalSchemas struct{}

func (noExternalSchemas) Load(string) (any, error) {
	return nil, errors.New("external schema loading disabled")
}
func validateArguments(raw json.RawMessage, args any) error {
	schema, err := compileSchema(raw)
	if err != nil {
		return err
	}
	return schema.Validate(args)
}

// checkedSchema never exposes an unguarded dependency validation entry point.
type checkedSchema struct{ schema *jsonschema.Schema }

func (s *checkedSchema) Validate(value any) error {
	nodes := 0
	if err := boundNumbers(value, 0, &nodes); err != nil {
		return err
	}
	return schemaOperation(func() error { return s.schema.Validate(value) })
}

// Last-resort containment for dependency bugs, not a replacement for resource
// bounds. Work stays synchronous; no validation goroutine can outlive a call.
func schemaOperation(fn func() error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("schema dependency failure")
		}
	}()
	return fn()
}
func compileSchema(raw json.RawMessage) (*checkedSchema, error) {
	if len(raw) > 64<<10 {
		return nil, errors.New("schema bytes limit")
	}
	doc, e := decodeJSON(raw)
	if e != nil {
		return nil, e
	}
	nodes, choices, refs := 0, 1, 0
	if e = boundSchema(doc, 0, &nodes, &choices, &refs); e != nil {
		return nil, e
	}
	if refs > 0 && choices > 1 {
		return nil, errors.New("reference/composition combination exceeds supported profile")
	}
	if e = boundReferenceGraph(doc); e != nil {
		return nil, e
	}
	var compiled *jsonschema.Schema
	e = schemaOperation(func() error {
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		compiler.UseLoader(noExternalSchemas{})
		if err := compiler.AddResource("urn:personal-agent:mcp-schema", doc); err != nil {
			return err
		}
		var err error
		compiled, err = compiler.Compile("urn:personal-agent:mcp-schema")
		return err
	})
	if e != nil {
		return nil, e
	}
	return &checkedSchema{schema: compiled}, nil
}

// Numeric tokens are at most 128 bytes and decimal exponents are in [-308,308].
// This bounds exact rational construction without lossy float conversion.
func boundedNumber(number string) error {
	if len(number) > 128 || !json.Valid([]byte(number)) {
		return errors.New("unsupported numeric domain")
	}
	if i := strings.IndexAny(number, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(number[i+1:])
		if err != nil || exponent < -308 || exponent > 308 {
			return errors.New("unsupported numeric exponent")
		}
	}
	if _, ok := new(big.Rat).SetString(number); !ok {
		return errors.New("unsupported numeric value")
	}
	return nil
}
func boundNumbers(value any, depth int, nodes *int) error {
	*nodes++
	if depth > 32 || *nodes > 100000 {
		return errors.New("value complexity limit")
	}
	switch v := value.(type) {
	case json.Number:
		return boundedNumber(string(v))
	case map[string]any:
		for _, child := range v {
			if err := boundNumbers(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := boundNumbers(child, depth+1, nodes); err != nil {
				return err
			}
		}
	case nil, bool, string:
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		raw, err := json.Marshal(v)
		if err != nil {
			return errors.New("unsupported numeric value")
		}
		return boundedNumber(string(raw))
	default:
		return errors.New("unsupported JSON value")
	}
	return nil
}
func boundSchema(v any, depth int, nodes, choices, refs *int) error {
	if err := boundSchemaSize(v, depth, nodes); err != nil {
		return err
	}
	return boundSchemaKeywords(v, choices, refs)
}
func boundSchemaSize(v any, depth int, nodes *int) error {
	*nodes++
	if depth > 16 || *nodes > 512 {
		return errors.New("schema complexity limit")
	}
	switch value := v.(type) {
	case map[string]any:
		for _, x := range value {
			if err := boundSchemaSize(x, depth+1, nodes); err != nil {
				return err
			}
		}
	case []any:
		for _, x := range value {
			if err := boundSchemaSize(x, depth+1, nodes); err != nil {
				return err
			}
		}
	}
	return nil
}

// Visit schema positions, not arbitrary annotation/enum data or property names.
func boundSchemaKeywords(v any, choices, refs *int) error {
	value, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	for k, x := range value {
		switch k {
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties", "minContains", "maxContains":
			// The dependency stores count constraints in int via Int64().
			// Reject overflow instead of silently turning a huge minimum into 0.
			number, ok := x.(json.Number)
			if !ok {
				return errors.New("invalid schema count constraint")
			}
			n, ok := new(big.Rat).SetString(string(number))
			if !ok || !n.IsInt() || n.Sign() < 0 || n.Num().Cmp(big.NewInt(2147483647)) > 0 {
				return errors.New("schema count constraint outside supported domain")
			}
		case "$schema":
			s, ok := x.(string)
			if !ok || !(s == "https://json-schema.org/draft/2020-12/schema" || s == "http://json-schema.org/draft-07/schema#" || s == "https://json-schema.org/draft-07/schema#") {
				return errors.New("schema dialect unsupported")
			}
		case "$ref":
			s, ok := x.(string)
			if !ok || !strings.HasPrefix(s, "#") {
				return errors.New("external reference unsupported")
			}
			*refs++
			if *refs > 32 {
				return errors.New("schema reference limit")
			}
		case "$dynamicRef", "$recursiveRef":
			return errors.New("dynamic references unsupported")
		case "anyOf", "oneOf", "allOf":
			if a, ok := x.([]any); ok {
				*choices *= len(a) + 1
				if *choices > 64 {
					return errors.New("schema branch limit")
				}
				for _, sub := range a {
					if e := boundSchemaKeywords(sub, choices, refs); e != nil {
						return e
					}
				}
			}
		case "if", "then", "else":
			*choices *= 2
			if *choices > 64 {
				return errors.New("schema branch limit")
			}
			if e := boundSchemaKeywords(x, choices, refs); e != nil {
				return e
			}
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
			if m, ok := x.(map[string]any); ok {
				for _, sub := range m {
					if e := boundSchemaKeywords(sub, choices, refs); e != nil {
						return e
					}
				}
			}
		case "items", "prefixItems", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "contains", "propertyNames", "not", "contentSchema":
			if a, ok := x.([]any); ok {
				for _, sub := range a {
					if e := boundSchemaKeywords(sub, choices, refs); e != nil {
						return e
					}
				}
			} else if e := boundSchemaKeywords(x, choices, refs); e != nil {
				return e
			}
		}
	}
	return nil
}

// Only JSON Pointer targets which were visited as schema positions are allowed.
// Unknown/annotation data is never promoted into unchecked executable schema.
// Rebased resources and anchor refs are outside this conservative profile.
func boundReferenceGraph(doc any) error {
	known := map[string]any{}
	edges := map[string][]string{}
	var visit func(any, string) error
	visit = func(value any, path string) error {
		switch value.(type) {
		case map[string]any, bool:
		default:
			return nil
		}
		known[path] = value
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		add := func(child any, childPath string) error {
			if err := visit(child, childPath); err != nil {
				return err
			}
			if _, ok := known[childPath]; ok {
				edges[path] = append(edges[path], childPath)
			}
			return nil
		}
		for key, child := range object {
			childPath := schemaPointer(path, key)
			switch key {
			case "$id", "$anchor", "$dynamicAnchor", "$recursiveAnchor":
				return errors.New("schema resource/anchor rebasing unsupported")
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				if children, ok := child.(map[string]any); ok {
					for name, sub := range children {
						if err := add(sub, schemaPointer(childPath, name)); err != nil {
							return err
						}
					}
				}
			case "anyOf", "oneOf", "allOf", "prefixItems":
				if children, ok := child.([]any); ok {
					for i, sub := range children {
						if err := add(sub, schemaPointer(childPath, strconv.Itoa(i))); err != nil {
							return err
						}
					}
				}
			case "items", "additionalItems", "additionalProperties", "unevaluatedItems", "unevaluatedProperties", "contains", "propertyNames", "not", "contentSchema", "if", "then", "else":
				if children, ok := child.([]any); ok {
					for i, sub := range children {
						if err := add(sub, schemaPointer(childPath, strconv.Itoa(i))); err != nil {
							return err
						}
					}
				} else if err := add(child, childPath); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(doc, ""); err != nil {
		return err
	}
	for path, value := range known {
		object, ok := value.(map[string]any)
		if !ok {
			continue
		}
		ref, ok := object["$ref"]
		if !ok {
			continue
		}
		raw, ok := ref.(string)
		if !ok || !strings.HasPrefix(raw, "#") {
			return errors.New("unsupported schema reference")
		}
		pointer, err := url.PathUnescape(raw[1:])
		if err != nil || (pointer != "" && !strings.HasPrefix(pointer, "/")) {
			return errors.New("only local JSON Pointer schema references supported")
		}
		target := ""
		if pointer != "" {
			for _, token := range strings.Split(pointer[1:], "/") {
				for i := 0; i < len(token); i++ {
					if token[i] == '~' {
						if i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1') {
							return errors.New("invalid schema reference pointer")
						}
						i++
					}
				}
				decoded := strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
				target = schemaPointer(target, decoded)
			}
		}
		if _, ok := known[target]; !ok {
			return errors.New("reference target outside checked schema locations")
		}
		edges[path] = append(edges[path], target)
	}
	// Include structural edges, not only ref edges: a ref to an ancestor can
	// recurse through properties/items. Reject cycles, and bound even acyclic
	// repeated DAG expansion rather than merely counting unique schema nodes.
	state := map[string]uint8{}
	cost := map[string]int{}
	var measure func(string) (int, error)
	measure = func(path string) (int, error) {
		if state[path] == 1 {
			return 0, errors.New("cyclic schema reference unsupported")
		}
		if state[path] == 2 {
			return cost[path], nil
		}
		state[path] = 1
		total := 1
		for _, child := range edges[path] {
			n, err := measure(child)
			if err != nil {
				return 0, err
			}
			if n > 1024-total {
				return 0, errors.New("schema expanded evaluation limit")
			}
			total += n
		}
		state[path] = 2
		cost[path] = total
		return total, nil
	}
	for path := range known {
		if _, err := measure(path); err != nil {
			return err
		}
	}
	return nil
}
func schemaPointer(parent, token string) string {
	return parent + "/" + strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1")
}
