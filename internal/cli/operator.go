package cli

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"agent/internal/api"
	"agent/internal/config"
	agentEvent "agent/internal/event"
	"agent/internal/notification"
	"agent/internal/observability"
	"agent/internal/permission"
	"agent/internal/project"
	"agent/internal/rag"
	"agent/internal/reliability"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/trigger"
)

type checkResult struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Remediation string `json:"remediation,omitempty"`
}

func initCommand(ctx context.Context, args []string, ioStreams IO) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "configs/config.yaml", "config file path")
	dataDir := fs.String("data-dir", "./data", "data directory")
	dbPath := fs.String("db-path", "", "SQLite database path")
	force := fs.Bool("force", false, "overwrite an existing config")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(*dataDir, "personal-agent.db")
	}
	if _, err := os.Stat(*configPath); err == nil && !*force {
		return fmt.Errorf("config already exists at %s; rerun with --force to overwrite", *configPath)
	}
	if err := os.MkdirAll(filepath.Dir(*configPath), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return err
	}
	content := defaultConfig(*dataDir, *dbPath)
	if err := os.WriteFile(*configPath, []byte(content), 0o600); err != nil {
		return err
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		return err
	}
	fmt.Fprintf(ioStreams.Stdout, "config=%s\ndata_dir=%s\ndatabase=%s\nprivacy_secret_env=%s\n", *configPath, *dataDir, *dbPath, cfg.Privacy.HMACSecretEnv)
	return nil
}

func defaultConfig(dataDir string, dbPath string) string {
	return fmt.Sprintf(`config_version: 1
app:
  name: personal-agent
  environment: local
  data_dir: %s

server:
  enabled: true
  listen_addr: 127.0.0.1:8787
  request_timeout: 120s

storage:
  driver: sqlite
  sqlite:
    path: %s

privacy:
  hmac_secret_env: PERSONAL_AGENT_PRIVACY_HMAC_SECRET
  fail_closed_for_public_models: true

models:
  default_provider: local
  providers:
    local:
      type: openai_compatible
      base_url: http://127.0.0.1:11434/v1
      trust_level: local_private
  registry:
    - id: local-planner
      provider: local
      model: local-planner
      trust_level: local_private
      context_window: 32768
      capabilities: [chat]

agent:
  leader:
    model_id: local-planner
    temperature: 0.2
    max_output_tokens: 2048
    trust_level_required: local_private

browser:
  enabled: false
  runtime: chromedp
  screenshot_dir: %s

permissions:
  default: deny
`, dataDir, dbPath, filepath.Join(dataDir, "browser", "screenshots"))
}

func configCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "validate" {
		return usageError("config requires validate")
	}
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("config validate requires --config")
	}
	report, err := config.Inspect(*configPath)
	if err != nil {
		return err
	}
	results := []checkResult{{Name: "schema", Status: "OK"}, {Name: "storage", Status: "OK"}, {Name: "models", Status: "OK"}}
	for _, warning := range report.Warnings {
		results = append(results, checkResult{Name: "config", Status: "WARN", Remediation: warning})
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"status": "OK", "checks": results})
	}
	for _, result := range results {
		printCheck(stdout, result)
	}
	return nil
}

func doctorCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("doctor requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	db, err := storage.Open(ctx, cfg.Storage)
	results := []checkResult{}
	if err != nil {
		results = append(results, checkResult{Name: "database", Status: "FAIL", Remediation: err.Error()})
	} else {
		defer db.Close()
		if err := storage.Migrate(ctx, db.SQL); err != nil {
			results = append(results, checkResult{Name: "migrations", Status: "FAIL", Remediation: err.Error()})
		} else {
			results = append(results, checkResult{Name: "database", Status: "OK"})
			if got, err := storage.IntegrityCheck(ctx, db.SQL); err != nil || got != "ok" {
				results = append(results, checkResult{Name: "sqlite_integrity", Status: "FAIL", Remediation: fmt.Sprintf("%v %s", err, got)})
			} else {
				results = append(results, checkResult{Name: "sqlite_integrity", Status: "OK"})
			}
		}
	}
	results = append(results, pathWritableCheck("data_dir", cfg.App.DataDir))
	if cfg.Privacy.HMACSecretEnv == "" {
		results = append(results, checkResult{Name: "privacy_secret", Status: "FAIL", Remediation: "set privacy.hmac_secret_env"})
	} else if _, ok := config.EnvValue(cfg.Privacy.HMACSecretEnv); !ok {
		results = append(results, checkResult{Name: "privacy_secret", Status: "WARN", Remediation: "set " + cfg.Privacy.HMACSecretEnv + " before enabling public providers"})
	} else {
		results = append(results, checkResult{Name: "privacy_secret", Status: "OK"})
	}
	for id, provider := range cfg.Models.Providers {
		if !provider.IsEnabled() {
			results = append(results, checkResult{Name: "model_provider." + id, Status: "WARN", Remediation: "provider disabled"})
			continue
		}
		if provider.BaseURL == "" && provider.BaseURLEnv != "" {
			if _, ok := config.EnvValue(provider.BaseURLEnv); !ok {
				results = append(results, checkResult{Name: "model_provider." + id, Status: "WARN", Remediation: "set " + provider.BaseURLEnv + " to check endpoint health"})
				continue
			}
		}
		results = append(results, checkResult{Name: "model_provider." + id, Status: "OK"})
	}
	if cfg.Browser.Enabled {
		results = append(results, pathWritableCheck("browser_screenshot_dir", cfg.Browser.ScreenshotDir))
	} else {
		results = append(results, checkResult{Name: "browser", Status: "WARN", Remediation: "browser disabled"})
	}
	for id, backend := range cfg.CodingAgents.Backends {
		if !backend.IsEnabled() {
			results = append(results, checkResult{Name: "coding_agent." + id, Status: "WARN", Remediation: "backend disabled"})
			continue
		}
		binary := backend.Binary
		if backend.BinaryEnv != "" {
			if got, ok := config.EnvValue(backend.BinaryEnv); ok {
				binary = got
			}
		}
		if binary == "" {
			results = append(results, checkResult{Name: "coding_agent." + id, Status: "WARN", Remediation: "no binary configured"})
		} else if _, err := exec.LookPath(binary); err != nil {
			results = append(results, checkResult{Name: "coding_agent." + id, Status: "WARN", Remediation: "binary not found: " + binary})
		} else {
			results = append(results, checkResult{Name: "coding_agent." + id, Status: "OK"})
		}
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"checks": results})
	}
	for _, result := range results {
		printCheck(stdout, result)
	}
	return nil
}

func knowledgeCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("knowledge requires add, remove, list, status, or reindex")
	}
	switch args[0] {
	case "add":
		return knowledgeAdd(ctx, args[1:], stdout)
	case "remove":
		return knowledgeRemove(ctx, args[1:], stdout)
	case "list":
		return knowledgeList(ctx, args[1:], stdout)
	case "status":
		return knowledgeStatus(ctx, args[1:], stdout)
	case "reindex":
		return knowledgeReindex(ctx, args[1:], stdout)
	default:
		return usageError("unknown knowledge subcommand " + args[0])
	}
}

func knowledgeAdd(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	title := fs.String("title", "", "document title")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 1 {
		return usageError("knowledge add requires --config and <path>")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	path := fs.Arg(0)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if *title == "" {
		*title = filepath.Base(path)
	}
	docID := "doc_" + rag.HashText(path)
	chunks, err := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 160}).Index(ctx, rag.Document{
		ID:           docID,
		SourceURI:    path,
		Title:        *title,
		Text:         string(content),
		PrivacyClass: rag.DefaultPrivacyClass,
		Metadata:     map[string]string{"operator_source": "knowledge add"},
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "document_id=%s chunks=%d\n", docID, len(chunks))
	return nil
}

