package api

import (
	"agent/internal/interaction"
	"agent/internal/model"
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

type JobDetail struct {
	SchemaVersion int             `json:"schema_version"`
	Job           interaction.Job `json:"job"`
	Snapshot      Snapshot        `json:"snapshot"`
}

type JobArtifact struct {
	SchemaVersion   int    `json:"schema_version"`
	OriginSessionID string `json:"origin_session_id"`
	JobID           string `json:"job_id"`
	SessionID       string `json:"session_id"`
	ArtifactID      string `json:"artifact_id"`
	Bytes           int    `json:"bytes"`
	Encoding        string `json:"encoding"`
	Content         string `json:"content"`
}

func jobRoute(p []string) bool {
	return len(p) == 3 && p[0] == "sessions" && p[2] == "jobs" || len(p) == 5 && p[0] == "sessions" && p[2] == "jobs" && (p[4] == "stop" || p[4] == "decision")
}
func (s *InteractionService) readJobs(w http.ResponseWriter, r *http.Request, p []string) bool {
	if len(p) < 3 || p[0] != "sessions" || (p[2] != "jobs" && p[2] != "job-operations") {
		return false
	}
	ctx := r.Context()
	if p[2] == "job-operations" && len(p) >= 4 {
		request, e := url.PathUnescape(strings.TrimPrefix(r.URL.EscapedPath(), "/api/v1/sessions/"+url.PathEscape(p[1])+"/job-operations/"))
		if e != nil {
			wireError(w, 400, "invalid_request")
			return true
		}
		v, e := s.store.JobOperation(ctx, p[1], request)
		if errors.Is(e, sql.ErrNoRows) {
			wireError(w, 404, "job_operation_not_found")
		} else if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, v)
		}
		return true
	}
	if len(p) == 3 {
		v, e := s.store.Jobs(ctx, p[1])
		if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, v)
		}
		return true
	}
	if len(p) == 6 && p[2] == "jobs" && p[4] == "artifacts" {
		j, e := s.store.Job(ctx, p[3])
		if e != nil || j.OriginSessionID != p[1] {
			wireError(w, 404, "job_not_found")
			return true
		}
		var size int
		var raw []byte
		// Never allocate an unbounded stored blob; the origin alone is not an
		// artifact capability. Fetch only under this immutable execution session.
		e = s.runtime.DB.SQL.QueryRowContext(ctx, "SELECT length(content),CASE WHEN length(content)<=? THEN content ELSE NULL END FROM interaction_artifacts WHERE session_id=? AND id=?", interaction.MaxWorkspaceFileBytes, j.SessionID, p[5]).Scan(&size, &raw)
		if errors.Is(e, sql.ErrNoRows) {
			wireError(w, 404, "job_artifact_not_found")
		} else if e != nil {
			wireError(w, 500, "storage_error")
		} else if size > interaction.MaxWorkspaceFileBytes {
			wireError(w, 409, "job_artifact_too_large")
		} else {
			encoding, content := "utf8", string(raw)
			if !utf8.Valid(raw) {
				encoding = "base64"
				content = base64.StdEncoding.EncodeToString(raw)
			}
			wireJSON(w, 200, JobArtifact{1, j.OriginSessionID, j.ID, j.SessionID, p[5], size, encoding, content})
		}
		return true
	}

	if len(p) == 4 {
		j, e := s.store.Job(ctx, p[3])
		if e != nil || j.OriginSessionID != p[1] {
			wireError(w, 404, "job_not_found")
			return true
		}
		snap, e := s.snapshot(ctx, j.SessionID)
		if e != nil {
			wireError(w, 500, "storage_error")
		} else {
			wireJSON(w, 200, JobDetail{1, j, snap})
		}
		return true
	}
	wireError(w, 404, "not_found")
	return true
}
func (s *InteractionService) validateJob(ctx context.Context, j interaction.Job) error {
	e := s.store.ValidateJob(ctx, j.ID)
	if e == nil {
		_, e = s.resolveSession(ctx, j.OriginSessionID)
	}
	if e != nil {
		_ = s.store.BlockJob(ctx, j.ID, "origin_policy_root_or_provider_revoked")
		return interaction.ErrJobRevoked
	}
	return nil
}

