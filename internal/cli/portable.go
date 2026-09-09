package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"agent/internal/memory"
	"agent/internal/observability"
	"agent/internal/project"
	"agent/internal/skill"
)

const (
	portableArtifactType    = "personal-agent.portable"
	portableArtifactVersion = 1
	defaultMemoryLimit      = 200
)

type portableArtifact struct {
	Type              string                      `json:"type"`
	Version           int                         `json:"version"`
	ExportedAt        time.Time                   `json:"exported_at"`
	Excluded          []string                    `json:"excluded"`
	Projects          []project.Project           `json:"projects"`
	Skills            []portableSkill             `json:"skills"`
	EpisodicMemory    []memory.EpisodicEvent      `json:"episodic_memory"`
	SemanticMemory    []memory.SemanticFact       `json:"semantic_memory"`
	KnowledgeMetadata []portableKnowledgeDocument `json:"knowledge_metadata"`
}

type portableSkill struct {
	Record   skill.Record          `json:"record"`
	Versions []skill.VersionRecord `json:"versions"`
}

type portableKnowledgeDocument struct {
	ID           string            `json:"id"`
	SourceURI    string            `json:"source_uri"`
	Title        string            `json:"title"`
	ContentHash  string            `json:"content_hash"`
	Metadata     map[string]string `json:"metadata"`
	PrivacyClass string            `json:"privacy_class"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

func exportCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	outputPath := fs.String("output", "", "output JSON artifact path")
	memoryLimit := fs.Int("memory-limit", defaultMemoryLimit, "maximum episodic and semantic memory records to export")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *outputPath == "" {
		return usageError("export requires --config and --output")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	artifact, err := buildPortableArtifact(ctx, db.SQL, *memoryLimit)
	if err != nil {
		return err
	}
	if err := writePortableArtifact(*outputPath, artifact); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "export=%s projects=%d skills=%d episodic_memory=%d semantic_memory=%d knowledge_metadata=%d excluded=%d\n",
		*outputPath, len(artifact.Projects), len(artifact.Skills), len(artifact.EpisodicMemory), len(artifact.SemanticMemory), len(artifact.KnowledgeMetadata), len(artifact.Excluded))
	return err
}

func importCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	inputPath := fs.String("input", "", "input JSON artifact path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *inputPath == "" {
		return usageError("import requires --config and --input")
	}
	artifact, err := readPortableArtifact(*inputPath)
	if err != nil {
		return err
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := importPortableArtifact(ctx, db.SQL, artifact); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "import=%s projects=%d skills=%d episodic_memory=%d semantic_memory=%d knowledge_metadata=%d\n",
		*inputPath, len(artifact.Projects), len(artifact.Skills), len(artifact.EpisodicMemory), len(artifact.SemanticMemory), len(artifact.KnowledgeMetadata))
	return err
}

func buildPortableArtifact(ctx context.Context, db *sql.DB, memoryLimit int) (portableArtifact, error) {
	if memoryLimit <= 0 {
		memoryLimit = defaultMemoryLimit
	}
	projects, err := (project.Store{DB: db}).List(ctx, 10000)
	if err != nil {
		return portableArtifact{}, err
	}
	skills, err := exportSkills(ctx, db)
	if err != nil {
		return portableArtifact{}, err
	}
	episodic, err := exportEpisodicMemory(ctx, db, memoryLimit)
	if err != nil {
		return portableArtifact{}, err
	}
	semantic, err := exportSemanticMemory(ctx, db, memoryLimit)
	if err != nil {
		return portableArtifact{}, err
	}
	knowledge, err := exportKnowledgeMetadata(ctx, db)
	if err != nil {
		return portableArtifact{}, err
	}
	return portableArtifact{
		Type:              portableArtifactType,
		Version:           portableArtifactVersion,
		ExportedAt:        time.Now().UTC(),
		Excluded:          portableExclusions(),
		Projects:          sanitizeProjects(projects),
		Skills:            sanitizePortableSkills(skills),
		EpisodicMemory:    sanitizeEpisodicMemory(episodic),
		SemanticMemory:    sanitizeSemanticMemory(semantic),
		KnowledgeMetadata: sanitizeKnowledgeMetadata(knowledge),
	}, nil
}

func portableExclusions() []string {
	return []string{
		"configuration secrets",
		"credentials",
		"browser cookies",
		"browser passwords",
		"browser profile data",
		"coding CLI credential stores",
		"document chunk text",
	}
}

func exportSkills(ctx context.Context, db *sql.DB) ([]portableSkill, error) {
	store := skill.Store{DB: db}
	records, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]portableSkill, 0, len(records))
	for _, record := range records {
		versions, err := store.Versions(ctx, record.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, portableSkill{Record: record, Versions: versions})
	}
	return out, nil
}

func exportEpisodicMemory(ctx context.Context, db *sql.DB, limit int) ([]memory.EpisodicEvent, error) {
	return memory.NewStore(db).Recent(ctx, limit)
}

func exportSemanticMemory(ctx context.Context, db *sql.DB, limit int) ([]memory.SemanticFact, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, scope, subject, predicate, object, source, confidence, privacy_class,
  evidence_ids_json, created_at, updated_at
FROM memory_semantic
ORDER BY updated_at DESC, id ASC
LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []memory.SemanticFact
	for rows.Next() {
		var fact memory.SemanticFact
		var evidenceJSON string
		if err := rows.Scan(&fact.ID, &fact.Scope, &fact.Subject, &fact.Predicate, &fact.Object, &fact.Source, &fact.Confidence, &fact.PrivacyClass, &evidenceJSON, &fact.CreatedAt, &fact.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(evidenceJSON), &fact.EvidenceIDs); err != nil {
			return nil, err
		}
		out = append(out, fact)
	}
	return out, rows.Err()
}

func exportKnowledgeMetadata(ctx context.Context, db *sql.DB) ([]portableKnowledgeDocument, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id, source_uri, title, content_hash, metadata_json, privacy_class, created_at, updated_at
FROM documents
ORDER BY id ASC
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []portableKnowledgeDocument
	for rows.Next() {
		var doc portableKnowledgeDocument
		var metadataJSON string
		if err := rows.Scan(&doc.ID, &doc.SourceURI, &doc.Title, &doc.ContentHash, &metadataJSON, &doc.PrivacyClass, &doc.CreatedAt, &doc.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(metadataJSON), &doc.Metadata); err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, rows.Err()
}

func writePortableArtifact(path string, artifact portableArtifact) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(artifact)
}

func readPortableArtifact(path string) (portableArtifact, error) {
	file, err := os.Open(path)
	if err != nil {
		return portableArtifact{}, err
	}
	defer file.Close()
	var artifact portableArtifact
	if err := json.NewDecoder(file).Decode(&artifact); err != nil {
		return portableArtifact{}, err
	}
	if artifact.Type != portableArtifactType {
		return portableArtifact{}, fmt.Errorf("unsupported portable artifact type %q", artifact.Type)
	}
	if artifact.Version != portableArtifactVersion {
		return portableArtifact{}, fmt.Errorf("unsupported portable artifact version %d", artifact.Version)
	}
	return artifact, nil
}

func importPortableArtifact(ctx context.Context, db *sql.DB, artifact portableArtifact) error {
	if artifact.Type != portableArtifactType || artifact.Version != portableArtifactVersion {
		return errors.New("unsupported portable artifact")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := importProjects(ctx, tx, artifact.Projects); err != nil {
		return err
	}
	if err := importSkills(ctx, tx, artifact.Skills); err != nil {
		return err
	}
	if err := importEpisodicMemory(ctx, tx, artifact.EpisodicMemory); err != nil {
		return err
	}
	if err := importSemanticMemory(ctx, tx, artifact.SemanticMemory); err != nil {
		return err
	}
	if err := importKnowledgeMetadata(ctx, tx, artifact.KnowledgeMetadata); err != nil {
		return err
	}
	return tx.Commit()
}

func importProjects(ctx context.Context, tx *sql.Tx, projects []project.Project) error {
	for _, item := range projects {
		repos, err := json.Marshal(item.RepositoryRefs)
		if err != nil {
			return err
		}
		scopes, err := json.Marshal(item.KnowledgeScopes)
		if err != nil {
			return err
		}
		skills, err := json.Marshal(item.AllowedSkills)
		if err != nil {
			return err
		}
		caps, err := json.Marshal(item.AllowedCapabilities)
		if err != nil {
			return err
		}
		agents, err := json.Marshal(item.AllowedCodingAgents)
		if err != nil {
			return err
		}
		models, err := json.Marshal(item.AllowedModels)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO projects (id,name,privacy_class,repository_refs_json,knowledge_scopes_json,memory_scope,allowed_skills_json,allowed_capabilities_json,allowed_coding_agents_json,allowed_models_json,budget_policy) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, privacy_class=excluded.privacy_class, repository_refs_json=excluded.repository_refs_json, knowledge_scopes_json=excluded.knowledge_scopes_json, memory_scope=excluded.memory_scope, allowed_skills_json=excluded.allowed_skills_json, allowed_capabilities_json=excluded.allowed_capabilities_json, allowed_coding_agents_json=excluded.allowed_coding_agents_json, allowed_models_json=excluded.allowed_models_json, budget_policy=excluded.budget_policy`, item.ID, item.Name, item.PrivacyClass, string(repos), string(scopes), item.MemoryScope, string(skills), string(caps), string(agents), string(models), item.BudgetPolicy); err != nil {
			return err
		}
	}
	return nil
}