func knowledgeRemove(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge remove", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || fs.NArg() != 1 {
		return usageError("knowledge remove requires --config and <source>")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	source := fs.Arg(0)
	result, err := db.SQL.ExecContext(ctx, `DELETE FROM document_chunks WHERE document_id IN (SELECT id FROM documents WHERE id = ? OR source_uri = ?)`, source, source)
	if err != nil {
		return err
	}
	if _, err := db.SQL.ExecContext(ctx, `DELETE FROM documents WHERE id = ? OR source_uri = ?`, source, source); err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	fmt.Fprintf(stdout, "removed_chunks=%d\n", n)
	return nil
}

func knowledgeList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("knowledge list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.SQL.QueryContext(ctx, `SELECT id, source_uri, title, privacy_class, updated_at FROM documents ORDER BY updated_at DESC`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var docs []map[string]string
	for rows.Next() {
		var id, source, title, privacyClass, updated string
		if err := rows.Scan(&id, &source, &title, &privacyClass, &updated); err != nil {
			return err
		}
		docs = append(docs, map[string]string{"id": id, "source_uri": source, "title": title, "privacy_class": privacyClass, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"documents": docs})
	}
	for _, doc := range docs {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", doc["id"], doc["privacy_class"], doc["title"])
	}
	return nil
}

func knowledgeStatus(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge status", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("knowledge status requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	var documents, chunks int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&documents); err != nil {
		return err
	}
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM document_chunks`).Scan(&chunks); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "documents=%d chunks=%d\n", documents, chunks)
	return nil
}

func knowledgeReindex(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("knowledge reindex", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("knowledge reindex requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.SQL.QueryContext(ctx, `SELECT id, source_uri, title, privacy_class FROM documents ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	store := rag.NewSQLiteStore(db.SQL, rag.Chunker{MaxTokens: 160})
	reindexed := 0
	for rows.Next() {
		var id, source, title, privacyClass string
		if err := rows.Scan(&id, &source, &title, &privacyClass); err != nil {
			return err
		}
		content, err := os.ReadFile(source)
		if err != nil {
			continue
		}
		if _, err := store.Index(ctx, rag.Document{ID: id, SourceURI: source, Title: title, Text: string(content), PrivacyClass: privacyClass}); err != nil {
			return err
		}
		reindexed++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "reindexed=%d\n", reindexed)
	return nil
}

func projectCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("project requires create, list, show, update, archive, or use")
	}
	switch args[0] {
	case "create":
		return projectCreate(ctx, args[1:], stdout)
	case "list":
		return projectList(ctx, args[1:], stdout)
	case "show":
		return projectShow(ctx, args[1:], stdout)
	case "update":
		return projectUpdate(ctx, args[1:], stdout)
	case "archive":
		return projectArchive(ctx, args[1:], stdout)
	case "use":
		return projectUse(ctx, args[1:], stdout)
	default:
		return usageError("unknown project subcommand " + args[0])
	}
}

func projectCreate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "project id")
	name := fs.String("name", "", "project name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *name == "" {
		return usageError("project create requires --config and --name")
	}
	if *id == "" {
		generated, err := newID("project")
		if err != nil {
			return err
		}
		*id = generated
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	p := project.Project{ID: *id, Name: *name, PrivacyClass: "local_private", MemoryScope: *id, BudgetPolicy: "{}"}
	if err := (project.Store{DB: db.SQL}).Save(ctx, p); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "project_id=%s name=%s\n", p.ID, p.Name)
	return nil
}

func projectList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("project list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	projects, err := (project.Store{DB: db.SQL}).List(ctx, 100)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"projects": projects})
	}
	for _, item := range projects {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.ID, item.PrivacyClass, item.Name)
	}
	return nil
}

