package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxInteractionExecutors = 4
const maxInteractionWorkers = 20 // Bounded total, including executors and pending drains.

const InteractionProtocolFlavor = "personal-agent-rebuild-v1"

type InteractionOptions struct {
	Token, Host, Workspace, DefaultModel string
	OperatorModels                       []OperatorRecord          // Sanitized startup-only model inventory.
	MCP                                  interaction.MCPHostConfig // Separate default-off trusted stdio configuration.
	NonSandboxExec                       bool                      // Explicit trusted host opt-in; not an HTTP setting.
	WebAssets                            fs.FS                     // Optional prebuilt trusted assets; no runtime filesystem access.
	Resolve                              ProviderResolver
}
type execution struct {
	engine *interaction.Engine
	again  bool
}
type InteractionService struct {
	runtime      *InteractionRuntime
	options      InteractionOptions
	store        *interaction.Store
	broker       *interaction.WorkspaceBroker
	mu           sync.Mutex
	preparations int // bounded staged session admissions, not run executors
	workers      map[string]*execution
	wg           sync.WaitGroup
	ctx          context.Context
	cancel       context.CancelFunc
	closed       bool
	closeDone    chan struct{}
	streams      chan struct{}
}
type offlineProvider struct{}

func (offlineProvider) Stream(context.Context, model.TurnRequest, func(model.ModelEvent) error) (model.TurnResult, error) {
	return model.TurnResult{}, errors.New("offline provider")
}
func NewInteractionService(rt *InteractionRuntime, o InteractionOptions) (*InteractionService, error) {
	host, _, e := net.SplitHostPort(o.Host)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || strings.TrimSpace(o.Token) == "" || o.Resolve == nil || rt == nil || rt.lock == nil || rt.DB == nil {
		return nil, fmt.Errorf("locked runtime, loopback host, auth and resolver required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &InteractionService{runtime: rt, options: o, store: interaction.NewStore(rt.DB.SQL), ctx: ctx, cancel: cancel, workers: map[string]*execution{}, closeDone: make(chan struct{}), streams: make(chan struct{}, 16)}
	s.broker = interaction.NewWorkspaceBrokerWithOptions(s.store, interaction.WorkspaceBrokerOptions{NonSandboxExec: o.NonSandboxExec, MCP: o.MCP})
	if o.NonSandboxExec && (o.Workspace == "" || !s.broker.Capabilities().Exec.Enabled) {
		cancel()
		return nil, fmt.Errorf("NON-SANDBOXED exec requires an explicit workspace and a supported native platform: %s", s.broker.Capabilities().Exec.Reason)
	}
	if e = interaction.ValidateMCPConfig(o.MCP); e != nil || (o.MCP.Enabled && (o.Workspace == "" || !s.broker.Capabilities().Files.Enabled)) {
		cancel()
		s.broker.CloseMCP()
		return nil, fmt.Errorf("MCP requires valid trusted configuration and explicit workspace")
	}
	// Interrupted admission is never resumed or automatically relaunched.
	if _, e = rt.DB.SQL.ExecContext(ctx, "UPDATE interaction_mcp_admissions SET state='revoked' WHERE state<>'complete'"); e != nil {
		cancel()
		return nil, e
	}
	// Exclusive process lock is held. Recovery never calls a provider or executes tools.
	rows, e := rt.DB.SQL.Query("SELECT r.id FROM interaction_runs r LEFT JOIN interaction_engine e ON r.id=e.run_id WHERE r.state='running' AND (e.run_id IS NOT NULL OR r.execution_kind='skill_readonly_v1')")
	if e != nil {
		cancel()
		return nil, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			cancel()
			return nil, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		cancel()
		return nil, e
	}
	engine, _ := interaction.NewEngine(s.store, offlineProvider{}, s.broker, interaction.EngineOptions{})
	for _, id := range ids {
		if e = engine.Recover(ctx, id); e != nil && !errors.Is(e, interaction.ErrReconciliation) {
			cancel()
			return nil, e
		}
	}
	// Recover never executes. Preserve a Job's still-pending exact approval after
	// offline reconciliation, but never revive an approved/consumed/unknown action.
	for _, id := range ids {
		if _, err := s.store.JobForRun(ctx, id); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			cancel()
			return nil, err
		}
		cp, err := s.store.EngineCheckpoint(ctx, id)
		if err != nil {
			cancel()
			return nil, err
		}
		if cp.Phase != interaction.PhaseRunning || len(cp.Pending) != 1 || !cp.Pending[0].RequiresApproval {
			continue
		}
		a, err := s.store.GetApproval(ctx, cp.Pending[0].ID)
		if err != nil {
			cancel()
			return nil, err
		}
		if a.RunID == id && a.ActionDigest == cp.Pending[0].Digest && a.Decision == "pending" && a.ConsumedAt == nil {
			if _, err = s.runtime.DB.SQL.ExecContext(ctx, "UPDATE interaction_engine SET phase='waiting_approval' WHERE run_id=? AND owner='' AND cancel_requested=0", id); err != nil {
				cancel()
				return nil, err
			}
		}
	}

	s.mu.Lock()
	s.kickQueues()
	s.kickJobs()
	s.mu.Unlock()
	return s, nil
}
func (s *InteractionService) Close() {
	s.mu.Lock()
	if s.closed {
		done := s.closeDone
		s.mu.Unlock()
		<-done
		return
	}
	s.closed = true
	for id, w := range s.workers {
		_ = w.engine.Cancel(context.Background(), id)
	}
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
	s.broker.CloseMCP()
	close(s.closeDone)
}
func wireJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func wireError(w http.ResponseWriter, status int, code string) {
	wireJSON(w, status, map[string]any{"schema_version": 1, "error": map[string]string{"code": code, "message": code}})
}
func (s *InteractionService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; frame-ancestors 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() || r.Host != s.options.Host || r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+s.options.Host || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		wireError(w, 403, "forbidden_origin")
		return
	}
	// Use the same path spelling for admission query rules and dispatch. A
	// leading/trailing slash accepted below must not bypass a mutating guard.
	routePath := strings.Trim(r.URL.Path, "/")
	if r.URL.RawQuery != "" && ((r.Method == "POST" && routePath == "api/v1/sessions") || strings.HasPrefix(r.URL.Path, "/api/v1/admissions/")) {
		wireError(w, 400, "invalid_query")
		return
	}
	catalogPath := routePath == "api/v1/catalog/projects"
	skillPath := skillRunRoute(strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")) || skillRoute(strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")) && strings.HasPrefix(r.URL.Path, "/api/v1/")
	knowledgePath := knowledgeRoute(strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")) && strings.HasPrefix(r.URL.Path, "/api/v1/")
	operatorPath := operatorRoute(strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/")) && strings.HasPrefix(r.URL.Path, "/api/v1/")
	if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
		wireError(w, 400, "invalid_query")
		return
	}
	if operatorPath && !operatorQuery(r.URL.Query()) {
		wireError(w, 400, "invalid_query")
		return
	}
	for key := range r.URL.Query() {
		if !catalogPath && !skillPath && !knowledgePath && !operatorPath && key != "after" && key != "before" {
			wireError(w, 400, "invalid_query")
			return
		}
	}
	if s.options.WebAssets != nil && r.Method == http.MethodGet && !strings.HasPrefix(r.URL.Path, "/api/") {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if !fs.ValidPath(name) || (name != "index.html" && !strings.HasPrefix(name, "assets/")) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(s.options.WebAssets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		_, _ = w.Write(data)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.options.Token)) != 1 {
		wireError(w, 401, "unauthorized")
		return
	}
	parts := strings.Split(routePath, "/")
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
		wireError(w, 404, "not_found")
		return
	}
	if r.Method == "GET" {
		s.read(w, r, parts[2:])
		return
	}
	if r.Method != "POST" {
		wireError(w, 405, "method_not_allowed")
		return
	}
	if r.Header.Get("X-Agent-Protocol") != InteractionProtocolFlavor {
		wireError(w, 426, "incompatible_protocol")
		return
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		wireError(w, 415, "json_required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		wireError(w, 503, "stopping")
		return
	}
	if s.skillRuns(w, r, parts[2:]) {
		return
	}
	if s.skills(w, r, parts[2:]) {
		return
	}
	if s.knowledge(w, r, parts[2:]) {
		return
	}
	if s.operatorActions(w, r, parts[2:]) {
		return
	}
	s.write(w, r, parts[2:])
}
func (s *InteractionService) read(w http.ResponseWriter, r *http.Request, p []string) {
	ctx := r.Context()
	if len(p) == 2 && p[0] == "catalog" && p[1] == "projects" {
		s.readActiveProjects(w, r)
		return
	}
	if s.skillRuns(w, r, p) {
		return
	}
	if s.skills(w, r, p) {
		return
	}
	if s.knowledge(w, r, p) {
		return
	}
	if s.operatorActions(w, r, p) {
		return
	}
	if s.readOperator(w, r, p) {
		return
	}
	if s.readJobs(w, r, p) {
		return
	}
	if s.readQueue(w, r, p) {
		return
	}
	if len(p) >= 1 && p[0] == "admissions" && strings.HasPrefix(r.URL.EscapedPath(), "/api/v1/admissions/") {
		requestID, e := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/admissions/"))
		if e != nil || requestID == "" || len(requestID) > 128 || !utf8.ValidString(requestID) {
			wireError(w, 400, "invalid_request")
			return
		}
		a, e := s.admission(ctx, requestID)
		if errors.Is(e, sql.ErrNoRows) {
			wireError(w, 404, "admission_not_found")
		} else if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, a)
		}
		return
	}
	if len(p) == 1 && p[0] == "protocol" {
		if r.URL.RawQuery != "" {
			wireError(w, 400, "invalid_query")
			return
		}
		wireJSON(w, 200, map[string]any{"schema_version": 1, "protocol": InteractionProtocolFlavor})
		return
	}
	if len(p) == 1 && p[0] == "status" {
		capability, reason := "unavailable", "provider_unavailable"
		if selected, err := s.options.Resolve(ctx, s.options.DefaultModel); err == nil && selected.validate() == nil {
			capability = selected.toolCapability()
			reason = selected.ToolCapabilityReason
			if reason == "" {
				reason = "No production synthetic tool evidence supplied."
			}
		}

		wireJSON(w, 200, map[string]any{"schema_version": 1, "preview": "rebuilt preview", "protocol": InteractionProtocolFlavor, "chat_only": true, "tool_capability": capability, "tool_capability_reason": reason, "workspace": s.broker.Capabilities(), "mcp": s.broker.MCPStatus(ctx, ""), "commands": []string{"manage", "help", "model", "status", "context", "cost", "permissions", "history", "resume", "cancel", "mcp", "branch", "clear", "rewind", "compact", "queue", "jobs"}})
		return
	}
	if len(p) == 1 && p[0] == "sessions" {
		rows, e := s.runtime.DB.SQL.QueryContext(ctx, "SELECT session_id FROM interaction_api_sessions WHERE session_id NOT IN (SELECT session_id FROM interaction_jobs) AND (?='' OR rowid<(SELECT rowid FROM interaction_api_sessions WHERE session_id=?)) ORDER BY rowid DESC LIMIT 100", r.URL.Query().Get("before"), r.URL.Query().Get("before"))
		if e != nil {
			wireError(w, 500, "storage_error")
			return
		}
		ids := []string{}
		for rows.Next() {
			var id string
			rows.Scan(&id)
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			wireError(w, 500, "storage_error")
			return
		}
		list := []SessionView{}
		for _, id := range ids {
			v, _, e := s.session(ctx, id)
			if e != nil {
				wireError(w, 500, "storage_error")
				return
			}
			list = append(list, v)
		}
		wireJSON(w, 200, list)
		return
	}
	if len(p) == 4 && p[0] == "sessions" && p[2] == "operations" {
		if _, _, e := s.session(ctx, p[1]); e != nil {
			wireError(w, 404, "session_not_found")
			return
		}
		v, e := s.store.ReadSessionOperation(ctx, p[1], p[3])
		if errors.Is(e, sql.ErrNoRows) {
			wireError(w, 404, "operation_not_recorded")
			return
		}
		if e != nil {
			wireError(w, 500, "storage_error")
			return
		}
		wireJSON(w, 200, v)
		return
	}
	if len(p) == 3 && p[0] == "sessions" {
		if _, _, e := s.session(ctx, p[1]); e != nil {
			wireError(w, 404, "session_not_found")
			return
		}
		switch p[2] {
		case "edit-checkpoints":
			v, e := s.store.EditCheckpoints(ctx, p[1])
			if e != nil {
				wireError(w, 409, "checkpoint_inventory_blocked")
				return
			}
			wireJSON(w, 200, v)
			return
		case "context-view":
			v, e := s.store.SessionContextView(ctx, p[1])
			if e != nil {
				wireError(w, 409, "context_view_blocked")
				return
			}
			wireJSON(w, 200, v)
			return
		case "mcp":
			wireJSON(w, 200, s.broker.MCPStatus(ctx, p[1]))
			return
		case "snapshot", "history":
			before := int64(0)
			var e error
			if raw := r.URL.Query().Get("before"); raw != "" {
				before, e = strconv.ParseInt(raw, 10, 64)
				if e != nil || before <= 0 || p[2] != "history" {
					wireError(w, 400, "invalid_cursor")
					return
				}
			}
			v, e := s.snapshotBefore(ctx, p[1], before)
			if e != nil {
				wireError(w, 500, "storage_error")
				return
			}
			wireJSON(w, 200, v)
			return
		case "events":
			s.events(w, r, p[1])
			return
		case "context":
			c, e := interaction.NewContextAssembler(s.store).Build(ctx, p[1], interaction.TokenBudget{Total: 32000, Output: 2048})
			if e != nil {
				wireError(w, 409, "context_blocked")
				return
			}
			wireJSON(w, 200, map[string]any{"estimated_input": c.EstimatedInput, "reserved_output": c.ReservedOutput, "estimator": c.Estimator, "exclusions": c.Exclusions, "cost": nil})
			return
		}
	}
	if len(p) == 3 && p[0] == "approvals" && p[2] == "review" {
		a, e := s.store.GetApproval(ctx, p[1])
		if e != nil {
			wireError(w, 404, "approval_not_found")
			return
		}
		run, e := s.store.GetRun(ctx, a.RunID)
		if e != nil {
			wireError(w, 404, "run_not_found")
			return
		}
		if _, _, e = s.session(ctx, run.SessionID); e != nil {
			wireError(w, 404, "session_not_found")
			return
		}
		v, e := s.broker.ApprovalReview(ctx, run.SessionID, p[1])
		if e != nil {
			wireError(w, 409, "review_unavailable")
			return
		}
		wireJSON(w, 200, v)
		return
	}
	wireError(w, 404, "not_found")
}