// Uses only the existing bounded run owner map. Two Jobs leave slots for ordinary work.
func (s *InteractionService) kickJobs() {
	if s.closed {
		return
	}
	items, e := s.store.RegisteredJobs(s.ctx)
	if e != nil {
		return
	}
	for _, j := range items {
		if len(s.workers) >= maxInteractionExecutors {
			return
		}
		n, err := s.jobWorkerCount(s.ctx)
		if err != nil || n >= 2 {
			return
		}
		if e = s.validateJob(s.ctx, j); e != nil {
			continue
		}
		sel, e := s.resolveSession(s.ctx, j.SessionID)
		if e != nil {
			_ = s.store.BlockJob(s.ctx, j.ID, "execution_provider_revoked")
			continue
		}
		run, e := s.store.AdmitJob(s.ctx, j.ID)
		if e != nil {
			if errors.Is(e, interaction.ErrJobRevoked) {
				_ = s.store.BlockJob(s.ctx, j.ID, "origin_policy_revoked")
			}
			continue
		}
		engine, e := s.engine(s.ctx, run, sel)
		if e != nil {
			// Successful admission in this exclusive service and no start call
			// prove this particular deterministic failure never executed.
			// Storage/ambiguous errors and recovered missing checkpoints do not.
			if errors.Is(e, interaction.ErrContextBudget) {
				_ = s.store.FailNewJobPreparation(s.ctx, run)
			}
			continue
		}
		s.start(run.ID, engine)
	}
}
func (s *InteractionService) writeJobs(ctx context.Context, w http.ResponseWriter, p []string, in writeInput) {
	var e error
	var j interaction.Job
	fresh := false
	committed := false
	if len(p) == 3 {
		j, e = s.store.CreateJob(ctx, p[1], in.RequestID, in.Content, in.OriginRunID, in.Effects)
	} else {
		j, e = s.store.Job(ctx, p[3])
		if errors.Is(e, sql.ErrNoRows) {
			// This lookup precedes every mutation; a missing target is a definite refusal.
			e = interaction.ErrInvalid
		}
		if e == nil && j.OriginSessionID != p[1] {
			e = interaction.ErrInvalid
		}
		if e == nil {
			if p[4] == "stop" {
				e = s.store.StopJob(ctx, p[1], j.ID, in.RequestID)
				committed = e == nil
				canDrain := j.State != "unknown" || s.workers[j.RunID] != nil
				if e == nil && !canDrain {
					cp, err := s.store.EngineCheckpoint(ctx, j.RunID)
					if err == nil {
						canDrain = cp.Phase != interaction.PhaseReconciliation
					} else if !errors.Is(err, sql.ErrNoRows) {
						e = err
					}
				}
				// Missing checkpoints remain unknown. Existing checkpoints may
				// be cancelled offline; Engine.claim still rejects uncertain
				// execution even if the visible phase changed to cancelling.
				if e == nil && canDrain && j.State != "registered" && j.State != "stopped" && j.State != "completed" && j.State != "failed" {
					engine, _ := interaction.NewEngine(s.store, offlineProvider{}, s.broker, interaction.EngineOptions{})
					e = engine.Cancel(ctx, j.RunID)
					if e == nil {
						if s.workers[j.RunID] != nil {
							s.workers[j.RunID].again = true
						} else if len(s.workers) < maxInteractionWorkers {
							s.start(j.RunID, engine)
						}
					}
				}
			} else {
				// Replay only reads the ledger. A lost decision acknowledgement cannot restart work.
				if old, err := s.store.JobOperation(ctx, p[1], in.RequestID); err == nil {
					_ = old
					fresh, e = s.store.DecideJob(ctx, p[1], j.ID, in.RequestID, in.ApprovalID, in.Digest, in.Decision)
				} else if !errors.Is(err, sql.ErrNoRows) {
					e = err
				} else if e = s.validateJob(ctx, j); e == nil {
					count, err := s.jobWorkerCount(ctx)
					if err != nil {
						wireError(w, 500, "outcome_unknown_check_job")
						return
					}
					if s.workers[j.RunID] == nil && (len(s.workers) >= maxInteractionExecutors || count >= 2) {
						wireError(w, 503, "job_operation_rejected")
						return
					}
					fresh, e = s.store.DecideJob(ctx, p[1], j.ID, in.RequestID, in.ApprovalID, in.Digest, in.Decision)
				}
				if e == nil {
					committed = true
				}
				if e == nil && fresh {
					sel, err := s.resolveSession(ctx, j.SessionID)
					if err != nil {
						e = err
					} else {
						run, err := s.store.GetRun(ctx, j.RunID)
						if err != nil {
							e = err
						} else {
							engine, err := s.engine(ctx, run, sel)
							if err != nil {
								e = err
							} else {
								s.start(run.ID, engine)
							}
						}
					}
				}
			}
		}
	}
	if e != nil {
		if committed {
			wireError(w, 500, "outcome_unknown_check_job")
			return
		}
		switch {
		case errors.Is(e, interaction.ErrInvalid), errors.Is(e, interaction.ErrConflict), errors.Is(e, interaction.ErrJobFull), errors.Is(e, interaction.ErrJobRevoked):
			wireError(w, 409, "job_operation_rejected")
		default:
			wireError(w, 500, "outcome_unknown_check_job")
		}
		return
	}
	s.kickJobs()
	v, e := s.store.JobOperation(ctx, p[1], in.RequestID)
	if e != nil {
		wireError(w, 500, "outcome_unknown_check_job")
		return
	}
	wireJSON(w, 202, v)
}

type jobProvider struct {
	s        *InteractionService
	job      interaction.Job
	provider model.StreamProvider
}

func (p jobProvider) Stream(ctx context.Context, r model.TurnRequest, emit func(model.ModelEvent) error) (model.TurnResult, error) {
	if e := p.s.validateJob(ctx, p.job); e != nil {
		return model.TurnResult{}, e
	}
	return p.provider.Stream(ctx, r, emit)
}

// Called only under service mu; inspection never reserves a worker.
func (s *InteractionService) jobWorkerCount(ctx context.Context) (int, error) {
	n := 0
	for run := range s.workers {
		if _, e := s.store.JobForRun(ctx, run); e == nil {
			n++
		} else if !errors.Is(e, sql.ErrNoRows) {
			return 0, e
		}
	}
	return n, nil
}