func projectShow(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "project id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("project show requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	p, err := (project.Store{DB: db.SQL}).Get(ctx, *id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, p)
	}
	fmt.Fprintf(stdout, "id=%s\nname=%s\nprivacy_class=%s\nmemory_scope=%s\n", p.ID, p.Name, p.PrivacyClass, p.MemoryScope)
	return nil
}

func projectUpdate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "project id")
	name := fs.String("name", "", "project name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" || *name == "" {
		return usageError("project update requires --config, --id, and --name")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := project.Store{DB: db.SQL}
	p, err := store.Get(ctx, *id)
	if err != nil {
		return err
	}
	p.Name = *name
	if err := store.Save(ctx, p); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "project_id=%s name=%s\n", p.ID, p.Name)
	return nil
}

func projectArchive(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project archive", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "project id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("project archive requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := (project.Store{DB: db.SQL}).Archive(ctx, *id); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "project_id=%s status=archived\n", *id)
	return nil
}

func projectUse(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("project use", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "project id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("project use requires --config and --id")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := (project.Store{DB: db.SQL}).Get(ctx, *id); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.App.DataDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(cfg.App.DataDir, "current_project"), []byte(*id+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "current_project=%s\n", *id)
	return nil
}

func approvalCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("approval requires list, show, approve, or deny")
	}
	if args[0] != "list" && args[0] != "show" && args[0] != "approve" && args[0] != "deny" {
		return usageError("unknown approval subcommand " + args[0])
	}
	fs := flag.NewFlagSet("approval "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "approval id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("approval requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := permission.SQLiteConfirmationStore{DB: db.SQL}
	switch args[0] {
	case "list":
		requests, err := store.List(ctx, "pending", 50)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writePrettyJSON(stdout, map[string]any{"approvals": requests})
		}
		for _, request := range requests {
			fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", request.ID, request.Risk, request.Action, request.Target)
		}
		return nil
	case "show":
		if *id == "" {
			return usageError("approval show requires --id")
		}
		request, ok := store.RequestByID(*id)
		if !ok {
			return sql.ErrNoRows
		}
		if *jsonOutput {
			return writePrettyJSON(stdout, request)
		}
		fmt.Fprintf(stdout, "id=%s\ntask_id=%s\naction=%s\nrisk=%s\ntarget=%s\n", request.ID, request.TaskID, request.Action, request.Risk, request.Target)
		return nil
	case "approve":
		if *id == "" {
			return usageError("approval approve requires --id")
		}
		if err := store.Approve(ctx, *id); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "approval_id=%s status=approved\n", *id)
		return nil
	case "deny":
		if *id == "" {
			return usageError("approval deny requires --id")
		}
		if err := store.Deny(ctx, *id); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "approval_id=%s status=denied\n", *id)
		return nil
	}
	return nil
}

func serveCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("serve requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	rt, err := runtime.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()
	confirmations := permission.SQLiteConfirmationStore{DB: rt.Storage.SQL}
	eventSource, _ := rt.Events.(api.EventSource)
	server := api.NewServerWithRunner(rt.Storage, eventSource, confirmations, cfg.Agent.Leader.ModelID, rt)
	server.Security = api.NewSecurityPolicy(cfg.Security.API)
	server.Triggers = trigger.Store{DB: rt.Storage.SQL}
	server.EventStore = agentEvent.Store{DB: rt.Storage.SQL}
	server.Notifications = notification.Store{DB: rt.Storage.SQL}
	for _, model := range cfg.Models.Registry {
		server.ModelRegistry = append(server.ModelRegistry, model.ID)
	}
	metrics := observability.NewRegistry()
	mux := http.NewServeMux()
	mux.Handle("/", metricsMiddleware(metrics, server.Handler()))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, http.StatusOK, observability.Health())
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		report := observability.Readiness(r.Context(), rt.Storage.SQL, true, readinessDependencies(cfg))
		status := http.StatusOK
		if !report.Ready {
			status = http.StatusServiceUnavailable
		}
		writeServeJSON(w, status, report)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		writeServeJSON(w, http.StatusOK, metrics.Snapshot(r.Context(), rt.Storage.SQL))
	})
	addr := cfg.Server.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:8787"
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: cfg.Server.RequestTimeout.Duration}
	fmt.Fprintf(stdout, "listening=%s\n", listener.Addr().String())
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func metricsMiddleware(metrics *observability.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics.Inc("http_requests_total", 1)
		metrics.AddGauge("active_requests", 1)
		defer metrics.AddGauge("active_requests", -1)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(recorder, r)
		metrics.SetGauge("last_request_duration_ms", float64(time.Since(started).Milliseconds()))
		if recorder.status >= 500 {
			metrics.Inc("errors_total", 1)
		}
	})
}