type writeInput struct {
	CatalogProjectID string `json:"catalog_project_id,omitempty"`
	Effects          bool   `json:"effects,omitempty"`
	OriginRunID      string `json:"origin_run_id,omitempty"`
	ApprovalID       string `json:"approval_id,omitempty"`
	CheckpointID     string `json:"checkpoint_id,omitempty"`
	BoundaryRunID    string `json:"boundary_run_id,omitempty"`
	ExpectedCursor   *int64 `json:"expected_cursor,omitempty"`
	Selection        string `json:"selection,omitempty"`
	RequestID        string `json:"request_id"`
	MCP              bool   `json:"mcp,omitempty"`
	Mode             string `json:"mode,omitempty"`
	ModelID          string `json:"model_id,omitempty"`
	Content          string `json:"content,omitempty"`
	Digest           string `json:"digest,omitempty"`
	Decision         string `json:"decision,omitempty"`
}
type actionResult struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
}

func (s *InteractionService) write(w http.ResponseWriter, r *http.Request, p []string) {
	ctx := r.Context()
	rawBody, e := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if e != nil || len(rawBody) > 1<<20 {
		wireError(w, 400, "invalid_request")
		return
	}
	// Flat request DTO: reject duplicates and fields not meaningful for this route.
	allowed := map[string]bool{"request_id": true}
	switch {
	case len(p) == 3 && p[0] == "sessions" && p[2] == "jobs":
		allowed["content"] = true
		allowed["effects"] = true
		allowed["origin_run_id"] = true
	case len(p) == 5 && p[0] == "sessions" && p[2] == "jobs" && p[4] == "decision":
		allowed["approval_id"] = true
		allowed["digest"] = true
		allowed["decision"] = true
	case len(p) == 1 && p[0] == "sessions":
		allowed["mode"] = true
		allowed["model_id"] = true
		allowed["catalog_project_id"] = true
		allowed["mcp"] = true
	case len(p) == 3 && p[0] == "sessions" && (p[2] == "branch" || p[2] == "clear" || p[2] == "rewind" || p[2] == "compact" || p[2] == "file-undo"):
		allowed["expected_cursor"] = true
		if p[2] == "file-undo" {
			allowed["checkpoint_id"] = true
		}
		if p[2] == "branch" || p[2] == "rewind" {
			allowed["boundary_run_id"] = true
		}
		if p[2] == "compact" {
			allowed["selection"] = true
		}
	case len(p) == 3 && p[0] == "sessions" && (p[2] == "messages" || p[2] == "queue"):
		allowed["content"] = true
	case len(p) == 3 && p[0] == "approvals" && p[2] == "decision":
		allowed["digest"] = true
		allowed["decision"] = true
	}
	d := json.NewDecoder(bytes.NewReader(rawBody))
	tok, e := d.Token()
	if e != nil || tok != json.Delim('{') {
		wireError(w, 400, "invalid_request")
		return
	}
	seen := map[string]bool{}
	for d.More() {
		tok, e = d.Token()
		key, ok := tok.(string)
		if e != nil || !ok || seen[key] || !allowed[key] {
			wireError(w, 400, "invalid_request")
			return
		}
		seen[key] = true
		if key == "expected_cursor" {
			var value json.Number
			if d.Decode(&value) != nil {
				wireError(w, 400, "invalid_request")
				return
			}
			n, e := value.Int64()
			if e != nil || n < 0 || n > 9007199254740991 {
				wireError(w, 400, "invalid_request")
				return
			}
			continue
		}
		if key == "mcp" || key == "effects" {
			var value json.RawMessage
			if d.Decode(&value) != nil || (string(value) != "true" && string(value) != "false") {
				wireError(w, 400, "invalid_request")
				return
			}
			continue
		}
		var encoded json.RawMessage
		var value string
		if d.Decode(&encoded) != nil || len(encoded) == 0 || encoded[0] != '"' || json.Unmarshal(encoded, &value) != nil || (key == "catalog_project_id" && (!exactJSONStringUnicode(encoded) || len(value) > 512 || strings.ContainsRune(value, 0))) {
			wireError(w, 400, "invalid_request")
			return
		}
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF {
		wireError(w, 400, "invalid_request")
		return
	}
	var in writeInput
	if json.Unmarshal(rawBody, &in) != nil || in.RequestID == "" || len(in.RequestID) > 128 {
		wireError(w, 400, "invalid_request")
		return
	}
	raw, _ := json.Marshal(in)
	fingerprint := fmt.Sprintf("%x", sha256.Sum256(append([]byte(r.URL.Path), raw...)))
	var oldFP, result string
	e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT fingerprint,result FROM interaction_api_actions WHERE request_id=?", in.RequestID).Scan(&oldFP, &result)
	if e == nil {
		if oldFP != fingerprint {
			wireError(w, 409, "request_id_conflict")
			return
		}
		var a actionResult
		if json.Unmarshal([]byte(result), &a) != nil {
			wireError(w, 500, "storage_error")
			return
		}
		if len(p) == 1 && p[0] == "sessions" && (a.Status != 201 || s.validateSessionReplay(ctx, in, a.Body) != nil) {
			wireError(w, 500, "storage_error")
			return
		}
		wireJSON(w, a.Status, a.Body)
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		wireError(w, 500, "storage_error")
		return
	}
	if s.rejectAdmissionReplay(w, ctx, in.RequestID, fingerprint) {
		return
	}
	var queueCount int
	if e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM interaction_queue_operations WHERE request_id=?", in.RequestID).Scan(&queueCount); e != nil {
		wireError(w, 500, "storage_error")
		return
	}
	if queueCount > 0 && !queueRoute(p) {
		wireError(w, 409, "request_id_conflict")
		return
	}
	var jobCount int
	if e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM interaction_job_operations WHERE request_id=?", in.RequestID).Scan(&jobCount); e != nil {
		wireError(w, 500, "storage_error")
		return
	}
	if jobCount > 0 && !jobRoute(p) {
		wireError(w, 409, "request_id_conflict")
		return
	}
	if len(p) >= 2 && p[0] == "sessions" {
		isJob, err := s.store.IsJobSession(ctx, p[1])
		if err != nil {
			wireError(w, 500, "storage_error")
			return
		}
		if isJob {
			wireError(w, 409, "immutable_job_context")
			return
		}
	}
	// Also block old/partially acknowledged session mappings written before the
	// admission ledger existed. They cannot safely acquire a fresh generation.
	var mapped string
	e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT session_id FROM interaction_api_sessions WHERE request_id=?", in.RequestID).Scan(&mapped)
	if e == nil {
		wireError(w, 409, "request_id_conflict")
		return
	}
	if !errors.Is(e, sql.ErrNoRows) {
		wireError(w, 500, "storage_error")
		return
	}
	var sessionOpCount int
	if e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM interaction_session_operations WHERE request_id=?", in.RequestID).Scan(&sessionOpCount); e != nil {
		wireError(w, 500, "storage_error")
		return
	}
	isSessionOp := len(p) == 3 && p[0] == "sessions" && (p[2] == "branch" || p[2] == "clear" || p[2] == "rewind" || p[2] == "compact" || p[2] == "file-undo")
	if sessionOpCount > 0 && !isSessionOp {
		wireError(w, 409, "request_id_conflict")
		return
	}
	if jobRoute(p) {
		var n int
		if e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM interaction_runs WHERE request_id=?", in.RequestID).Scan(&n); e != nil {
			wireError(w, 500, "storage_error")
			return
		}
		if n > 0 {
			wireError(w, 409, "request_id_conflict")
			return
		}
		s.writeJobs(ctx, w, p, in)
		return
	}
	if queueRoute(p) {
		// Never adopt an ordinary run's identity after an uncertain HTTP ack.
		var n int
		if e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT count(*) FROM interaction_runs WHERE request_id=?", in.RequestID).Scan(&n); e != nil {
			wireError(w, 500, "storage_error")
			return
		}
		if n > 0 {
			wireError(w, 409, "request_id_conflict")
			return
		}
		if _, _, e = s.session(ctx, p[1]); e != nil {
			if errors.Is(e, sql.ErrNoRows) && queueCount == 0 {
				wireError(w, 404, "queue_operation_rejected")
			} else {
				wireError(w, 500, "outcome_unknown_check_queue")
			}
			return
		}
		s.writeQueue(ctx, w, p, in)
		return
	}
	var body any
	status := 202
	var startID string
	var engine *interaction.Engine
	switch {
	case isSessionOp:
		if in.ExpectedCursor == nil {
			wireError(w, 400, "explicit_cursor_required")
			return
		}
		if _, _, e := s.session(ctx, p[1]); e != nil {
			wireError(w, 404, "session_not_found")
			return
		}
		op := interaction.SessionOperation{RequestID: in.RequestID, Kind: p[2], BoundaryRunID: in.BoundaryRunID, ExpectedCursor: *in.ExpectedCursor}
		if p[2] == "file-undo" {
			if len(s.workers) >= maxInteractionExecutors {
				wireError(w, 503, "service_capacity")
				return
			}
			sel, err := s.resolveSession(ctx, p[1])
			if err != nil {
				s.providerError(w, err)
				return
			}
			if !sel.ToolsSupported {
				wireError(w, 409, "context_operation_rejected")
				return
			}
			op.Kind = "file_undo"
			op.BoundaryRunID = in.CheckpointID
			run, err := s.store.UndoEdit(ctx, p[1], op)
			if err != nil {
				sessionOperationError(w, err)
				return
			}
			engine, err = s.engine(ctx, run, sel)
			if err != nil {
				wireError(w, 500, "outcome_unknown_check_snapshot")
				return
			}
			if run.State == interaction.RunRunning {
				startID = run.ID
			}
			body = map[string]string{"run_id": run.ID, "state": string(run.State)}
		} else if p[2] == "compact" {
			if in.Selection != "" && in.Selection != "compacted" && in.Selection != "original" {
				wireError(w, 400, "invalid_selection")
				return
			}
			if in.Selection == "original" {
				op.Kind = "original"
			}
			v, e := s.store.CompactSession(ctx, p[1], op)
			if e != nil {
				sessionOperationError(w, e)
				return
			}
			body = v
			status = 200
		} else {
			v, e := s.store.BranchSession(ctx, p[1], op)
			if e != nil {
				sessionOperationError(w, e)
				return
			}
			body, _, e = s.session(ctx, v.SessionID)
			if e != nil {
				wireError(w, 500, "storage_error")
				return
			}
			status = 201
		}
	case len(p) == 1 && p[0] == "sessions":
		if in.Mode != "chat_only" && in.Mode != "default" && in.Mode != "plan" && in.Mode != "acceptEdits" {
			wireError(w, 400, "explicit_mode_required")
			return
		}
		if _, err := interaction.ActiveProjectLabel(ctx, s.runtime.DB.SQL, in.CatalogProjectID); err != nil {
			wireError(w, 409, "project_label_unavailable")
			return
		}
		modelID := in.ModelID
		if modelID == "" {
			modelID = s.options.DefaultModel
		}
		s.createAdmittedSession(w, r, in, fingerprint, modelID)
		return
	case len(p) == 3 && p[0] == "sessions" && p[2] == "messages":
		if strings.TrimSpace(in.Content) == "" || len(in.Content) > 64<<10 {
			wireError(w, 400, "invalid_content")
			return
		}
		if len(s.workers) >= maxInteractionExecutors {
			wireError(w, 503, "service_capacity")
			return
		}
		sel, e := s.resolveSession(ctx, p[1])
		if e != nil {
			s.providerError(w, e)
			return
		}
		run, e := s.store.CreateRun(ctx, p[1], in.RequestID, interaction.Message{Content: in.Content})
		if e != nil {
			wireError(w, 409, "session_busy")
			return
		}
		if run.Message.Content != in.Content {
			wireError(w, 409, "request_id_conflict")
			return
		}
		engine, e = s.engine(ctx, run, sel)
		if e != nil {
			_, _ = s.store.Transition(ctx, run.ID, run.Version, interaction.RunFailed)
			wireError(w, 409, "context_blocked")
			return
		}
		startID = run.ID
		body = map[string]string{"run_id": run.ID, "state": string(run.State)}
	case len(p) == 3 && p[0] == "runs" && (p[2] == "cancel" || p[2] == "resume"):
		run, e := s.store.GetRun(ctx, p[1])
		if e != nil {
			wireError(w, 404, "run_not_found")
			return
		}
		isSkill, routeErr := s.store.IsSkillRun(ctx, run.ID)
		if routeErr != nil {
			wireError(w, 409, "run_identity_blocked")
			return
		}
		if isSkill {
			var sessionID string
			if e = s.runtime.DB.SQL.QueryRowContext(ctx, `SELECT session_id FROM interaction_api_sessions WHERE session_id=?`, run.SessionID).Scan(&sessionID); e != nil {
				wireError(w, 404, "session_not_found")
				return
			}
		} else if _, _, e = s.session(ctx, run.SessionID); e != nil {
			wireError(w, 404, "session_not_found")
			return
		}
		if _, err := s.store.JobForRun(ctx, run.ID); err == nil {
			wireError(w, 409, "use_job_controls")
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			wireError(w, 500, "storage_error")
			return
		}
		if s.workers[run.ID] == nil && len(s.workers) >= maxInteractionWorkers {
			wireError(w, 503, "service_capacity")
			return
		}
		if isSkill {
			if e = s.store.ReserveSkillControl(ctx, run.ID, in.RequestID, fingerprint); e != nil {
				if errors.Is(e, interaction.ErrBudget) {
					wireError(w, 409, "budget_exhausted")
				} else if errors.Is(e, interaction.ErrReconciliation) {
					wireError(w, 500, "outcome_unknown_check_snapshot")
				} else {
					wireError(w, 409, "request_id_conflict")
				}
				return
			}
		}
		if p[2] == "cancel" {
			engine, _ = interaction.NewEngine(s.store, offlineProvider{}, s.broker, interaction.EngineOptions{})
			if run.State == interaction.RunRunning {
				if e = engine.Cancel(ctx, run.ID); e != nil {
					wireError(w, 409, "cancel_blocked")
					return
				}
				startID = run.ID
			}
			body = map[string]string{"run_id": run.ID, "state": string(run.State), "phase": "cancelling"}
		} else {
			var sel ProviderSelection
			isSkill, routeErr := s.store.IsSkillRun(ctx, run.ID)
			if routeErr != nil {
				wireError(w, 409, "resume_blocked")
				return
			}
			if !isSkill {
				sel, e = s.resolveSession(ctx, run.SessionID)
				if e != nil {
					s.providerError(w, e)
					return
				}
			}
			if run.State != interaction.RunRunning {
				wireError(w, 409, "run_terminal")
				return
			}
			engine, e = s.engine(ctx, run, sel)
			if e != nil {
				wireError(w, 409, "resume_blocked")
				return
			}
			startID = run.ID
			body = map[string]string{"run_id": run.ID, "state": "running"}
		}
	case len(p) == 3 && p[0] == "approvals" && p[2] == "decision":
		a, e := s.store.GetApproval(ctx, p[1])
		if e != nil {
			wireError(w, 404, "approval_not_found")
			return
		}
		run, e := s.store.GetRun(ctx, a.RunID)
		if e != nil {
			wireError(w, 404, "run_not_found")
			return
		}
		if _, err := s.store.JobForRun(ctx, run.ID); err == nil {
			wireError(w, 409, "use_job_controls")
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			wireError(w, 500, "storage_error")
			return
		}
		if s.workers[run.ID] == nil && len(s.workers) >= maxInteractionWorkers {
			wireError(w, 503, "service_capacity")
			return
		}
		sel, e := s.resolveSession(ctx, run.SessionID)
		if e != nil {
			s.providerError(w, e)
			return
		}
		engine, e = s.engine(ctx, run, sel)
		if e != nil {
			wireError(w, 409, "resume_blocked")
			return
		}
		if in.Decision == "approved" {
			review, err := s.broker.ApprovalReview(ctx, run.SessionID, p[1])
			if err == nil && review.MCP != nil && review.MCP.UnavailableReason != "" {
				wireError(w, 409, "mcp_approval_revoked")
				return
			}
		}
		if e = s.store.DecideApproval(ctx, p[1], in.Digest, in.Decision); e != nil {
			wireError(w, 409, "approval_conflict")
			return
		}
		startID = run.ID
		body = map[string]string{"run_id": run.ID, "decision": in.Decision}
	default:
		wireError(w, 404, "not_found")
		return
	}
	b, _ := json.Marshal(body)
	record, _ := json.Marshal(actionResult{Status: status, Body: b})
	_, e = s.runtime.DB.SQL.ExecContext(ctx, "INSERT INTO interaction_api_actions(request_id,fingerprint,result) VALUES(?,?,?)", in.RequestID, fingerprint, string(record))
	if e != nil {
		wireError(w, 500, "outcome_unknown_check_snapshot")
		return
	}
	if startID != "" {
		s.start(startID, engine)
	}
	wireJSON(w, status, body)
}

