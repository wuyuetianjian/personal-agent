package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// UnsupportedCapability is explicit: UI SSE is not an MCP transport.
func UnsupportedCapability(name string) error {
	switch name {
	case "http", "streamable-http", "sse":
		return errors.New("MCP HTTP transports are not implemented; only stdio is supported")
	case "tasks", "input_required":
		return errors.New("MCP Tasks and interactive continuation are not implemented or advertised")
	}
	return errors.New("MCP capability is not implemented")
}
func complete(raw json.RawMessage) error {
	var result struct {
		ResultType *string `json:"resultType"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return failure(KnownResponse, "invalid result", nil)
	}
	// Absent resultType is complete for older peers. Null is not absent.
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if typ, ok := fields["resultType"]; ok && (string(typ) == "null" || result.ResultType == nil) {
		return failure(KnownResponse, "invalid resultType", nil)
	}
	if result.ResultType != nil && *result.ResultType != "complete" {
		return failure(KnownResponse, "unsupported resultType (no continuation or replay)", nil)
	}
	return nil
}
func (c *StdioClient) negotiate(ctx context.Context) error {
	probe, cancel := context.WithTimeout(ctx, c.cfg.ProbeTimeout)
	result, err := c.request(probe, ModernVersion, "server/discover", nil)
	cancel()
	if err != nil {
		var rpc *CallError
		if errors.As(err, &rpc) && rpc.IsRPC && rpc.Code == -32022 {
			var data struct {
				Supported []string `json:"supported"`
			}
			_ = json.Unmarshal(rpc.data, &data)
			if !contains(data.Supported, ModernVersion) {
				return failure(NotSent, "unsupported protocol version (no modern intersection)", nil)
			}
			// The sole implemented modern version is also in the server's intersection.
			// Retry discovery once, never an effect and never legacy initialize.
			probe, cancel = context.WithTimeout(ctx, c.cfg.ProbeTimeout)
			result, err = c.request(probe, ModernVersion, "server/discover", nil)
			cancel()
			if err != nil {
				return err
			}
		} else if errors.As(err, &rpc) && rpc.IsRPC && (rpc.Code == -32020 || rpc.Code == -32021) {
			return err
		} else {
			// Only remote non-modern errors or a bounded probe timeout allow fallback.
			if ctx.Err() != nil {
				return failure(NotSent, "cancelled", ctx.Err())
			}
			if !(errors.As(err, &rpc) && rpc.IsRPC) && !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			result, err = c.request(ctx, LegacyVersion, "initialize", map[string]any{"protocolVersion": LegacyVersion, "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "personal-agent", "version": "1"}})
			if err != nil {
				return err
			}
			if err = complete(result.Raw); err != nil {
				return err
			}
			var init struct {
				Version      string                     `json:"protocolVersion"`
				Capabilities map[string]json.RawMessage `json:"capabilities"`
			}
			if json.Unmarshal(result.Raw, &init) != nil || init.Version != LegacyVersion || init.Capabilities == nil {
				return failure(NotSent, "unsupported legacy initialization", nil)
			}
			initCtx, done := context.WithTimeout(ctx, c.cfg.RequestTimeout)
			defer done()
			if err = c.initialized(initCtx); err != nil {
				return failure(NotSent, "initialized notification failed", safeCause(err))
			}
			c.version = LegacyVersion
			c.capabilities = init.Capabilities
			return nil
		}
	}
	if err = complete(result.Raw); err != nil {
		return err
	}
	var discovery struct {
		Supported    []string                   `json:"supportedVersions"`
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	if json.Unmarshal(result.Raw, &discovery) != nil || !contains(discovery.Supported, ModernVersion) || discovery.Capabilities == nil {
		return failure(NotSent, "unsupported modern discovery", nil)
	}
	if _, err = parseHints(result.Raw, ""); err != nil {
		return err
	}
	c.version = ModernVersion
	c.capabilities = discovery.Capabilities
	return nil
}
func contains(v []string, want string) bool {
	for _, s := range v {
		if s == want {
			return true
		}
	}
	return false
}

type ToolDefinition struct {
	Name         string          `json:"name"`
	Description  string          `json:"description,omitempty"`
	InputSchema  json.RawMessage `json:"inputSchema"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	SchemaDigest string          `json:"-"`
}
type PageHint struct {
	Cursor     string
	TTLMillis  int64
	CacheScope string
	ReceivedAt time.Time
}

func (h PageHint) Fresh(now time.Time) bool {
	return h.TTLMillis > 0 && now.Before(h.ReceivedAt.Add(time.Duration(h.TTLMillis)*time.Millisecond))
}

type ToolList struct {
	Tools []ToolDefinition
	Pages []PageHint
}

func parseHints(raw json.RawMessage, cursor string) (PageHint, error) {
	var wire struct {
		TTL   *int64 `json:"ttlMs"`
		Scope string `json:"cacheScope"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return PageHint{}, failure(KnownResponse, "invalid cache hints", nil)
	}
	hint := PageHint{Cursor: cursor, CacheScope: wire.Scope, ReceivedAt: time.Now()}
	if hint.CacheScope == "" {
		hint.CacheScope = "private"
	}
	if hint.CacheScope != "private" && hint.CacheScope != "public" {
		return PageHint{}, failure(KnownResponse, "invalid cache scope", nil)
	}
	if wire.TTL != nil && *wire.TTL > 0 {
		hint.TTLMillis = *wire.TTL
		if hint.TTLMillis > int64((24*time.Hour)/time.Millisecond) {
			hint.TTLMillis = int64((24 * time.Hour) / time.Millisecond)
		}
	}
	return hint, nil
}

// ListTools always fetches a complete bounded list from its beginning. Hints are
// exposed per page but never shared across clients/authorization contexts. No
// stale-cache serving or background polling. This intentionally conservative
// no-cache policy also handles list_changed without a stale schema cache.
func (c *StdioClient) ListTools(ctx context.Context) (ToolList, error) {
	var out ToolList
	if _, ok := c.capabilities["tools"]; !ok {
		return out, failure(NotSent, "tools capability unavailable", nil)
	}
	seen := map[string]bool{}
	names := map[string]bool{}
	cursor := ""
	total := 0
	scope := ""
	for page := 0; page < c.cfg.Limits.MaxPages; page++ {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		r, e := c.request(ctx, c.version, "tools/list", params)
		if e != nil {
			return ToolList{}, e
		}
		if e = complete(r.Raw); e != nil {
			return ToolList{}, e
		}
		total += len(r.Raw)
		if total > c.cfg.Limits.MaxListBytes {
			return ToolList{}, failure(KnownResponse, "list bytes limit", nil)
		}
		var data struct {
			Tools []ToolDefinition `json:"tools"`
			Next  string           `json:"nextCursor"`
		}
		if json.Unmarshal(r.Raw, &data) != nil || data.Tools == nil {
			return ToolList{}, failure(KnownResponse, "invalid tools list", nil)
		}
		hint, e := parseHints(r.Raw, cursor)
		if e != nil {
			return ToolList{}, e
		}
		if scope != "" && hint.CacheScope != scope {
			return ToolList{}, failure(KnownResponse, "inconsistent page cache scope", nil)
		}
		scope = hint.CacheScope
		out.Pages = append(out.Pages, hint)
		for _, tool := range data.Tools {
			if tool.Name == "" || len(tool.Name) > 256 || names[tool.Name] || len(out.Tools) >= c.cfg.Limits.MaxTools {
				return ToolList{}, failure(KnownResponse, "invalid, duplicate, or excessive tools", nil)
			}
			schema, e := decodeJSON(tool.InputSchema)
			if e != nil {
				return ToolList{}, failure(KnownResponse, "invalid input schema", nil)
			}
			obj, ok := schema.(map[string]any)
			if !ok || obj["type"] != "object" {
				return ToolList{}, failure(KnownResponse, "object input schema required", nil)
			}
			// Bind both input and output schema, using canonical JSON object-key order.
			var output any
			if len(tool.OutputSchema) > 0 {
				output, e = decodeJSON(tool.OutputSchema)
				if e != nil {
					return ToolList{}, failure(KnownResponse, "invalid output schema", nil)
				}
			}
			canonical, _ := json.Marshal(map[string]any{"inputSchema": schema, "outputSchema": output})
			sum := sha256.Sum256(canonical)
			tool.SchemaDigest = hex.EncodeToString(sum[:])
			names[tool.Name] = true
			out.Tools = append(out.Tools, tool)
		}
		if data.Next == "" {
			return out, nil
		}
		if seen[data.Next] {
			return ToolList{}, failure(KnownResponse, "pagination cursor loop", nil)
		}
		seen[data.Next] = true
		cursor = data.Next
	}
	return ToolList{}, failure(KnownResponse, "list page limit", nil)
}

// CallTool performs preflight discovery/schema validation then sends exactly one
// tools/call. It does NOT authorize effects; use only after caller authorization.
func (c *StdioClient) CallTool(ctx context.Context, name string, input json.RawMessage) (CallResult, error) {
	return c.CallToolPinned(ctx, name, input, "")
}

// CallToolPinned additionally rejects drift from a caller-approved schema digest.
// It is a seam for the future durable broker, not an approval system itself.
func (c *StdioClient) CallToolPinned(ctx context.Context, name string, input json.RawMessage, digest string) (CallResult, error) {
	notSent := CallResult{Certainty: NotSent}
	if e := ctx.Err(); e != nil {
		return notSent, failure(NotSent, "cancelled", e)
	}
	if len(input) > c.cfg.Limits.MaxFrameBytes {
		return notSent, failure(NotSent, "arguments size limit", nil)
	}
	args, e := decodeJSON(input)
	if e != nil {
		return notSent, failure(NotSent, "invalid arguments JSON", nil)
	}
	if _, ok := args.(map[string]any); !ok {
		return notSent, failure(NotSent, "arguments must be object", nil)
	}
	list, e := c.ListTools(ctx)
	if e != nil {
		return notSent, failure(NotSent, "tool discovery failed", safeCause(e))
	}
	var tool *ToolDefinition
	for i := range list.Tools {
		if list.Tools[i].Name == name {
			tool = &list.Tools[i]
			break
		}
	}
	if tool == nil {
		return notSent, failure(NotSent, "tool unavailable", nil)
	}
	if digest != "" && digest != tool.SchemaDigest {
		return notSent, failure(NotSent, "schema digest changed", nil)
	}
	if e = validateArguments(tool.InputSchema, args); e != nil {
		return notSent, failure(NotSent, "arguments or schema invalid/unsupported", nil)
	}
	var outputSchema interface{ Validate(any) error }
	if len(tool.OutputSchema) > 0 {
		outputSchema, e = compileSchema(tool.OutputSchema)
		if e != nil {
			return notSent, failure(NotSent, "output schema invalid/unsupported", nil)
		}
	}
	r, e := c.request(ctx, c.version, "tools/call", map[string]any{"name": name, "arguments": args})
	if e != nil {
		return r, e
	}
	if _, err := decodeJSON(r.Raw); err != nil {
		return r, failure(KnownResponse, "invalid tool result numeric/value profile", nil)
	}
	if e = complete(r.Raw); e != nil {
		return r, e
	}
	var result struct {
		IsError    bool              `json:"isError"`
		Content    []json.RawMessage `json:"content"`
		Structured json.RawMessage   `json:"structuredContent"`
	}
	if json.Unmarshal(r.Raw, &result) != nil || result.Content == nil {
		return r, failure(KnownResponse, "invalid tool result", nil)
	}
	r.IsError = result.IsError
	if result.IsError {
		return r, failure(KnownResponse, "tool failure", nil)
	}
	if len(tool.OutputSchema) > 0 {
		value, err := decodeJSON(result.Structured)
		if err != nil {
			return r, failure(KnownResponse, "invalid structured tool output", nil)
		}
		if err = outputSchema.Validate(value); err != nil {
			return r, failure(KnownResponse, "output schema validation failed", nil)
		}
	}
	return r, nil
}

// ValidateToolDefinition verifies the bounded supported schema profile without
// launching a process or sending any request. Peer annotations grant no rights.
func ValidateToolDefinition(t ToolDefinition) error {
	if _, e := compileSchema(t.InputSchema); e != nil {
		return failure(NotSent, "input schema invalid/unsupported", nil)
	}
	if len(t.OutputSchema) > 0 {
		if _, e := compileSchema(t.OutputSchema); e != nil {
			return failure(NotSent, "output schema invalid/unsupported", nil)
		}
	}
	return nil
}

// ValidateToolArguments is the broker's pre-approval validation boundary.
func ValidateToolArguments(t ToolDefinition, input json.RawMessage) error {
	if len(input) > 64<<10 {
		return failure(NotSent, "arguments size limit", nil)
	}
	args, e := decodeJSON(input)
	if e != nil {
		return failure(NotSent, "invalid arguments JSON", nil)
	}
	if _, ok := args.(map[string]any); !ok {
		return failure(NotSent, "arguments must be object", nil)
	}
	if e = ValidateToolDefinition(t); e != nil {
		return e
	}
	if e = validateArguments(t.InputSchema, args); e != nil {
		return failure(NotSent, "arguments do not satisfy schema", nil)
	}
	return nil
}