func importSkills(ctx context.Context, tx *sql.Tx, skills []portableSkill) error {
	for _, item := range skills {
		now := time.Now().UTC()
		createdAt := item.Record.CreatedAt
		if createdAt.IsZero() {
			createdAt = now
		}
		updatedAt := item.Record.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO skills (id,name,description,active_version,status,source,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, active_version=excluded.active_version, status=excluded.status, source=excluded.source, updated_at=excluded.updated_at`, item.Record.ID, item.Record.Name, item.Record.Description, item.Record.ActiveVersion, item.Record.Status, item.Record.Source, createdAt, updatedAt); err != nil {
			return err
		}
		for _, version := range item.Versions {
			body, err := json.Marshal(skill.Normalize(version.Manifest))
			if err != nil {
				return err
			}
			checksum := version.Checksum
			if checksum == "" {
				checksum, err = skill.Checksum(version.Manifest)
				if err != nil {
					return err
				}
			}
			versionCreatedAt := version.CreatedAt
			if versionCreatedAt.IsZero() {
				versionCreatedAt = now
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO skill_versions (skill_id,version,manifest_json,checksum,created_at) VALUES (?,?,?,?,?) ON CONFLICT(skill_id,version) DO UPDATE SET manifest_json=excluded.manifest_json, checksum=excluded.checksum`, version.Manifest.ID, version.Manifest.Version, string(body), checksum, versionCreatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func importEpisodicMemory(ctx context.Context, tx *sql.Tx, events []memory.EpisodicEvent) error {
	for _, event := range events {
		payload, err := json.Marshal(event.Payload)
		if err != nil {
			return err
		}
		evidenceIDs, err := json.Marshal(event.EvidenceIDs)
		if err != nil {
			return err
		}
		createdAt := event.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_episodic (id, task_id, event_type, summary, payload_json, evidence_ids_json, confidence, privacy_class, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET task_id=excluded.task_id, event_type=excluded.event_type, summary=excluded.summary, payload_json=excluded.payload_json, evidence_ids_json=excluded.evidence_ids_json, confidence=excluded.confidence, privacy_class=excluded.privacy_class`, event.ID, event.TaskID, event.EventType, event.Summary, string(payload), string(evidenceIDs), event.Confidence, event.PrivacyClass, createdAt); err != nil {
			return err
		}
	}
	return nil
}

func importSemanticMemory(ctx context.Context, tx *sql.Tx, facts []memory.SemanticFact) error {
	for _, fact := range facts {
		evidenceIDs, err := json.Marshal(fact.EvidenceIDs)
		if err != nil {
			return err
		}
		createdAt := fact.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		updatedAt := fact.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_semantic (id, scope, subject, predicate, object, source, confidence, privacy_class, evidence_ids_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET scope=excluded.scope, subject=excluded.subject, predicate=excluded.predicate, object=excluded.object, source=excluded.source, confidence=excluded.confidence, privacy_class=excluded.privacy_class, evidence_ids_json=excluded.evidence_ids_json, updated_at=excluded.updated_at`, fact.ID, fact.Scope, fact.Subject, fact.Predicate, fact.Object, fact.Source, fact.Confidence, fact.PrivacyClass, string(evidenceIDs), createdAt, updatedAt); err != nil {
			return err
		}
	}
	return nil
}

func importKnowledgeMetadata(ctx context.Context, tx *sql.Tx, documents []portableKnowledgeDocument) error {
	for _, doc := range documents {
		metadata, err := json.Marshal(doc.Metadata)
		if err != nil {
			return err
		}
		createdAt := doc.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		updatedAt := doc.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = createdAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO documents (id, source_uri, title, content_hash, metadata_json, privacy_class, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET source_uri=excluded.source_uri, title=excluded.title, content_hash=excluded.content_hash, metadata_json=excluded.metadata_json, privacy_class=excluded.privacy_class, updated_at=excluded.updated_at`, doc.ID, doc.SourceURI, doc.Title, doc.ContentHash, string(metadata), doc.PrivacyClass, createdAt, updatedAt); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeProjects(in []project.Project) []project.Project {
	out := make([]project.Project, 0, len(in))
	for _, item := range in {
		item.ID = observability.Redact(item.ID)
		item.Name = observability.Redact(item.Name)
		item.PrivacyClass = observability.Redact(item.PrivacyClass)
		item.RepositoryRefs = redactStrings(item.RepositoryRefs)
		item.KnowledgeScopes = redactStrings(item.KnowledgeScopes)
		item.MemoryScope = observability.Redact(item.MemoryScope)
		item.AllowedSkills = redactStrings(item.AllowedSkills)
		item.AllowedCapabilities = redactStrings(item.AllowedCapabilities)
		item.AllowedCodingAgents = redactStrings(item.AllowedCodingAgents)
		item.AllowedModels = redactStrings(item.AllowedModels)
		item.BudgetPolicy = observability.Redact(item.BudgetPolicy)
		out = append(out, item)
	}
	return out
}

func sanitizePortableSkills(in []portableSkill) []portableSkill {
	out := make([]portableSkill, 0, len(in))
	for _, item := range in {
		item.Record.ID = observability.Redact(item.Record.ID)
		item.Record.Name = observability.Redact(item.Record.Name)
		item.Record.Description = observability.Redact(item.Record.Description)
		item.Record.ActiveVersion = observability.Redact(item.Record.ActiveVersion)
		item.Record.Status = skill.Status(observability.Redact(string(item.Record.Status)))
		item.Record.Source = observability.Redact(item.Record.Source)
		for i := range item.Versions {
			item.Versions[i].Manifest = sanitizeSkillManifest(item.Versions[i].Manifest)
			item.Versions[i].Checksum = observability.Redact(item.Versions[i].Checksum)
		}
		out = append(out, item)
	}
	return out
}

func sanitizeSkillManifest(manifest skill.Manifest) skill.Manifest {
	manifest.ID = observability.Redact(manifest.ID)
	manifest.Version = observability.Redact(manifest.Version)
	manifest.Name = observability.Redact(manifest.Name)
	manifest.Description = observability.Redact(manifest.Description)
	manifest.Status = skill.Status(observability.Redact(string(manifest.Status)))
	manifest.Permissions.MaxLevel = observability.Redact(manifest.Permissions.MaxLevel)
	manifest.Privacy.MaxExternalTrust = observability.Redact(manifest.Privacy.MaxExternalTrust)
	manifest.Requires.Capabilities = redactStrings(manifest.Requires.Capabilities)
	if manifest.Inputs != nil {
		inputs := make(map[string]skill.Field, len(manifest.Inputs))
		for name, field := range manifest.Inputs {
			inputs[observability.Redact(name)] = skill.Field{Type: observability.Redact(field.Type)}
		}
		manifest.Inputs = inputs
	}
	for i := range manifest.Workflow.Nodes {
		manifest.Workflow.Nodes[i].ID = observability.Redact(manifest.Workflow.Nodes[i].ID)
		manifest.Workflow.Nodes[i].Capability = observability.Redact(manifest.Workflow.Nodes[i].Capability)
		manifest.Workflow.Nodes[i].Role = observability.Redact(manifest.Workflow.Nodes[i].Role)
		manifest.Workflow.Nodes[i].DependsOn = redactStrings(manifest.Workflow.Nodes[i].DependsOn)
	}
	return manifest
}

func sanitizeEpisodicMemory(in []memory.EpisodicEvent) []memory.EpisodicEvent {
	out := make([]memory.EpisodicEvent, 0, len(in))
	for _, item := range in {
		item.ID = observability.Redact(item.ID)
		item.TaskID = observability.Redact(item.TaskID)
		item.EventType = observability.Redact(item.EventType)
		item.Summary = observability.Redact(item.Summary)
		item.Payload = redactMap(item.Payload)
		item.EvidenceIDs = redactStrings(item.EvidenceIDs)
		item.PrivacyClass = observability.Redact(item.PrivacyClass)
		out = append(out, item)
	}
	return out
}

func sanitizeSemanticMemory(in []memory.SemanticFact) []memory.SemanticFact {
	out := make([]memory.SemanticFact, 0, len(in))
	for _, item := range in {
		item.ID = observability.Redact(item.ID)
		item.Scope = observability.Redact(item.Scope)
		item.Subject = observability.Redact(item.Subject)
		item.Predicate = observability.Redact(item.Predicate)
		item.Object = observability.Redact(item.Object)
		item.Source = observability.Redact(item.Source)
		item.PrivacyClass = observability.Redact(item.PrivacyClass)
		item.EvidenceIDs = redactStrings(item.EvidenceIDs)
		out = append(out, item)
	}
	return out
}

func sanitizeKnowledgeMetadata(in []portableKnowledgeDocument) []portableKnowledgeDocument {
	out := make([]portableKnowledgeDocument, 0, len(in))
	for _, item := range in {
		item.ID = observability.Redact(item.ID)
		item.SourceURI = observability.Redact(item.SourceURI)
		item.Title = observability.Redact(item.Title)
		item.ContentHash = observability.Redact(item.ContentHash)
		item.Metadata = redactMap(item.Metadata)
		item.PrivacyClass = observability.Redact(item.PrivacyClass)
		out = append(out, item)
	}
	return out
}

func redactStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		out = append(out, observability.Redact(item))
	}
	return out
}

func redactMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[observability.Redact(key)] = observability.Redact(value)
	}
	return out
}