var errProviderChanged = errors.New("provider_changed")

func (s *InteractionService) resolveSession(ctx context.Context, id string) (ProviderSelection, error) {
	view, binding, e := s.session(ctx, id)
	if e != nil {
		return ProviderSelection{}, e
	}
	p, e := s.options.Resolve(ctx, binding.ModelID)
	if e != nil || p.validate() != nil {
		return ProviderSelection{}, errors.New("provider_unavailable")
	}
	p = p.forMode(view.Mode)
	if p.Binding() != binding.Fingerprint {
		// Older chat-only sessions saved the host tool flag, despite having no
		// tools. Match that historical nonsecret identity without granting it.
		legacy := p
		legacy.ToolsSupported = binding.Tools
		if view.Mode != "chat_only" || legacy.Binding() != binding.Fingerprint {
			return ProviderSelection{}, errProviderChanged
		}
	}
	return p, nil
}
func (s *InteractionService) providerError(w http.ResponseWriter, e error) {
	if errors.Is(e, sql.ErrNoRows) {
		wireError(w, 404, "session_not_found")
	} else if errors.Is(e, errProviderChanged) {
		wireError(w, 409, "provider_changed")
	} else {
		wireError(w, 409, "provider_unavailable")
	}
}
func (s *InteractionService) engine(ctx context.Context, r interaction.Run, p ProviderSelection) (*interaction.Engine, error) {
	isSkill, routeErr := s.store.IsSkillRun(ctx, r.ID)
	if routeErr != nil {
		return nil, routeErr
	}
	if isSkill {
		return interaction.NewSkillRunEngine(s.store, r.ID)
	}

	return s.modelEngine(ctx, r, p)
}