func readinessDependencies(cfg config.Config) []observability.ComponentStatus {
	dependencies := []observability.ComponentStatus{}
	for id, provider := range cfg.Models.Providers {
		status := "OK"
		remediation := ""
		if !provider.IsEnabled() {
			status = "WARN"
			remediation = "provider disabled"
		} else if provider.BaseURL == "" && provider.BaseURLEnv != "" {
			if _, ok := config.EnvValue(provider.BaseURLEnv); !ok {
				status = "WARN"
				remediation = "set " + provider.BaseURLEnv + " to check endpoint health"
			}
		}
		dependencies = append(dependencies, observability.ComponentStatus{Name: "model_provider." + id, Status: status, Remediation: remediation})
	}
	if cfg.Browser.Enabled {
		dependencies = append(dependencies, observability.ComponentStatus{Name: "browser", Status: "OK"})
	} else {
		dependencies = append(dependencies, observability.ComponentStatus{Name: "browser", Status: "WARN", Remediation: "browser disabled"})
	}
	if len(cfg.Reliability.Disk.Paths) > 0 {
		disk := reliability.CheckDiskPressure(cfg.Reliability.Disk)
		status := "OK"
		remediation := ""
		if disk.Status == reliability.DiskSoft {
			status = "WARN"
			remediation = "disk soft pressure; cleanup temporary artifacts"
		} else if disk.Status == reliability.DiskHard {
			status = "FAIL"
			remediation = "disk hard pressure; stop non-critical write-heavy tasks"
		} else if disk.Status == reliability.DiskUnknown {
			status = "WARN"
			remediation = "disk usage could not be measured"
		}
		dependencies = append(dependencies, observability.ComponentStatus{Name: "disk_pressure", Status: status, Remediation: remediation})
	}
	for id, backend := range cfg.CodingAgents.Backends {
		status := "OK"
		remediation := ""
		if !backend.IsEnabled() {
			status = "WARN"
			remediation = "backend disabled"
		}
		dependencies = append(dependencies, observability.ComponentStatus{Name: "coding_agent." + id, Status: status, Remediation: remediation})
	}
	return dependencies
}

func writeServeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func backupCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("backup requires create or restore")
	}
	switch args[0] {
	case "create":
		return backupCreate(ctx, args[1:], stdout)
	case "restore":
		return backupRestore(ctx, args[1:], stdout)
	default:
		return usageError("unknown backup subcommand " + args[0])
	}
}

func backupCreate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	output := fs.String("output", "", "backup zip path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("backup create requires --config")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.WALCheckpoint(ctx, db.SQL); err != nil {
		return err
	}
	if *output == "" {
		*output = filepath.Join(cfg.App.DataDir, "backup-"+time.Now().UTC().Format("20060102T150405Z")+".zip")
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(*output)
	if err != nil {
		return err
	}
	defer file.Close()
	zipper := zip.NewWriter(file)
	defer zipper.Close()
	if err := addFileToZip(zipper, cfg.Storage.SQLite.Path, "personal-agent.db"); err != nil {
		return err
	}
	configContent, err := os.ReadFile(*configPath)
	if err != nil {
		return err
	}
	if err := addBytesToZip(zipper, "config.sanitized.yaml", configContent); err != nil {
		return err
	}
	metadata, _ := json.MarshalIndent(map[string]string{"created_at": time.Now().UTC().Format(time.RFC3339), "schema": "p12-backup-v1"}, "", "  ")
	if err := addBytesToZip(zipper, "metadata.json", metadata); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "backup=%s\n", *output)
	return nil
}

