package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"agent/internal/agent"
	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/notification"
	"agent/internal/permission"
	"agent/internal/project"
	agentruntime "agent/internal/runtime"
	"agent/internal/skill"
	"agent/internal/storage"
	"agent/internal/trigger"
	"gopkg.in/yaml.v3"
)

type recoveryDrillReport struct {
	Status            string           `json:"status"`
	StartedAt         time.Time        `json:"started_at"`
	CompletedAt       time.Time        `json:"completed_at"`
	WorkDir           string           `json:"work_dir"`
	BackupPath        string           `json:"backup_path"`
	SourceDBDestroyed bool             `json:"source_db_destroyed"`
	RestoredIntegrity string           `json:"restored_integrity"`
	Verified          map[string]bool  `json:"verified"`
	Counts            map[string]int64 `json:"counts"`
	Failures          []string         `json:"failures,omitempty"`
}

func releaseRecoveryDrillCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release recovery-drill", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	workDir := fs.String("work-dir", "", "disposable drill directory")
	outputPath := fs.String("output", "", "write JSON report path")
	jsonOutput := fs.Bool("json", false, "print JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("release recovery-drill requires --config")
	}
	template, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	report, err := runRecoveryDrill(ctx, template, *workDir, *outputPath)
	if *jsonOutput {
		if writeErr := writePrettyJSON(stdout, report); writeErr != nil {
			return writeErr
		}
		return err
	}
	fmt.Fprintf(stdout, "status=%s\nbackup=%s\nsource_db_destroyed=%t\nintegrity=%s\n", report.Status, report.BackupPath, report.SourceDBDestroyed, report.RestoredIntegrity)
	for name, ok := range report.Verified {
		fmt.Fprintf(stdout, "verified_%s=%t\n", name, ok)
	}
	for _, failure := range report.Failures {
		fmt.Fprintf(stdout, "failure=%s\n", failure)
	}
	return err
}

func runRecoveryDrill(ctx context.Context, template config.Config, workDir string, outputPath string) (recoveryDrillReport, error) {
	started := time.Now().UTC()
	if workDir == "" {
		dir, err := os.MkdirTemp("", "pachat-recovery-drill-*")
		if err != nil {
			return recoveryDrillReport{}, err
		}
		workDir = dir
	} else if err := os.MkdirAll(workDir, 0o755); err != nil {
		return recoveryDrillReport{}, err
	}
	sourceConfigPath := filepath.Join(workDir, "source-config.yaml")
	restoreConfigPath := filepath.Join(workDir, "restore-config.yaml")
	backupPath := filepath.Join(workDir, "fixture-backup.zip")
	sourceDBPath := filepath.Join(workDir, "source.db")
	restoreDBPath := filepath.Join(workDir, "restore.db")
	sourceCfg := recoveryDrillConfig(template, workDir, sourceDBPath)
	restoreCfg := recoveryDrillConfig(template, workDir, restoreDBPath)
	if err := writeConfigFile(sourceConfigPath, sourceCfg); err != nil {
		return recoveryDrillReport{}, err
	}
	if err := writeConfigFile(restoreConfigPath, restoreCfg); err != nil {
		return recoveryDrillReport{}, err
	}
	report := recoveryDrillReport{
		StartedAt:  started,
		WorkDir:    workDir,
		BackupPath: backupPath,
		Verified:   map[string]bool{},
		Counts:     map[string]int64{},
	}
	if err := seedRecoveryFixture(ctx, sourceCfg); err != nil {
		report.Failures = append(report.Failures, err.Error())
		return finishRecoveryDrillReport(report, outputPath)
	}
	if err := backupCreate(ctx, []string{"--config", sourceConfigPath, "--output", backupPath}, io.Discard); err != nil {
		report.Failures = append(report.Failures, err.Error())
		return finishRecoveryDrillReport(report, outputPath)
	}
	if err := removeSQLiteFiles(sourceDBPath); err != nil {
		report.Failures = append(report.Failures, err.Error())
		return finishRecoveryDrillReport(report, outputPath)
	}
	if _, err := os.Stat(sourceDBPath); os.IsNotExist(err) {
		report.SourceDBDestroyed = true
	}
	if err := backupRestore(ctx, []string{"--config", restoreConfigPath, "--input", backupPath}, io.Discard); err != nil {
		report.Failures = append(report.Failures, "dry-run restore: "+err.Error())
		return finishRecoveryDrillReport(report, outputPath)
	}
	if err := backupRestore(ctx, []string{"--config", restoreConfigPath, "--input", backupPath, "--dry-run=false", "--force"}, io.Discard); err != nil {
		report.Failures = append(report.Failures, "restore: "+err.Error())
		return finishRecoveryDrillReport(report, outputPath)
	}
	integrity, verified, counts, err := verifyRecoveryFixture(ctx, restoreDBPath)
	report.RestoredIntegrity = integrity
	report.Verified = verified
	report.Counts = counts
	if err != nil {
		report.Failures = append(report.Failures, err.Error())
	}
	return finishRecoveryDrillReport(report, outputPath)
}

