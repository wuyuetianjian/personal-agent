package interaction

import (
	"encoding/json"
	"strconv"
)

// DecodeSkillRunIntent is shared with the eventual public API. It is deliberately
// exact and does not accept caller-produced calls, scopes, budgets or pin hashes.
func DecodeSkillRunIntent(raw []byte) (out SkillRunIntent, err error) {
	obj, err := skillJSONObjectLimit(raw, 6*32768+4096)
	if err != nil || len(obj) != 5 {
		return out, ErrInvalid
	}
	for _, key := range []string{"request_id", "catalog_id", "revision_id", "execution_profile"} {
		if _, ok := obj[key].(string); !ok {
			return out, ErrInvalid
		}
	}
	inputs, ok := obj["inputs"].(map[string]any)
	if !ok {
		return out, ErrInvalid
	}
	canonical, e := json.Marshal(inputs)
	if e != nil || len(canonical) > 32768 {
		return out, ErrInvalid
	}
	out = SkillRunIntent{RequestID: obj["request_id"].(string), CatalogID: obj["catalog_id"].(string), RevisionID: obj["revision_id"].(string), ExecutionProfile: obj["execution_profile"].(string), Inputs: canonical}
	if !operatorRequestID(out.RequestID) || !operatorRequestID(out.CatalogID) || !operatorRequestID(out.RevisionID) || len(out.ExecutionProfile) > 64 {
		return out, ErrInvalid
	}
	return out, nil
}

// encoding/json replaces unpaired UTF-16 surrogates. Reject those rather than
// pinning a different path/query from the literal input. Escaped backslashes are
// skipped so a literal six-character sequence remains ordinary data.
func skillJSONUnicode(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
