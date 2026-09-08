package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"agent/internal/runtime"
	"agent/internal/skill"
	"agent/internal/storage"
)

func skillCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("skill requires list, show, import, validate, enable, disable, run, or versions")
	}
	switch args[0] {
	case "list":
		return skillList(ctx, args[1:], stdout)
	case "show":
		return skillShow(ctx, args[1:], stdout)
	case "import":
		return skillImport(ctx, args[1:], stdout)
	case "validate":
		return skillValidate(ctx, args[1:], stdout)
	case "enable", "disable":
		return skillSetStatus(ctx, args[0], args[1:], stdout)
	case "run":
		return skillRun(ctx, args[1:], stdout)
	case "versions":
		return skillVersions(ctx, args[1:], stdout)
	default:
		return usageError("unknown skill subcommand " + args[0])
	}
}

func skillList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("skill list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	items, err := (skill.Store{DB: db.SQL}).List(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"skills": items})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.ID, item.ActiveVersion, item.Status, item.Name)
	}
	return nil
}

func skillShow(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "skill id")
	version := fs.String("version", "", "skill version")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("skill show requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	manifest, err := (skill.Store{DB: db.SQL}).GetManifest(ctx, *id, *version)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, manifest)
	}
	fmt.Fprintf(stdout, "id=%s\nversion=%s\nstatus=%s\nname=%s\ndescription=%s\n", manifest.ID, manifest.Version, manifest.Status, manifest.Name, manifest.Description)
	return nil
}

func skillImport(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	path := fs.String("path", "", "skill manifest path")
	source := fs.String("source", "local", "skill source")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *path == "" {
		return usageError("skill import requires --config and --path")
	}
	manifest, err := loadAndValidateSkill(ctx, *configPath, *path)
	if err != nil {
		return err
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := (skill.Store{DB: db.SQL}).Import(ctx, manifest, *source); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "skill_id=%s version=%s status=imported\n", manifest.ID, manifest.Version)
	return nil
}

func skillValidate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	path := fs.String("path", "", "skill manifest path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return usageError("skill validate requires --path")
	}
	if _, err := loadAndValidateSkill(ctx, *configPath, *path); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "status=OK")
	return nil
}

func skillSetStatus(ctx context.Context, command string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "skill id")
	version := fs.String("version", "", "skill version")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("skill " + command + " requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := skill.Store{DB: db.SQL}
	if command == "enable" {
		err = store.Enable(ctx, *id, *version)
	} else {
		err = store.Disable(ctx, *id)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "skill_id=%s status=%sd\n", *id, command)
	return nil
}

func skillVersions(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill versions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "skill id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("skill versions requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	versions, err := (skill.Store{DB: db.SQL}).Versions(ctx, *id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"versions": versions})
	}
	for _, item := range versions {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.Manifest.ID, item.Manifest.Version, item.Checksum)
	}
	return nil
}

func skillRun(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("skill run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "skill id")
	input := fs.String("input", "", "skill input")
	projectID := fs.String("project", "", "project id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("skill run requires --config and --id")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := skill.Store{DB: db.SQL}
	manifest, err := store.GetManifest(ctx, *id, "")
	if err != nil {
		return err
	}
	manifest.Status = skill.StatusActive
	rt := runtime.NewLocal(cfg, db, nil)
	if err := rt.Skills.Add(manifest, rt.Capabilities); err != nil {
		return err
	}
	taskInput := strings.TrimSpace(*input)
	if taskInput == "" {
		taskInput = manifest.Name
	} else if !strings.Contains(taskInput, manifest.Name) && !strings.Contains(taskInput, manifest.Description) {
		taskInput = manifest.Name + " " + taskInput
	}
	taskID, err := newID("task")
	if err != nil {
		return err
	}
	if err := db.CreateTask(ctx, storage.Task{ID: taskID, Title: taskInput, Input: taskInput, Status: "running", LeaderModelID: cfg.Agent.Leader.ModelID, PrivacyClass: "local_private"}); err != nil {
		return err
	}
	result, err := rt.Run(ctx, runtime.RunRequest{TaskID: taskID, Input: taskInput, ProjectID: *projectID, LeaderModelID: cfg.Agent.Leader.ModelID, PrivacyClass: "local_private"})
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, result)
	}
	fmt.Fprintf(stdout, "skill_id=%s\ntask_id=%s\nstatus=completed\nconfidence=%.2f\n\nanswer:\n%s\n", manifest.ID, taskID, result.Confidence, result.Answer)
	return nil
}

func loadAndValidateSkill(ctx context.Context, configPath string, path string) (skill.Manifest, error) {
	manifest, err := skill.Load(path)
	if err != nil {
		return skill.Manifest{}, err
	}
	if configPath == "" {
		return manifest, skill.Validate(manifest, nil)
	}
	cfg, db, err := openConfiguredDB(ctx, configPath)
	if err != nil {
		return skill.Manifest{}, err
	}
	defer db.Close()
	rt := runtime.NewLocal(cfg, db, nil)
	if err := skill.Validate(manifest, rt.Capabilities); err != nil {
		return skill.Manifest{}, err
	}
	return manifest, nil
}
