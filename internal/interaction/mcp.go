package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agent/internal/mcp"
	"agent/internal/model"
	"github.com/google/uuid"
)

const MCPWarning = "NON-SANDBOXED: configured MCP server runs as the current OS user, with access to that user's files, network and credentials. Minimal environment is not secret isolation. Cancellation is intent, not proof of stopped effects or descendants."
const MCPEnvironmentPolicy = "complete declared environment only; no inherited host/model/auth variables (Windows may add SystemRoot)"
const MCPLimits = "stdio only; at most 8 configured servers and 16 live processes; 64 tools/server; 64 KiB arguments; 1 MiB frames; 30s maximum request deadline; direct-child cleanup only. HTTP, Tasks and interactive continuation unsupported."

// This separate trusted operator configuration is never read from legacy MCP
// flags, HTTP tool arguments, a discovered annotation, or a model response.
type MCPHostConfig struct {
	Enabled bool              `json:"enabled"`
	Servers []MCPServerConfig `json:"servers"`
}
type MCPServerConfig struct {
	ID            string   `json:"id"`
	Command       string   `json:"command"`
	Args          []string `json:"args"`
	Environment   []string `json:"environment"`
	Directory     string   `json:"directory"`
	TimeoutMillis int      `json:"timeout_ms"`
	ReadOnlyTools []string `json:"read_only_tools"` // Trusted exact names, NOT peer hints.
}
type MCPToolSnapshot struct {
	Name         string
	Alias        string
	SchemaDigest string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	ReadOnly     bool
}
type MCPServerSnapshot struct {
	ID                string
	ConfigFingerprint string
	ProtocolVersion   string
	Generation        string
	Tools             []MCPToolSnapshot
}
type MCPReview struct {
	ServerID          string `json:"server_id"`
	Tool              string `json:"tool"`
	Arguments         string `json:"arguments"`
	SchemaDigest      string `json:"schema_digest"`
	ConfigFingerprint string `json:"config_fingerprint"`
	ProtocolVersion   string `json:"protocol_version"`
	Generation        string `json:"generation"`
	Command           string `json:"command"`
	EnvironmentPolicy string `json:"environment_policy"`
	EffectClass       string `json:"effect_class"`
	Warning           string `json:"warning"`
	UnavailableReason string `json:"unavailable_reason"` // Computed display only; not authority.
}
type MCPServerView struct {
	ID                string `json:"id"`
	State             string `json:"state"`
	Reason            string `json:"reason"`
	ProtocolVersion   string `json:"protocol_version"`
	ConfigFingerprint string `json:"config_fingerprint"`
	Tools             int    `json:"tools"`
}
type MCPStatus struct {
	Enabled bool            `json:"enabled"`
	Warning string          `json:"warning"`
	Limits  string          `json:"limits"`
	Servers []MCPServerView `json:"servers"`
}
type mcpProcess struct {
	client   *mcp.StdioClient
	snapshot MCPServerSnapshot
}
type mcpAdmissionLifetime struct {
	cancel context.CancelFunc
	done   chan struct{}
}
type mcpHost struct {
	admissions  map[string]*mcpAdmissionLifetime // Tombstones prohibit a second launch for the same session.
	reserved    int
	wg          sync.WaitGroup
	closeDone   chan struct{}
	mu          sync.Mutex
	config      MCPHostConfig
	configError error
	processes   map[string]*mcpProcess
	closed      bool
	ctx         context.Context
	cancel      context.CancelFunc
}

func newMCPHost(c MCPHostConfig) *mcpHost {
	raw, _ := json.Marshal(c)
	var copy MCPHostConfig
	_ = json.Unmarshal(raw, &copy)
	ctx, cancel := context.WithCancel(context.Background())
	h := &mcpHost{admissions: map[string]*mcpAdmissionLifetime{}, closeDone: make(chan struct{}), config: copy, processes: map[string]*mcpProcess{}, ctx: ctx, cancel: cancel}
	h.configError = ValidateMCPConfig(copy)
	return h
}