func backupRestore(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	input := fs.String("input", "", "backup zip path")
	dryRun := fs.Bool("dry-run", true, "validate without restoring")
	force := fs.Bool("force", false, "overwrite existing database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *input == "" {
		return usageError("backup restore requires --config and --input")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	reader, err := zip.OpenReader(*input)
	if err != nil {
		return err
	}
	defer reader.Close()
	hasDB := false
	for _, file := range reader.File {
		if file.Name == "personal-agent.db" {
			hasDB = true
			break
		}
	}
	if !hasDB {
		return errors.New("backup missing personal-agent.db")
	}
	if *dryRun {
		fmt.Fprintln(stdout, "restore=dry-run status=ok")
		return nil
	}
	if !*force {
		if _, err := os.Stat(cfg.Storage.SQLite.Path); err == nil {
			return errors.New("database exists; rerun with --force to restore")
		}
	}
	for _, file := range reader.File {
		if file.Name != "personal-agent.db" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		if err := os.MkdirAll(filepath.Dir(cfg.Storage.SQLite.Path), 0o755); err != nil {
			return err
		}
		out, err := os.Create(cfg.Storage.SQLite.Path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "restore=%s status=ok\n", cfg.Storage.SQLite.Path)
	return nil
}

func retentionCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "cleanup" {
		return usageError("retention requires cleanup")
	}
	fs := flag.NewFlagSet("retention cleanup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	olderThan := fs.Duration("older-than", 30*24*time.Hour, "delete temporary records older than duration")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("retention cleanup requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	deleted, err := storage.DeleteEventsBefore(ctx, db.SQL, time.Now().Add(-*olderThan))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "deleted_events=%d\n", deleted)
	return nil
}

func storageCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("storage requires integrity, vacuum, or checkpoint")
	}
	fs := flag.NewFlagSet("storage "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("storage command requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	switch args[0] {
	case "integrity":
		result, err := storage.IntegrityCheck(ctx, db.SQL)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "integrity=%s\n", result)
	case "vacuum":
		if err := storage.Vacuum(ctx, db.SQL); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "vacuum=ok")
	case "checkpoint":
		if err := storage.WALCheckpoint(ctx, db.SQL); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "checkpoint=ok")
	default:
		return usageError("unknown storage subcommand " + args[0])
	}
	return nil
}

func pathWritableCheck(name string, path string) checkResult {
	if path == "" {
		return checkResult{Name: name, Status: "WARN", Remediation: "path not configured"}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return checkResult{Name: name, Status: "FAIL", Remediation: err.Error()}
	}
	var random [4]byte
	_, _ = rand.Read(random[:])
	probe := filepath.Join(path, ".pachat-write-check-"+hex.EncodeToString(random[:]))
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return checkResult{Name: name, Status: "FAIL", Remediation: err.Error()}
	}
	_ = os.Remove(probe)
	return checkResult{Name: name, Status: "OK"}
}

func printCheck(stdout io.Writer, result checkResult) {
	if result.Remediation == "" {
		fmt.Fprintf(stdout, "%s\t%s\n", result.Status, result.Name)
		return
	}
	fmt.Fprintf(stdout, "%s\t%s\t%s\n", result.Status, result.Name, result.Remediation)
}

func writePrettyJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func addFileToZip(zipper *zip.Writer, path string, name string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	writer, err := zipper.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}

func addBytesToZip(zipper *zip.Writer, name string, content []byte) error {
	writer, err := zipper.Create(name)
	if err != nil {
		return err
	}
	_, err = writer.Write(content)
	return err
}
