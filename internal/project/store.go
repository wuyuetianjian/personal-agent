package project

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type Store struct{ DB *sql.DB }

func (s Store) Save(ctx context.Context, p Project) error {
	if p.imported || (p.ExecutionClass != "" && p.ExecutionClass != ExecutionLegacyPolicy) {
		return ErrProjectWriteDenied
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Include retained tombstones: deleting a metadata row cannot make Save a
	// promotion/recreation escape hatch. Missing classification on an existing
	// row is an integrity failure, not a legacy default.
	var class string
	err = tx.QueryRowContext(ctx, `SELECT execution_class FROM project_execution_classification WHERE project_id=?`, p.ID).Scan(&class)
	if err == nil && class != ExecutionLegacyPolicy {
		return ErrProjectWriteDenied
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE id=?`, p.ID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return ErrInvalidExecutionClass
		}
	}

	repos, _ := json.Marshal(p.RepositoryRefs)
	scopes, _ := json.Marshal(p.KnowledgeScopes)
	skills, _ := json.Marshal(p.AllowedSkills)
	caps, _ := json.Marshal(p.AllowedCapabilities)
	agents, _ := json.Marshal(p.AllowedCodingAgents)
	models, _ := json.Marshal(p.AllowedModels)
	_, err = tx.ExecContext(ctx, `INSERT INTO projects (id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, privacy_class=excluded.privacy_class, repository_refs_json=excluded.repository_refs_json, knowledge_scopes_json=excluded.knowledge_scopes_json, memory_scope=excluded.memory_scope, allowed_skills_json=excluded.allowed_skills_json, allowed_capabilities_json=excluded.allowed_capabilities_json, allowed_coding_agents_json=excluded.allowed_coding_agents_json, allowed_models_json=excluded.allowed_models_json, budget_policy=excluded.budget_policy`, p.ID, p.Name, p.PrivacyClass, string(repos), string(scopes), p.MemoryScope, string(skills), string(caps), string(agents), string(models), p.BudgetPolicy)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s Store) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	var repos, scopes, skills, caps, agents, models string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy,(SELECT execution_class FROM project_execution_classification c WHERE c.project_id=projects.id) FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.PrivacyClass, &repos, &scopes, &p.MemoryScope, &skills, &caps, &agents, &models, &p.BudgetPolicy, &p.ExecutionClass)
	if err != nil {
		return p, err
	}
	if !p.ValidExecutionClass() {
		return p, ErrInvalidExecutionClass
	}
	for _, item := range []struct {
		raw string
		dst any
	}{{repos, &p.RepositoryRefs}, {scopes, &p.KnowledgeScopes}, {skills, &p.AllowedSkills}, {caps, &p.AllowedCapabilities}, {agents, &p.AllowedCodingAgents}, {models, &p.AllowedModels}} {
		if err := json.Unmarshal([]byte(item.raw), item.dst); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (s Store) List(ctx context.Context, limit int) ([]Project, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy,(SELECT execution_class FROM project_execution_classification c WHERE c.project_id=projects.id) FROM projects ORDER BY name ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		var p Project
		var repos, scopes, skills, caps, agents, models string
		if err := rows.Scan(&p.ID, &p.Name, &p.PrivacyClass, &repos, &scopes, &p.MemoryScope, &skills, &caps, &agents, &models, &p.BudgetPolicy, &p.ExecutionClass); err != nil {
			return nil, err
		}
		if !p.ValidExecutionClass() {
			return nil, ErrInvalidExecutionClass
		}
		for _, item := range []struct {
			raw string
			dst any
		}{{repos, &p.RepositoryRefs}, {scopes, &p.KnowledgeScopes}, {skills, &p.AllowedSkills}, {caps, &p.AllowedCapabilities}, {agents, &p.AllowedCodingAgents}, {models, &p.AllowedModels}} {
			if err := json.Unmarshal([]byte(item.raw), item.dst); err != nil {
				return nil, err
			}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s Store) Archive(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET name = name || ' (archived ' || ? || ')' WHERE id = ? AND name NOT LIKE '% (archived %)'`, time.Now().UTC().Format("2006-01-02"), id)
	return err
}