// Validation/inspection do not execute a server or inherit an environment.
func ValidateMCPConfig(c MCPHostConfig) error {
	if !c.Enabled {
		return nil
	}
	if len(c.Servers) == 0 || len(c.Servers) > 8 {
		return fmt.Errorf("MCP requires 1..8 explicitly configured stdio servers")
	}
	ids := map[string]bool{}
	for _, s := range c.Servers {
		if s.ID == "" || len(s.ID) > 80 || ids[s.ID] || strings.ContainsAny(s.ID, "\x00\r\n") || !filepath.IsAbs(s.Command) || s.TimeoutMillis < 1 || s.TimeoutMillis > 30000 || len(s.Args) > 128 || len(s.ReadOnlyTools) > 64 {
			return fmt.Errorf("invalid trusted MCP server configuration")
		}
		ids[s.ID] = true
		if s.Directory != "" && !filepath.IsAbs(s.Directory) {
			return ErrInvalid
		}
		size := len(s.Command) + len(s.Directory)
		for _, a := range s.Args {
			size += len(a)
			if strings.IndexByte(a, 0) >= 0 {
				return ErrInvalid
			}
		}
		env := map[string]bool{}
		for _, v := range s.Environment {
			k, _, ok := strings.Cut(v, "=")
			if !ok || k == "" || env[k] || strings.ContainsAny(v, "\x00\r\n") {
				return ErrInvalid
			}
			env[k] = true
			size += len(v)
		}
		if size > 16<<10 {
			return ErrBudget
		}
	}
	return nil
}
func mcpConfigIdentity(c MCPServerConfig) (string, error) {
	// Trusted operator executable, not a sandbox. Hash the selected regular file;
	// interpreter/shared library dependencies and same-user concurrent replacement
	// are not frozen. Any observed replacement revokes the capability.
	path, e := filepath.EvalSymlinks(c.Command)
	if e != nil {
		return "", ErrCapabilityUnavailable
	}
	info, e := os.Stat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 128<<20 {
		return "", ErrCapabilityUnavailable
	}
	f, e := os.Open(path)
	if e != nil {
		return "", ErrCapabilityUnavailable
	}
	defer f.Close()
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(f, (128<<20)+1))
	if e != nil || n > 128<<20 {
		return "", ErrCapabilityUnavailable
	}
	dir := ""
	if c.Directory != "" {
		var err error
		dir, err = workspaceIdentity(c.Directory)
		if err != nil {
			return "", ErrCapabilityUnavailable
		}
	}
	raw, _ := json.Marshal(struct {
		Config                                  MCPServerConfig
		Path, ExecutableHash, DirectoryIdentity string
	}{c, path, fmt.Sprintf("%x", hash.Sum(nil)), dir})
	return contentHash(raw), nil
}
func (b *WorkspaceBroker) CloseMCP() {
	if b.mcp == nil {
		return
	}
	h := b.mcp
	h.mu.Lock()
	if h.closed {
		done := h.closeDone
		h.mu.Unlock()
		<-done
		return
	}
	h.closed = true
	h.cancel()
	processes := h.processes
	h.processes = map[string]*mcpProcess{}
	h.mu.Unlock()
	// No process operations under the host lock. Cancellation also reaches
	// unpublished admissions, whose owners close/reap before releasing their slot.
	for _, p := range processes {
		_ = p.client.Close()
	}
	h.wg.Wait()
	close(h.closeDone)
}
func mcpKey(session, server string) string { return session + "\x00" + server }
func (h *mcpHost) configured(id string) (MCPServerConfig, bool) {
	for _, s := range h.config.Servers {
		if s.ID == id {
			return s, true
		}
	}
	return MCPServerConfig{}, false
}

