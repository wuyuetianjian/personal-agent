package interaction

// DecodeHookIntent is management input only. Policies are never evaluator file
// inputs and never an extension to the exact five-field SkillRunIntent.
func DecodeHookIntent(raw []byte) (out HookIntent, err error) {
	obj, e := skillJSONObjectLimit(raw, 53248)
	if e != nil || len(obj) != 4 {
		return out, ErrInvalid
	}
	for _, key := range []string{"request_id", "kind", "revision_id", "policy_text"} {
		if _, ok := obj[key].(string); !ok {
			return out, ErrInvalid
		}
	}
	out = HookIntent{RequestID: obj["request_id"].(string), Kind: obj["kind"].(string), RevisionID: obj["revision_id"].(string), PolicyText: obj["policy_text"].(string)}
	return out, validateHookIntent(out)
}
func validateHookIntent(in HookIntent) error {
	if !operatorRequestID(in.RequestID) {
		return ErrInvalid
	}
	switch in.Kind {
	case "import":
		if in.RevisionID != "" {
			return ErrInvalid
		}
		_, _, e := ParseHookPolicy([]byte(in.PolicyText))
		return e
	case "enable":
		if !operatorRequestID(in.RevisionID) || in.PolicyText != "" {
			return ErrInvalid
		}
	case "disable":
		if in.RevisionID != "" || in.PolicyText != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
