package interaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"agent/internal/model"
	"agent/internal/runtime"
	"github.com/google/uuid"
)

var (
	ErrBudget          = errors.New("agent budget exhausted")
	ErrCapacity        = errors.New("engine capacity full before ownership; safe to wait")
	ErrOwned           = errors.New("run already owned; recover only after previous process stops")
	ErrWaitingApproval = errors.New("run waiting for approval")
	ErrReconciliation  = errors.New("run needs reconciliation")
	ErrNoBroker        = errors.New("tool broker unavailable")
)

type Phase string

const (
	PhaseQueued          Phase = "queued"
	PhaseRunning         Phase = "running"
	PhaseModel           Phase = "model"
	PhaseWaitingApproval Phase = "waiting_approval"
	PhaseExecuting       Phase = "executing"
	PhaseCancelling      Phase = "cancelling"
	PhaseCompleted       Phase = "completed"
	PhaseFailed          Phase = "failed"
	PhaseCancelled       Phase = "cancelled"
	PhaseReconciliation  Phase = "needs_reconciliation"
)

type ToolCall = model.ToolCall
type ToolProposal struct {
	ID               string
	Call             ToolCall
	RequiresApproval bool
	ExpiresAt        time.Time
	Digest           string
}

// ToolObservation binds a recovered durable result to its original proposal/call.
// It is a display/control record, not an instruction to execute.
type ToolObservation struct {
	ProposalID string
	CallID     string
	ToolResult
}

type ToolResult struct {
	Content string
	IsError bool
}

// Propose must be side-effect-free and repeatable. Execute must honor cancellation.
// Approval is owned by Engine, not the broker. All executions use the existing journal.
type ToolBroker interface {
	Propose(context.Context, string, ToolCall) (ToolProposal, error)
	Execute(context.Context, string) (ToolResult, error)
}
type Budgets struct {
	MaxSteps           int
	MaxDuration        time.Duration
	MaxTokens          int
	MaxOutputTokens    int
	MaxOutputBytes     int
	MaxToolResultBytes int
	MaxEvents          int
	MaxConcurrent      int
}
type EngineOptions struct {
	Budgets Budgets
	Request model.TurnRequest
}
type Checkpoint struct {
	Action        string // Trusted deterministic user action, not a model turn.
	Request       model.TurnRequest
	MessageOffset int
	Phase         Phase
	Messages      []model.ChatMessage
	Calls         []ToolCall
	Pending       []ToolProposal
	Steps         int
	Tokens        int
	Events        int
	OutputBytes   int
	Deadline      time.Time
	Budgets       Budgets
}
type Engine struct {
	store    *Store
	provider model.StreamProvider
	broker   ToolBroker
	options  EngineOptions
}

var activeOwners = struct {
	sync.Mutex
	cancels map[string]context.CancelFunc
}{cancels: map[string]context.CancelFunc{}}

func NewEngine(s *Store, p model.StreamProvider, b ToolBroker, o EngineOptions) (*Engine, error) {
	if s == nil || p == nil {
		return nil, ErrInvalid
	}
	d := &o.Budgets
	if d.MaxSteps == 0 {
		d.MaxSteps = 32
	}
	if d.MaxDuration == 0 {
		d.MaxDuration = 5 * time.Minute
	}
	if d.MaxTokens == 0 {
		d.MaxTokens = 64000
	}
	if d.MaxOutputTokens == 0 {
		d.MaxOutputTokens = 2048
	}
	if d.MaxOutputBytes == 0 {
		d.MaxOutputBytes = 1 << 20
	}
	if d.MaxToolResultBytes == 0 {
		d.MaxToolResultBytes = 64 << 10
	}
	if d.MaxEvents == 0 {
		d.MaxEvents = 2048
	}
	if d.MaxConcurrent == 0 {
		d.MaxConcurrent = 4
	}
	if d.MaxSteps < 1 || d.MaxDuration < 1 || d.MaxTokens < 1 || d.MaxOutputTokens < 1 || d.MaxOutputBytes < 1 || d.MaxToolResultBytes < 1 || d.MaxEvents < 1 || d.MaxConcurrent < 1 {
		return nil, ErrInvalid
	}
	return &Engine{store: s, provider: p, broker: b, options: o}, nil
}