// PrepareMCPPolicy is intentional session opt-in by the host/API, before any
// run. It is the ONLY launch path. GET status/review and model calls never launch.
// A new session is required after process death or service restart.
func (b *WorkspaceBroker) PrepareMCPPolicy(ctx context.Context, session string, p WorkspacePolicy) (WorkspacePolicy, error) {
	if e := ctx.Err(); e != nil {
		return p, e
	}
	h := b.mcp
	if h == nil {
		return p, ErrCapabilityUnavailable
	}
	if _, e := workspaceIdentity(p.Root); e != nil {
		return p, e
	}
	h.mu.Lock()
	if h.closed || !h.config.Enabled || h.configError != nil || len(p.MCP) != 0 || session == "" || p.Root == "" {
		h.mu.Unlock()
		return p, ErrCapabilityUnavailable
	}
	if h.admissions[session] != nil {
		h.mu.Unlock()
		return p, ErrConflict
	}
	if len(h.processes)+h.reserved+len(h.config.Servers) > 16 {
		h.mu.Unlock()
		return p, ErrCapacity
	}
	lifeCtx, cancel := context.WithCancel(h.ctx)
	admission := &mcpAdmissionLifetime{cancel: cancel, done: make(chan struct{})}
	h.admissions[session] = admission
	h.reserved += len(h.config.Servers)
	h.wg.Add(1)
	h.mu.Unlock()
	stop := context.AfterFunc(ctx, cancel)
	defer func() {
		stop()
		h.mu.Lock()
		h.reserved -= len(h.config.Servers)
		h.mu.Unlock()
		close(admission.done)
		h.wg.Done()
	}()
	var opened []*mcpProcess
	failed := true
	defer func() {
		if failed {
			cancel()
			for _, pr := range opened {
				_ = pr.client.Close()
			}
		}
	}()
	for _, cfg := range h.config.Servers {
		if e := lifeCtx.Err(); e != nil {
			return p, e
		}
		fingerprint, e := mcpConfigIdentity(cfg)
		if e != nil {
			return p, e
		}
		if e := lifeCtx.Err(); e != nil {
			return p, e
		}
		// Process lifetime is host-owned; request cancellation cannot orphan it.
		client, e := mcp.OpenStdio(lifeCtx, mcp.StdioConfig{Command: cfg.Command, Args: cfg.Args, Env: cfg.Environment, Dir: cfg.Directory, ProbeTimeout: time.Second, RequestTimeout: time.Duration(cfg.TimeoutMillis) * time.Millisecond, Limits: mcp.Limits{MaxTools: 64}})
		if e != nil {
			return p, fmt.Errorf("MCP server startup refused")
		}
		pr := &mcpProcess{client: client, snapshot: MCPServerSnapshot{ID: cfg.ID, ConfigFingerprint: fingerprint, ProtocolVersion: client.Version(), Generation: uuid.NewString()}}
		opened = append(opened, pr)
		list, e := client.ListTools(lifeCtx)
		if e != nil {
			return p, fmt.Errorf("MCP discovery failed")
		}
		for _, t := range list.Tools {
			if e = mcp.ValidateToolDefinition(t); e != nil {
				return p, e
			}
			readonly := false
			for _, name := range cfg.ReadOnlyTools {
				if name == t.Name {
					readonly = true
				}
			}
			pr.snapshot.Tools = append(pr.snapshot.Tools, MCPToolSnapshot{Name: t.Name, Alias: "mcp_" + contentHash([]byte(cfg.ID + "\x00" + t.Name))[:48], SchemaDigest: t.SchemaDigest, InputSchema: t.InputSchema, OutputSchema: t.OutputSchema, ReadOnly: readonly})
		}
		sort.Slice(pr.snapshot.Tools, func(i, j int) bool { return pr.snapshot.Tools[i].Alias < pr.snapshot.Tools[j].Alias })
		p.MCP = append(p.MCP, pr.snapshot)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed || lifeCtx.Err() != nil {
		return p, ErrCapabilityUnavailable
	}
	// Once published the process lifetime belongs to the host, not HTTP request.
	stop()
	for _, pr := range opened {
		h.processes[mcpKey(session, pr.snapshot.ID)] = pr
	}
	failed = false
	return p, nil
}

// ForgetMCPSession cleans failed session admission, never reconnects.
func (b *WorkspaceBroker) ForgetMCPSession(session string) {
	if b.mcp == nil {
		return
	}
	h := b.mcp
	h.mu.Lock()
	admission := h.admissions[session]
	if admission != nil {
		admission.cancel()
	}
	var processes []*mcpProcess
	for k, p := range h.processes {
		if strings.HasPrefix(k, session+"\x00") {
			processes = append(processes, p)
		}
	}
	h.mu.Unlock()
	for _, p := range processes {
		_ = p.client.Close()
	}
	// Keep these slots counted until the children have actually been reaped.
	h.mu.Lock()
	for _, p := range processes {
		delete(h.processes, mcpKey(session, p.snapshot.ID))
	}
	h.mu.Unlock()
	if admission != nil {
		<-admission.done
	}
}
func (b *WorkspaceBroker) MCPStatus(ctx context.Context, session string) MCPStatus {
	var bound []MCPServerSnapshot
	if session != "" {
		if w, e := b.store.Workspace(ctx, session); e == nil {
			bound = w.Policy.MCP
		}
	}
	return b.StagedMCPStatus(session, bound)
}

// StagedMCPStatus projects already-discovered processes without reading a
// partially published workspace or launching anything.
func (b *WorkspaceBroker) StagedMCPStatus(session string, bound []MCPServerSnapshot) MCPStatus {
	out := MCPStatus{Warning: MCPWarning, Limits: MCPLimits, Servers: []MCPServerView{}}
	revoked := func(s MCPServerSnapshot) MCPServerView {
		return MCPServerView{ID: s.ID, State: "disconnected", Reason: "Process/configuration unavailable; old approvals revoked. Deny or cancel pending run and create a new MCP session.", ConfigFingerprint: s.ConfigFingerprint, ProtocolVersion: s.ProtocolVersion, Tools: len(s.Tools)}
	}
	h := b.mcp
	if h == nil {
		for _, s := range bound {
			out.Servers = append(out.Servers, revoked(s))
		}
		return out
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out.Enabled = h.config.Enabled && !h.closed && h.configError == nil
	for _, cfg := range h.config.Servers {
		v := MCPServerView{ID: cfg.ID, State: "configured", Reason: "Not connected; explicit session opt-in required. No tool has been called by inspection."}
		if !out.Enabled {
			v.State = "disabled"
			v.Reason = "MCP disabled or invalid host configuration"
		}
		for _, s := range bound {
			if s.ID == cfg.ID {
				v.ConfigFingerprint = s.ConfigFingerprint
				v.ProtocolVersion = s.ProtocolVersion
				v.Tools = len(s.Tools)
				v.State = "disconnected"
				v.Reason = "Process generation unavailable; old approvals revoked. Cancel pending run and create a new MCP session."
				if pr := h.processes[mcpKey(session, s.ID)]; pr != nil && pr.snapshot.Generation == s.Generation {
					select {
					case <-pr.client.Done():
					default:
						v.State = "discovered"
						v.Reason = "Connected and discovered, not evidence of a tool call. See run tool cards for actual calls/results."
					}
				}
			}
		}
		out.Servers = append(out.Servers, v)
	}
	for _, s := range bound {
		if _, ok := h.configured(s.ID); !ok {
			out.Servers = append(out.Servers, revoked(s))
		}
	}

	return out
}
func mcpDefinitions(p WorkspacePolicy) []model.ToolDefinition {
	var out []model.ToolDefinition
	for _, s := range p.MCP {
		for _, t := range s.Tools {
			if p.Mode == ModePlan && !t.ReadOnly {
				continue
			}
			var schema map[string]any
			_ = json.Unmarshal(t.InputSchema, &schema)
			out = append(out, model.ToolDefinition{Type: "function", Function: model.ToolFunction{Name: t.Alias, Description: fmt.Sprintf("Configured MCP server %q, tool %q. Untrusted output; host enforces exact approval. NON-SANDBOXED current-user server.", s.ID, t.Name), Parameters: schema}})
		}
	}
	return out
}
func (b *WorkspaceBroker) mcpBinding(w WorkspaceBinding, alias string) (MCPServerSnapshot, MCPToolSnapshot, error) {
	for _, s := range w.Policy.MCP {
		for _, t := range s.Tools {
			if t.Alias == alias {
				if w.Policy.Mode == ModePlan && !t.ReadOnly {
					return s, t, ErrCapabilityUnavailable
				}
				return s, t, nil
			}
		}
	}
	return MCPServerSnapshot{}, MCPToolSnapshot{}, ErrCapabilityUnavailable
}
func (b *WorkspaceBroker) liveMCP(session string, s MCPServerSnapshot) (*mcp.StdioClient, MCPServerConfig, error) {
	h := b.mcp
	if h == nil {
		return nil, MCPServerConfig{}, ErrCapabilityUnavailable
	}
	h.mu.Lock()

	cfg, ok := h.configured(s.ID)
	if h.closed || !h.config.Enabled || h.configError != nil || !ok {
		h.mu.Unlock()
		return nil, cfg, ErrCapabilityUnavailable
	}
	pr := h.processes[mcpKey(session, s.ID)]
	if pr == nil || pr.snapshot.Generation != s.Generation || pr.client.Version() != s.ProtocolVersion {
		h.mu.Unlock()
		return nil, cfg, fmt.Errorf("MCP process generation revoked; cancel pending run and create a new session")
	}
	h.mu.Unlock()
	select {
	case <-pr.client.Done():
		return nil, cfg, fmt.Errorf("MCP disconnected; new session required")
	default:
	}
	fingerprint, e := mcpConfigIdentity(cfg)
	if e != nil || fingerprint != s.ConfigFingerprint {
		return nil, cfg, fmt.Errorf("MCP configuration or executable identity changed")
	}
	return pr.client, cfg, nil
}
func mcpDigest(d string, r *MCPReview) string {
	copy := *r
	copy.UnavailableReason = ""
	raw, _ := json.Marshal(copy)
	return contentHash(append([]byte(d+"\x00"), raw...))
}
func (b *WorkspaceBroker) prepareMCP(ctx context.Context, w WorkspaceBinding, p *preparedTool) error {
	s, t, e := b.mcpBinding(w, p.Proposal.Call.Name)
	if e != nil {
		return e
	}
	c, cfg, e := b.liveMCP(w.SessionID, s)
	if e != nil {
		return e
	}
	list, e := c.ListTools(ctx)
	if e != nil {
		return fmt.Errorf("MCP discovery failed before approval")
	}
	found := false
	for _, current := range list.Tools {
		if current.Name == t.Name && current.SchemaDigest == t.SchemaDigest {
			found = true
			if e = mcp.ValidateToolArguments(current, json.RawMessage(p.Proposal.Call.Arguments)); e != nil {
				return e
			}
		}
	}
	if !found {
		return fmt.Errorf("MCP schema revoked; new session required")
	}
	effect := "unknown/writable"
	if t.ReadOnly {
		effect = "host-classified read-only"
	}
	p.MCPReview = &MCPReview{ServerID: s.ID, Tool: t.Name, Arguments: p.Proposal.Call.Arguments, SchemaDigest: t.SchemaDigest, ConfigFingerprint: s.ConfigFingerprint, ProtocolVersion: s.ProtocolVersion, Generation: s.Generation, Command: cfg.Command, EnvironmentPolicy: MCPEnvironmentPolicy, EffectClass: effect, Warning: MCPWarning}
	p.Proposal.RequiresApproval = !t.ReadOnly
	p.Proposal.Digest = mcpDigest(p.Proposal.Digest, p.MCPReview)
	raw, _ := json.Marshal(p.MCPReview)
	p.Summary = string(raw)
	return nil
}
func (b *WorkspaceBroker) executeMCP(ctx context.Context, w WorkspaceBinding, p preparedTool) (ToolResult, error) {
	s, t, e := b.mcpBinding(w, p.Proposal.Call.Name)
	if e != nil {
		return refused(e)
	}
	r := p.MCPReview
	if r == nil || r.ServerID != s.ID || r.Tool != t.Name || r.SchemaDigest != t.SchemaDigest || r.ConfigFingerprint != s.ConfigFingerprint || r.Generation != s.Generation || r.Arguments != p.Proposal.Call.Arguments || p.Proposal.RequiresApproval == t.ReadOnly {
		return refused(ErrConflict)
	}
	c, _, e := b.liveMCP(w.SessionID, s)
	if e != nil {
		return refused(e)
	}
	result, e := c.CallToolPinned(ctx, t.Name, json.RawMessage(p.Proposal.Call.Arguments), t.SchemaDigest)
	var callErr *mcp.CallError
	if e != nil && errors.As(e, &callErr) && callErr.Certainty == mcp.NotSent {
		return refused(e)
	}
	// Even a matching response is not completion proof for unsupported Tasks,
	// input_required or malformed outputs. Never advance/replay those effects.
	unknown := e != nil && !result.IsError
	raw := result.Raw
	// Keep bounded observations explicit, including empty results and certainty.
	data := map[string]any{"server_id": s.ID, "tool": t.Name, "certainty": result.Certainty, "is_error": result.IsError, "trust": "untrusted MCP observation", "outcome": "result", "raw": json.RawMessage(raw)}
	if len(result.Raw) > MaxInlineToolBytes {
		ref, e2 := b.store.putArtifact(context.Background(), w.SessionID, result.Raw)
		if e2 != nil {
			return ToolResult{}, e2
		}
		data["artifact"] = json.RawMessage(ref)
		delete(data, "raw")
	}
	if unknown {
		data["outcome"] = "unknown_requires_reconciliation"
		data["reason"] = "MCP outcome unsupported or uncertain; no automatic replay/continuation"
	} else {
		var v struct {
			Content []any `json:"content"`
		}
		_ = json.Unmarshal(result.Raw, &v)
		if len(v.Content) == 0 {
			data["outcome"] = "empty"
		}
		if result.IsError {
			data["outcome"] = "tool_error"
		}
	}
	content, marshalErr := json.Marshal(data)
	if marshalErr != nil {
		return ToolResult{}, fmt.Errorf("MCP observation encoding failed after call")
	}
	if len(content) > MaxInlineToolBytes {
		ref, err := b.store.putArtifact(context.Background(), w.SessionID, result.Raw)
		if err != nil {
			return ToolResult{}, err
		}
		delete(data, "raw")
		data["artifact"] = json.RawMessage(ref)
		content, marshalErr = json.Marshal(data)
		if marshalErr != nil || len(content) > MaxInlineToolBytes {
			return ToolResult{}, fmt.Errorf("MCP observation exceeds bound after call")
		}
	}
	out := ToolResult{Content: string(content), IsError: result.IsError || unknown}
	if unknown {
		return out, fmt.Errorf("MCP effect requires reconciliation")
	}
	return out, nil
}