// Context preflight can precede run admission; execution routing cannot.
func (s *InteractionService) modelEngine(ctx context.Context, r interaction.Run, p ProviderSelection) (*interaction.Engine, error) {
	request := model.TurnRequest{ChatRequest: model.ChatRequest{Model: p.safeModel()}}
	cp, e := s.store.EngineCheckpoint(ctx, r.ID)
	if e == nil {
		if cp.Action == "file_undo" {
			if !p.ToolsSupported {
				return nil, interaction.ErrCapabilityUnavailable
			}
			return interaction.NewEngine(s.store, offlineProvider{}, s.broker, interaction.EngineOptions{})
		}
		if cp.Action != "" {
			return nil, interaction.ErrReconciliation
		}
		if cp.Request.Model.ID != p.Model.ID {
			return nil, errProviderChanged
		}
	} else if !errors.Is(e, sql.ErrNoRows) {
		return nil, e
	} else {
		total := p.Model.ContextWindow
		if total <= 0 || total > 32000 {
			total = 32000
		}
		c, e := interaction.NewContextAssembler(s.store).Build(ctx, r.SessionID, interaction.TokenBudget{Total: total, Output: 2048})
		if e != nil {
			return nil, e
		}
		request.Messages = c.Messages
		if p.ToolsSupported {
			request.Tools = c.Tools
			if !s.broker.Capabilities().Exec.Enabled {
				var filtered []model.ToolDefinition
				for _, tool := range request.Tools {
					if tool.Function.Name != "exec" {
						filtered = append(filtered, tool)
					}
				}
				request.Tools = filtered
			}
		}
	}
	var broker interaction.ToolBroker
	if p.ToolsSupported {
		broker = s.broker
	}
	options := interaction.EngineOptions{Request: request}
	provider := p.Provider
	if j, err := s.store.JobForRun(ctx, r.ID); err == nil {
		if err = s.validateJob(ctx, j); err != nil {
			return nil, err
		}
		provider = jobProvider{s: s, job: j, provider: provider}
		if broker != nil {
			broker = interaction.NewJobBroker(s.broker, j.RunID, func(ctx context.Context) error { return s.validateJob(ctx, j) })
		}
		options.Budgets = interaction.JobBudgets()
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return interaction.NewEngine(s.store, provider, broker, options)
}

// Called under mu after admission reserved a bounded worker slot. Only ErrCapacity
// is retried: it attests failure before ownership/model/tool execution. Owned or
// reconciliation failures must never become blind retries.
func (s *InteractionService) start(id string, e *interaction.Engine) {
	if w := s.workers[id]; w != nil {
		w.again = true
		return
	}
	w := &execution{engine: e}
	s.workers[id] = w
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		waiting := false
		for {
			err := e.Run(s.ctx, id)
			if errors.Is(err, interaction.ErrCapacity) {
				if !waiting {
					_ = s.store.RecordCapacityWait(s.ctx, id)
					waiting = true
				}
				select {
				case <-s.ctx.Done():
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			s.mu.Lock()
			// A same-run decision/cancel racing a completed callback gets one drain,
			// but ownership/reconciliation failures cannot be made safe by that flag.
			if w.again && !s.closed && (err == nil || errors.Is(err, interaction.ErrWaitingApproval) || errors.Is(err, context.Canceled)) {
				w.again = false
				s.mu.Unlock()
				waiting = false
				continue
			}
			delete(s.workers, id)
			s.kickQueues()
			s.kickJobs()
			s.mu.Unlock()
			return
		}
	}()
}
func (s *InteractionService) events(w http.ResponseWriter, r *http.Request, id string) {
	after := int64(0)
	var e error
	if raw := r.URL.Query().Get("after"); raw != "" {
		after, e = strconv.ParseInt(raw, 10, 64)
	}
	if e != nil || after < 0 {
		wireError(w, 400, "invalid_cursor")
		return
	}
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		wireError(w, 429, "too_many_streams")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	rc := http.NewResponseController(w)
	send := func(kind string, seq int64, v any) error {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, e = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", seq, kind, raw); e != nil {
			return e
		}
		return rc.Flush()
	}
	var cursor int64
	if e = s.runtime.DB.SQL.QueryRowContext(r.Context(), "SELECT sequence FROM interaction_sessions WHERE id=?", id).Scan(&cursor); e != nil {
		return
	}
	if after > cursor || cursor-after > 512 {
		snap, e := s.snapshot(r.Context(), id)
		if e != nil {
			return
		}
		if send("snapshot", snap.Cursor, snap) != nil {
			return
		}
		after = snap.Cursor
	} else {
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		fmt.Fprint(w, ": connected\n\n")
		if rc.Flush() != nil {
			return
		}
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		evs, e := s.store.Events(r.Context(), id, after, 256)
		if e != nil {
			return
		}
		for _, ev := range evs {
			if send("event", ev.Sequence, projectEvent(ev)) != nil {
				return
			}
			after = ev.Sequence
		}
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-heartbeat.C:
			_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, e = fmt.Fprint(w, ": heartbeat\n\n"); e != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// Only typed validation/CAS refusals prove this operation did not commit.
// Storage/commit errors remain uncertain, even if their HTTP status is 409.
func sessionOperationError(w http.ResponseWriter, e error) {
	if errors.Is(e, interaction.ErrInvalid) || errors.Is(e, interaction.ErrConflict) || errors.Is(e, sql.ErrNoRows) {
		wireError(w, 409, "context_operation_rejected")
	} else {
		wireError(w, 409, "context_operation_unconfirmed")
	}
}