func recoveryDrillConfig(template config.Config, workDir string, dbPath string) config.Config {
	cfg := template
	cfg.App.DataDir = workDir
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.SQLite.Path = dbPath
	for id, provider := range cfg.Models.Providers {
		provider.Enabled = recoveryBoolPtr(false)
		cfg.Models.Providers[id] = provider
	}
	cfg.Agent.Planner.Enabled = recoveryBoolPtr(false)
	for role, subagent := range cfg.Agent.SubAgents {
		subagent.ModelID = ""
		cfg.Agent.SubAgents[role] = subagent
	}
	cfg.RAG.Vector.Enabled = false
	cfg.RAG.Reranker.Enabled = false
	cfg.Browser.Enabled = false
	cfg.MCP.Servers = map[string]config.MCPServerConfig{}
	cfg.Tools.Allowlist = nil
	for id, backend := range cfg.CodingAgents.Backends {
		backend.Enabled = recoveryBoolPtr(false)
		cfg.CodingAgents.Backends[id] = backend
	}
	cfg.Proactive.Enabled = recoveryBoolPtr(false)
	return cfg
}

func seedRecoveryFixture(ctx context.Context, cfg config.Config) error {
	rt, err := agentruntime.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer rt.Close()
	if err := (project.Store{DB: rt.Storage.SQL}).Save(ctx, project.Project{ID: "drill-project", Name: "Recovery Drill", PrivacyClass: "local_private"}); err != nil {
		return err
	}
	if err := (skill.Store{DB: rt.Storage.SQL}).Import(ctx, skill.Manifest{
		ID:          "drill-skill",
		Version:     "1.0.0",
		Name:        "Recovery Drill Skill",
		Description: "Recovery drill fixture",
		Status:      skill.StatusActive,
		Requires:    skill.Requirements{Capabilities: []string{"memory.search"}},
		Privacy:     skill.PrivacyPolicy{MaxExternalTrust: "local_private"},
		Workflow:    skill.Workflow{Nodes: []skill.Node{{ID: "memory", Capability: "memory.search", Role: string(agent.RoleMemory)}}},
	}, "recovery-drill"); err != nil {
		return err
	}
	if err := memory.NewStore(rt.Storage.SQL).Append(ctx, memory.EpisodicEvent{ID: "drill-memory", EventType: "drill", Summary: "recovery drill memory"}); err != nil {
		return err
	}
	if err := rt.Storage.CreateTask(ctx, storage.Task{ID: "drill-task", Title: "recovery drill task", Input: "recover me", Status: "running", PrivacyClass: "local_private"}); err != nil {
		return err
	}
	if _, err := rt.Run(ctx, agentruntime.RunRequest{TaskID: "drill-task", Input: "recover me", PrivacyClass: "local_private"}); err != nil {
		return err
	}
	if err := (trigger.Store{DB: rt.Storage.SQL}).Put(ctx, trigger.Trigger{ID: "drill-trigger", ProjectID: "drill-project", Type: trigger.TypeManual, Enabled: true}); err != nil {
		return err
	}
	if err := (notification.Store{DB: rt.Storage.SQL}).Put(ctx, notification.Notification{ID: "drill-notification", Title: "Recovery drill", DedupKey: "drill-notification"}); err != nil {
		return err
	}
	_, err = (permission.SQLiteConfirmationStore{DB: rt.Storage.SQL}).Request(ctx, permission.Request{ID: "drill-approval", TaskID: "drill-task", NodeID: "drill-node", Action: permission.ActionExternalSideEffect, Target: "fixture", Risk: permission.RiskHigh, ProposedEffect: "fixture approval"})
	return err
}

func verifyRecoveryFixture(ctx context.Context, dbPath string) (string, map[string]bool, map[string]int64, error) {
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		return "", nil, nil, err
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		return "", nil, nil, err
	}
	integrity, err := storage.IntegrityCheck(ctx, db.SQL)
	if err != nil {
		return "", nil, nil, err
	}
	counts := map[string]int64{
		"projects":      countTable(ctx, db.SQL, "projects"),
		"skills":        countTable(ctx, db.SQL, "skills"),
		"memory":        countTable(ctx, db.SQL, "memory_episodic"),
		"workflows":     countTable(ctx, db.SQL, "workflow_runs"),
		"triggers":      countTable(ctx, db.SQL, "triggers"),
		"notifications": countTable(ctx, db.SQL, "notification_inbox"),
		"approvals":     countTable(ctx, db.SQL, "confirmation_requests"),
	}
	verified := map[string]bool{}
	for name, count := range counts {
		verified[name] = count > 0
	}
	verified["integrity"] = integrity == "ok"
	for name, ok := range verified {
		if !ok {
			return integrity, verified, counts, fmt.Errorf("recovery drill verification failed for %s", name)
		}
	}
	return integrity, verified, counts, nil
}

func finishRecoveryDrillReport(report recoveryDrillReport, outputPath string) (recoveryDrillReport, error) {
	report.CompletedAt = time.Now().UTC()
	if len(report.Failures) > 0 {
		report.Status = "fail"
	} else {
		report.Status = "pass"
	}
	if outputPath != "" {
		if err := writeRecoveryReport(outputPath, report); err != nil {
			return report, err
		}
	}
	if len(report.Failures) > 0 {
		return report, fmt.Errorf("recovery drill failed")
	}
	return report, nil
}

func writeConfigFile(path string, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, payload, 0o600)
}

func writeRecoveryReport(path string, report recoveryDrillReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func removeSQLiteFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func countTable(ctx context.Context, db *sql.DB, table string) int64 {
	var count int64
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
		return 0
	}
	return count
}

func recoveryBoolPtr(value bool) *bool {
	return &value
}
