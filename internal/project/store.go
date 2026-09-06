package project

import (
	"context"
	"database/sql"
	"encoding/json"
)

type Store struct{ DB *sql.DB }

func (s Store) Save(ctx context.Context, p Project) error {
	repos, _ := json.Marshal(p.RepositoryRefs)
	scopes, _ := json.Marshal(p.KnowledgeScopes)
	skills, _ := json.Marshal(p.AllowedSkills)
	caps, _ := json.Marshal(p.AllowedCapabilities)
	agents, _ := json.Marshal(p.AllowedCodingAgents)
	_, err := s.DB.ExecContext(ctx, `INSERT INTO projects (id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,budget_policy) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, privacy_class=excluded.privacy_class, repository_refs_json=excluded.repository_refs_json, knowledge_scopes_json=excluded.knowledge_scopes_json, memory_scope=excluded.memory_scope, allowed_skills_json=excluded.allowed_skills_json, allowed_capabilities_json=excluded.allowed_capabilities_json, allowed_coding_agents_json=excluded.allowed_coding_agents_json, budget_policy=excluded.budget_policy`, p.ID, p.Name, p.PrivacyClass, string(repos), string(scopes), p.MemoryScope, string(skills), string(caps), string(agents), p.BudgetPolicy)
	return err
}
func (s Store) Get(ctx context.Context, id string) (Project, error) {
	var p Project
	var repos, scopes, skills, caps, agents string
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,budget_policy FROM projects WHERE id=?`, id).Scan(&p.ID, &p.Name, &p.PrivacyClass, &repos, &scopes, &p.MemoryScope, &skills, &caps, &agents, &p.BudgetPolicy)
	if err != nil {
		return p, err
	}
	for _, item := range []struct {
		raw string
		dst any
	}{{repos, &p.RepositoryRefs}, {scopes, &p.KnowledgeScopes}, {skills, &p.AllowedSkills}, {caps, &p.AllowedCapabilities}, {agents, &p.AllowedCodingAgents}} {
		if err := json.Unmarshal([]byte(item.raw), item.dst); err != nil {
			return p, err
		}
	}
	return p, nil
}