// ProposalDigest binds exact JSON semantic arguments, tool name, call ID and run.
// Numbers retain their decimal spelling to avoid float precision loss.
func ProposalDigest(runID string, call ToolCall) (string, error) {
	if runID == "" || call.ID == "" || call.Name == "" {
		return "", ErrInvalid
	}
	dec := json.NewDecoder(bytes.NewBufferString(call.Arguments))
	dec.UseNumber()
	args, err := decodeArguments(dec)
	if err != nil {
		return "", err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return "", ErrInvalid
	}
	if _, ok := args.(map[string]any); !ok {
		return "", ErrInvalid
	}
	raw, err := json.Marshal([]any{runID, call.ID, call.Name, args})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (e *Engine) Checkpoint(ctx context.Context, id string) (Checkpoint, error) {
	return e.store.EngineCheckpoint(ctx, id)
}

func (s *Store) EngineCheckpoint(ctx context.Context, id string) (Checkpoint, error) {
	var cp Checkpoint
	var raw string
	var phase Phase
	err := s.db.QueryRowContext(ctx, "SELECT phase,checkpoint FROM interaction_engine WHERE run_id=?", id).Scan(&phase, &raw)
	if err == nil {
		err = json.Unmarshal([]byte(raw), &cp)
		cp.Phase = phase
	}
	return cp, err
}
func (e *Engine) save(ctx context.Context, r Run, owner string, cp *Checkpoint, kind string, payload any, observations ...ToolObservation) error {
	if kind != "" && kind != "run.needs_reconciliation" && kind != "run.recovered" && kind != "tool.uncertain" {
		if cp.Events >= cp.Budgets.MaxEvents {
			return ErrBudget
		}
		cp.Events++
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE interaction_engine SET phase=CASE WHEN cancel_requested=1 AND ? NOT IN ('completed','failed','cancelled','needs_reconciliation') THEN 'cancelling' ELSE ? END,checkpoint=? WHERE run_id=? AND owner=?", cp.Phase, cp.Phase, string(raw), r.ID, owner)
	if err = changed(res, err); err != nil {
		return err
	}
	if cp.Action == skillRunAction && cp.Phase == PhaseReconciliation && len(cp.Pending) == 1 {
		p := cp.Pending[0]
		var status string
		if tx.QueryRowContext(ctx, `SELECT status FROM side_effect_records WHERE idempotency_key=?`, effectKey(r.ID, p.ID)).Scan(&status) == nil && status != "completed" {
			_, plan, _, e := skillRunData(ctx, tx, r.ID)
			if e != nil {
				return e
			}
			i := nodeIndex(plan, p.Call.ID)
			if i < 0 {
				return ErrReconciliation
			}
			n := SkillRunNodeResult{NodeID: p.Call.ID, Capability: plan[i].Capability, ProposalID: p.ID, Outcome: "unknown", Reason: "invocation_unresolved"}
			raw, _ := json.Marshal(n)
			if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_node_results SET result=? WHERE run_id=? AND node_id=?`, string(raw), r.ID, p.Call.ID); err != nil {
				return err
			}
		}
	}
	if cp.Action == skillRunAction {
		if o, ok := payload.(ToolObservation); ok && kind == "tool.completed" {
			if err = skillNodeObservation(ctx, tx, r.ID, o); err != nil {
				return err
			}
		}
		for _, o := range observations {
			if err = skillNodeObservation(ctx, tx, r.ID, o); err != nil {
				return err
			}
		}
	}
	for _, observation := range observations {
		if err = appendEngineEvent(ctx, tx, r, "tool.recovered", observation); err != nil {
			return err
		}
	}
	if kind != "" {
		if err = appendEngineEvent(ctx, tx, r, kind, payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// appendEngineEvent participates in its caller's checkpoint/terminal transaction.
func appendEngineEvent(ctx context.Context, tx *sql.Tx, r Run, kind string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if skip, e := skillEventAdmission(ctx, tx, r, kind, raw); e != nil {
		return e
	} else if skip {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "UPDATE interaction_sessions SET sequence=sequence+1 WHERE id=?", r.SessionID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO interaction_events(session_id,run_id,sequence,version,type,payload) SELECT id,?,sequence,?,?,? FROM interaction_sessions WHERE id=?", r.ID, r.Version, kind, string(raw), r.SessionID)
	return err
}
func (e *Engine) claim(ctx context.Context, r Run, owner string) (Checkpoint, error) {
	isSkill, routeErr := e.store.IsSkillRun(ctx, r.ID)
	if routeErr != nil {
		return Checkpoint{}, routeErr
	}
	if isSkill {
		cp, err := e.store.EngineCheckpoint(ctx, r.ID)
		if err != nil {
			return cp, ErrReconciliation
		}
		if err = e.store.validateSkillCheckpoint(ctx, r.ID, cp); err != nil {
			return cp, err
		}
	}
	cp := e.initialCheckpoint(r)
	raw, err := json.Marshal(cp)
	if err != nil {
		return cp, err
	}
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return cp, err
	}
	defer tx.Rollback()
	if !isSkill {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO interaction_engine(run_id,phase,checkpoint) VALUES (?,?,?)", r.ID, cp.Phase, string(raw)); err != nil {
			return cp, err
		}
	}
	// An owner may have been released after a failed reconciliation write.
	// Ordinary resumption must not turn that unresolved execution into a
	// terminal approval/cancellation error. Only offline recovery may inspect
	// its journal. Check both phases because Cancel changes the visible phase
	// to cancelling without replacing the last execution checkpoint.
	var durableOwner, checkpointJSON string
	var durablePhase Phase
	if err = tx.QueryRowContext(ctx, "SELECT owner,phase,checkpoint FROM interaction_engine WHERE run_id=?", r.ID).Scan(&durableOwner, &durablePhase, &checkpointJSON); err != nil {
		return cp, err
	}
	if durableOwner != "" {
		return cp, ErrOwned
	}
	if err = json.Unmarshal([]byte(checkpointJSON), &cp); err != nil {
		return cp, err
	}
	if isSkill {
		if err = validateSkillCheckpointAt(ctx, tx, r.ID, cp); err != nil {
			return cp, err
		}
	}
	if (cp.Action == skillRunAction && !isSkill) || (cp.Action != "" && cp.Action != "file_undo" && cp.Action != skillRunAction) {
		return cp, ErrReconciliation
	}
	if durablePhase == PhaseExecuting || durablePhase == PhaseReconciliation || cp.Phase == PhaseExecuting || cp.Phase == PhaseReconciliation {
		return cp, ErrReconciliation
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM interaction_engine WHERE owner<>''").Scan(&count); err != nil {
		return cp, err
	}
	if count >= e.options.Budgets.MaxConcurrent {
		return cp, ErrCapacity
	}
	res, err := tx.ExecContext(ctx, "UPDATE interaction_engine SET owner=? WHERE run_id=? AND owner='' AND phase NOT IN ('executing','needs_reconciliation','completed','failed','cancelled') AND EXISTS(SELECT 1 FROM interaction_runs WHERE id=? AND state='running')", owner, r.ID, r.ID)
	if err = changed(res, err); err != nil {
		if errors.Is(err, ErrConflict) {
			err = ErrOwned
		}
		return cp, err
	}
	if err = tx.Commit(); err != nil {
		return cp, err
	}
	return e.Checkpoint(ctx, r.ID)
}
func (e *Engine) check(ctx context.Context, id string, cp *Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var cancelled bool
	if err := e.store.db.QueryRowContext(ctx, "SELECT cancel_requested FROM interaction_engine WHERE run_id=?", id).Scan(&cancelled); err != nil {
		return err
	}
	if cancelled {
		return context.Canceled
	}
	if time.Now().After(cp.Deadline) || (cp.Steps >= cp.Budgets.MaxSteps && !(cp.Action == skillRunAction && len(cp.Pending) == 0 && len(cp.Calls) == 0)) || cp.Tokens >= cp.Budgets.MaxTokens {
		return ErrBudget
	}
	return nil
}
func (e *Engine) Cancel(ctx context.Context, id string) error {
	// Persist intent before signalling; terminal state is written only after callbacks return.
	r, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if r.State != RunRunning {
		return ErrConflict
	}
	isSkill, routeErr := e.store.IsSkillRun(ctx, id)
	if routeErr != nil {
		return routeErr
	}
	if isSkill {
		cp, err := e.store.EngineCheckpoint(ctx, id)
		if err != nil {
			return ErrReconciliation
		}
		if err = e.store.validateSkillCheckpoint(ctx, id, cp); err != nil {
			return err
		}
	}
	cp := e.initialCheckpoint(r)
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !isSkill {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO interaction_engine(run_id,phase,checkpoint) VALUES (?,?,?)", id, cp.Phase, string(raw)); err != nil {
			return err
		}
	} else {
		var persisted string
		if err = tx.QueryRowContext(ctx, `SELECT checkpoint FROM interaction_engine WHERE run_id=?`, id).Scan(&persisted); err != nil {
			return ErrReconciliation
		}
		if json.Unmarshal([]byte(persisted), &cp) != nil {
			return ErrReconciliation
		}
		if err = validateSkillCheckpointAt(ctx, tx, id, cp); err != nil {
			return err
		}
	}

	res, err := tx.ExecContext(ctx, "UPDATE interaction_engine SET cancel_requested=1,phase='cancelling' WHERE run_id=? AND cancel_requested=0 AND phase NOT IN ('completed','failed','cancelled','needs_reconciliation')", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		if err = appendEvent(ctx, tx, r, "run.cancelling"); err != nil {
			return err
		}
	} else {
		var phase string
		if err = tx.QueryRowContext(ctx, "SELECT phase FROM interaction_engine WHERE run_id=?", id).Scan(&phase); err != nil {
			return err
		}
		if phase != "cancelling" {
			return ErrConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	activeOwners.Lock()
	if f := activeOwners.cancels[id]; f != nil {
		f()
	}
	activeOwners.Unlock()
	return nil
}
func (e *Engine) Run(ctx context.Context, id string) (err error) {
	r, err := e.store.GetRun(ctx, id)
	if err != nil {
		return err
	}
	if r.State != RunRunning {
		return ErrConflict
	}
	isSkill, routeErr := e.store.IsSkillRun(ctx, id)
	if routeErr != nil {
		return routeErr
	}
	if isSkill {
		b, err := NewSkillRunBroker(e.store, id)
		if err != nil {
			return err
		}
		// Routing is per invocation. An Engine can also service ordinary runs,
		// and concurrent callers must not mutate its shared configuration.
		routed := *e
		routed.broker = b
		routed.provider = actionOnlyProvider{}
		e = &routed
	}
	owner := uuid.NewString()
	// Serializes registration with cancellation and offline recovery in this process.
	activeOwners.Lock()
	cp, err := e.claim(ctx, r, owner)
	if err != nil {
		activeOwners.Unlock()
		return err
	}
	runCtx, cancel := context.WithDeadline(ctx, cp.Deadline)
	activeOwners.cancels[id] = cancel
	activeOwners.Unlock()
	defer func() {
		cancel()
		activeOwners.Lock()
		delete(activeOwners.cancels, id)
		activeOwners.Unlock()
		_, releaseErr := e.store.db.ExecContext(context.Background(), "UPDATE interaction_engine SET owner='' WHERE run_id=? AND owner=?", id, owner)
		if err == nil {
			err = releaseErr
		}
	}()
	err = e.loop(runCtx, r, owner, &cp)
	// Use independent context only after active callbacks have returned.
	finalCtx := context.Background()
	if errors.Is(err, ErrWaitingApproval) || errors.Is(err, ErrReconciliation) {
		return err
	}
	state := RunCompleted
	cp.Phase = PhaseCompleted
	if err != nil {
		state = RunFailed
		cp.Phase = PhaseFailed
	}
	var cancelled bool
	if qerr := e.store.db.QueryRowContext(finalCtx, "SELECT cancel_requested FROM interaction_engine WHERE run_id=?", id).Scan(&cancelled); qerr != nil {
		return qerr
	}
	if cancelled || errors.Is(err, context.Canceled) {
		state = RunCancelled
		cp.Phase = PhaseCancelled
		err = context.Canceled
	}
	if saveErr := e.finish(finalCtx, r, owner, &cp, state); saveErr != nil {
		return errors.Join(err, saveErr)
	}
	return err
}
func (e *Engine) finish(ctx context.Context, r Run, owner string, cp *Checkpoint, state RunState, observations ...ToolObservation) error {
	// Phase and aggregate terminal state commit atomically.
	tx, err := e.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Serialize with Cancel's intent write. Cancellation wins if it committed
	// before this terminal transition, even if the caller's prior read was stale.
	if _, err = tx.ExecContext(ctx, "UPDATE interaction_engine SET owner=owner WHERE run_id=? AND owner=?", r.ID, owner); err != nil {
		return err
	}
	var cancelled bool
	if err = tx.QueryRowContext(ctx, "SELECT cancel_requested FROM interaction_engine WHERE run_id=? AND owner=?", r.ID, owner).Scan(&cancelled); err != nil {
		return err
	}
	var kind string
	if err = tx.QueryRowContext(ctx, "SELECT execution_kind FROM interaction_runs WHERE id=?", r.ID).Scan(&kind); err != nil {
		return err
	}
	if kind == skillRunAction {
		if cp.Action != skillRunAction {
			return ErrReconciliation
		}
		if _, err = persistedSkillCheckpoint(ctx, tx, r.ID); err != nil {
			return err
		}
		pin, _, revoked, e := skillRunData(ctx, tx, r.ID)
		if e != nil {
			return e
		}
		w, policy, authorityErr := skillRunWorkspace(ctx, tx, r.SessionID)
		authorityValid := authorityErr == nil && w.ScopeID == pin.ScopeID && w.RootIdentity == pin.RootIdentity && policy == pin.PolicyDigest
		if authorityValid {
			identity, e := workspaceIdentity(w.Policy.Root)
			authorityValid = e == nil && identity == pin.RootIdentity
		}
		if !authorityValid && !revoked {
			if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET revoked=1 WHERE run_id=?`, r.ID); err != nil {
				return err
			}
			revoked = true
		}
		if revoked {
			cancelled = true
		}
		for _, o := range observations {
			if err = skillNodeObservation(ctx, tx, r.ID, o); err != nil {
				return err
			}
		}
		failed, complete, outcomeErr := skillOutcomeState(ctx, tx, r.ID)
		if outcomeErr != nil {
			return outcomeErr
		}
		if failed {
			state, cp.Phase = RunFailed, PhaseFailed
		}
		if state == RunCompleted && !complete {
			return ErrReconciliation
		}
	}
	cancellationWon := cancelled && state != RunCancelled
	if cancelled {
		state = RunCancelled
		cp.Phase = PhaseCancelled
	}
	if kind == skillRunAction {
		status := string(state)
		var revoked bool
		if err = tx.QueryRowContext(ctx, `SELECT revoked FROM interaction_skill_runs WHERE run_id=?`, r.ID).Scan(&revoked); err != nil {
			return err
		}
		if revoked {
			status = "revoked"
		}
		if err = hookRunTerminal(ctx, tx, r.ID, status); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET terminal=1 WHERE run_id=?`, r.ID); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE interaction_engine SET phase=?,checkpoint=? WHERE run_id=? AND owner=?", cp.Phase, string(raw), r.ID, owner)
	if err = changed(res, err); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, "UPDATE interaction_runs SET state=?,version=version+1 WHERE id=? AND state='running'", state, r.ID)
	if err = changed(res, err); err != nil {
		return err
	}
	for _, observation := range observations {
		if err = appendEngineEvent(ctx, tx, r, "tool.recovered", observation); err != nil {
			return err
		}
	}
	r.State = state
	r.Version++
	if err = appendEvent(ctx, tx, r, "run."+string(state)); err != nil {
		return err
	}
	if cp.Action == skillRunAction {
		if _, err = tx.ExecContext(ctx, `UPDATE interaction_skill_runs SET reserved_bytes=retained_bytes WHERE run_id=?`, r.ID); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if cancellationWon {
		return context.Canceled
	}
	return nil
}
func (e *Engine) loop(ctx context.Context, r Run, owner string, cp *Checkpoint) error {
	for {
		if err := e.check(ctx, r.ID, cp); err != nil {
			return err
		}
		if cp.Action == skillRunAction {
			if err := e.store.validateSkillCheckpoint(ctx, r.ID, *cp); err != nil {
				return err
			}
			failed, _, err := skillOutcomeState(ctx, e.store.db, r.ID)
			if err != nil {
				return err
			}
			if failed {
				return errSkillNodeFailed
			}
		}
		if len(cp.Pending) > 0 {
			if err := e.execute(ctx, r, owner, cp); err != nil {
				return err
			}
			continue
		}
		if len(cp.Calls) > 0 {
			if e.broker == nil {
				return ErrNoBroker
			}
			call := cp.Calls[0]
			digest, err := ProposalDigest(r.ID, call)
			if err != nil {
				return err
			}
			proposal, err := e.broker.Propose(ctx, r.ID, call)
			if err != nil {
				return err
			}
			actual, err := ProposalDigest(r.ID, proposal.Call)
			if err != nil || actual != digest || proposal.ID == "" {
				return ErrInvalid
			}
			if binder, ok := e.broker.(interface {
				preparedActionDigest(context.Context, string, ToolProposal) (string, error)
			}); ok {
				digest, err = binder.preparedActionDigest(ctx, r.ID, proposal)
				if err != nil {
					return err
				}
			}
			proposal.Digest = digest
			cp.Pending = []ToolProposal{proposal}
			cp.Calls = cp.Calls[1:]
			cp.Phase = PhaseRunning
			if err = e.save(ctx, r, owner, cp, "tool.proposed", proposal); err != nil {
				return err
			}
			continue
		}
		if cp.Action != "" {
			return nil
		}
		req := cp.Request
		req.Messages = cp.Messages
		req.MaxOutputTokens = cp.Budgets.MaxOutputTokens
		req.MaxOutputBytes = cp.Budgets.MaxOutputBytes - cp.OutputBytes
		req.MaxToolArgumentBytes = cp.Budgets.MaxToolResultBytes
		raw, err := json.Marshal(struct {
			Messages []model.ChatMessage
			Tools    []model.ToolDefinition
		}{req.Messages, req.Tools})
		if err != nil {
			return err
		}
		if len(raw) > cp.Budgets.MaxTokens-cp.Tokens || req.MaxOutputTokens > cp.Budgets.MaxTokens-cp.Tokens-len(raw) {
			return ErrBudget
		}
		reserve := len(raw) + req.MaxOutputTokens // Conservative byte-based request reservation; unknown usage is never zero.
		if reserve > cp.Budgets.MaxTokens-cp.Tokens || req.MaxOutputBytes <= 0 {
			return ErrBudget
		}
		cp.Tokens += reserve
		cp.Steps++
		cp.Phase = PhaseModel
		if err = e.save(ctx, r, owner, cp, "model.started", nil); err != nil {
			return err
		}
		turnOutputStart := cp.OutputBytes
		result, err := e.provider.Stream(ctx, req, func(ev model.ModelEvent) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if ev.Kind != model.EventTextDelta {
				return nil
			}
			if cp.Events >= cp.Budgets.MaxEvents || len(ev.Text) > cp.Budgets.MaxOutputBytes-cp.OutputBytes {
				return ErrBudget
			}
			cp.OutputBytes += len(ev.Text)
			return e.save(ctx, r, owner, cp, "model.text_delta", map[string]string{"text": ev.Text})
		})
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if turnOutputStart+len(result.Content) > cp.OutputBytes {
			cp.OutputBytes = turnOutputStart + len(result.Content)
		}
		if len(result.Content) > req.MaxOutputBytes {
			return ErrBudget
		}
		if result.Usage != nil {
			u := result.Usage
			if u.InputTokens < 0 || u.OutputTokens < 0 {
				return ErrInvalid
			}
			if u.InputTokens > cp.Budgets.MaxTokens || u.OutputTokens > cp.Budgets.MaxTokens-u.InputTokens {
				return ErrBudget
			}
			spent := cp.Tokens - reserve
			used := u.InputTokens + u.OutputTokens
			if used > cp.Budgets.MaxTokens-spent {
				cp.Tokens = cp.Budgets.MaxTokens
				return ErrBudget
			}
			cp.Tokens = spent + used
		}
		msg := model.ChatMessage{Role: "assistant", Content: result.Content}
		seen := map[string]bool{}
		argBytes := 0
		for _, call := range result.ToolCalls {
			argBytes += len(call.Arguments)
			if argBytes > cp.Budgets.MaxToolResultBytes || len(result.ToolCalls) > 128 || seen[call.ID] {
				return ErrInvalid
			}
			seen[call.ID] = true
			if _, err := ProposalDigest(r.ID, call); err != nil {
				return err
			}
			msg.ToolCalls = append(msg.ToolCalls, model.ChatToolCall{ID: call.ID, Type: "function", Function: model.ChatToolFunction{Name: call.Name, Arguments: call.Arguments}})
		}
		cp.Messages = append(cp.Messages, msg)
		cp.Calls = result.ToolCalls
		cp.Phase = PhaseRunning
		if err = e.save(ctx, r, owner, cp, "model.completed", result); err != nil {
			return err
		}
		if len(cp.Calls) == 0 {
			return nil
		}
	}
}
func (e *Engine) execute(ctx context.Context, r Run, owner string, cp *Checkpoint) error {
	p := cp.Pending[0]
	if e.broker == nil {
		return ErrNoBroker
	}
	if p.RequiresApproval {
		a, err := e.store.GetApproval(ctx, p.ID)
		if errors.Is(err, sql.ErrNoRows) {
			err = e.store.CreateApproval(ctx, Approval{ID: p.ID, RunID: r.ID, ActionDigest: p.Digest, ExpiresAt: p.ExpiresAt})
			if err != nil {
				return err
			}
			a, err = e.store.GetApproval(ctx, p.ID)
		}
		if err != nil {
			return err
		}
		if a.RunID != r.ID || a.ActionDigest != p.Digest || a.ConsumedAt != nil || !time.Now().Before(a.ExpiresAt) || a.Decision == "denied" {
			return ErrConflict
		}
		if a.Decision == "pending" {
			cp.Phase = PhaseWaitingApproval
			if err = e.save(ctx, r, owner, cp, "approval.waiting", p); err != nil {
				return err
			}
			return ErrWaitingApproval
		}
		if err = e.store.ConsumeApproval(ctx, p.ID, p.Digest); err != nil {
			return err
		}
	}
	if err := e.check(ctx, r.ID, cp); err != nil {
		return err
	}
	journal := runtime.SQLiteSideEffectStore{DB: e.store.db}
	var err error
	var hooks bool
	if cp.Action == skillRunAction {
		_, hooks, err = hookRunSnapshotAt(ctx, e.store.db, r.ID)
		if err != nil {
			return err
		}
	}
	if hooks {
		if err = e.startSkillNode(ctx, r, owner, cp); err != nil {
			return err
		}
	} else {
		cp.Steps++
		cp.Phase = PhaseExecuting
		if err = e.save(ctx, r, owner, cp, "tool.started", p); err != nil {
			return err
		}
		_, claimed, claimErr := journal.Claim(ctx, runtime.SideEffectRecord{IdempotencyKey: effectKey(r.ID, p.ID), WorkflowID: r.ID, NodeID: p.ID, CapabilityID: p.Call.Name})
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			return e.reconcile(r, owner, cp)
		}
	}
	if err := ctx.Err(); err != nil {
		return e.reconcile(r, owner, cp)
	}
	result, execErr := e.broker.Execute(ctx, p.ID)
	if execErr != nil {
		if result.Content != "" && len(result.Content) <= cp.Budgets.MaxToolResultBytes {
			cp.Phase = PhaseReconciliation
			observation := ToolObservation{ProposalID: p.ID, CallID: p.Call.ID, ToolResult: result}
			if err := e.save(context.Background(), r, owner, cp, "tool.uncertain", observation); err != nil {
				execErr = errors.Join(execErr, err)
			}
		}
		return e.reconcile(r, owner, cp, execErr)
	} // An error does not attest no external effect.
	if len(result.Content) > cp.Budgets.MaxToolResultBytes {
		return e.reconcile(r, owner, cp)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return e.reconcile(r, owner, cp)
	}
	if cp.Action == skillRunAction && len(raw) > cp.Budgets.MaxToolResultBytes {
		return e.reconcile(r, owner, cp)
	}
	if err = journal.Complete(context.Background(), effectKey(r.ID, p.ID), string(raw)); err != nil {
		return e.reconcile(r, owner, cp)
	}
	if cp.Action == "" {
		cp.Messages = append(cp.Messages, model.ChatMessage{Role: "tool", Content: result.Content, ToolCallID: p.Call.ID})
	}
	cp.Pending = nil
	cp.Phase = PhaseRunning
	if cp.Action == skillRunAction {
		observation := ToolObservation{ProposalID: p.ID, CallID: p.Call.ID, ToolResult: result}
		if err = e.save(context.Background(), r, owner, cp, "tool.completed", observation); err != nil {
			cp.Pending = []ToolProposal{p}
			return e.reconcile(r, owner, cp, err)
		}
		if result.IsError {
			return errSkillNodeFailed
		}
		return nil
	}
	return e.save(context.Background(), r, owner, cp, "tool.completed", result)
}
func effectKey(runID, proposalID string) string {
	return fmt.Sprintf("interaction:%d:%s:%s", len(runID), runID, proposalID)
}
func (e *Engine) reconcile(r Run, owner string, cp *Checkpoint, causes ...error) error {
	cp.Phase = PhaseReconciliation
	causes = append(causes, ErrReconciliation, e.save(context.Background(), r, owner, cp, "run.needs_reconciliation", nil))
	return errors.Join(causes...)
}

func modelToolMessage(id, content string) model.ChatMessage {
	return model.ChatMessage{Role: "tool", Content: content, ToolCallID: id}
}

// SessionTranscript returns completed runs in their original session event order,
// excluding injected request context. Retrieval/compaction can select from these
// authoritative persisted messages without reconstructing them from delta events.
func (s *Store) SessionTranscript(ctx context.Context, sessionID string) ([]model.ChatMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.checkpoint FROM interaction_engine e JOIN interaction_runs r ON r.id=e.run_id JOIN interaction_events v ON v.run_id=r.id AND v.type='run.created' WHERE r.session_id=? AND r.state='completed' ORDER BY v.sequence`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []model.ChatMessage{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var cp Checkpoint
		if err = json.Unmarshal([]byte(raw), &cp); err != nil {
			return nil, err
		}
		if cp.MessageOffset < 0 || cp.MessageOffset > len(cp.Messages) {
			return nil, ErrInvalid
		}
		messages = append(messages, cp.Messages[cp.MessageOffset:]...)
	}
	return messages, rows.Err()
}

func (e *Engine) initialCheckpoint(r Run) Checkpoint {
	cp := Checkpoint{Request: e.options.Request, MessageOffset: len(e.options.Request.Messages), Phase: PhaseQueued, Messages: append(append([]model.ChatMessage(nil), e.options.Request.Messages...), model.ChatMessage{Role: "user", Content: r.Message.Content}), Budgets: e.options.Budgets, Deadline: time.Now().Add(e.options.Budgets.MaxDuration)}
	return cp
}

// decodeArguments rejects duplicate keys at every depth. An approval must not
// bind a different interpretation of JSON than the executor sees.
func decodeArguments(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, exists := object[key]; exists {
				return nil, ErrInvalid
			}
			value, err := decodeArguments(dec)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		end, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim('}') {
			return nil, ErrInvalid
		}
		return object, nil
	case '[':
		array := []any{}
		for dec.More() {
			value, err := decodeArguments(dec)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim(']') {
			return nil, ErrInvalid
		}
		return array, nil
	default:
		return nil, ErrInvalid
	}
}
