package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/release"
	"agent/internal/runtime"
	"agent/internal/storage"
	"agent/internal/version"
	"agent/internal/workflow"
	"github.com/chzyer/readline"
)

type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
}

func Run(ctx context.Context, args []string, stdout io.Writer) error {
	return RunWithIO(ctx, args, IO{Stdin: strings.NewReader(""), Stdout: stdout})
}

func RunWithIO(ctx context.Context, args []string, ioStreams IO) error {
	if len(args) == 0 {
		return usageError("missing command")
	}
	if ioStreams.Stdout == nil {
		ioStreams.Stdout = io.Discard
	}
	if ioStreams.Stdin == nil {
		ioStreams.Stdin = strings.NewReader("")
	}
	switch args[0] {
	case "version":
		return versionCommand(args[1:], ioStreams.Stdout)
	case "release":
		return releaseCommand(ctx, args[1:], ioStreams.Stdout)
	case "init":
		return initCommand(ctx, args[1:], ioStreams)
	case "config":
		return configCommand(ctx, args[1:], ioStreams.Stdout)
	case "doctor":
		return doctorCommand(ctx, args[1:], ioStreams.Stdout)
	case "run":
		return runTask(ctx, args[1:], ioStreams.Stdout)
	case "chat":
		return chat(ctx, args[1:], ioStreams)
	case "task":
		return taskCommand(ctx, args[1:], ioStreams.Stdout)
	case "capability":
		return capabilityCommand(ctx, args[1:], ioStreams.Stdout)
	case "skill":
		return skillCommand(ctx, args[1:], ioStreams.Stdout)
	case "workflow":
		return workflowCommand(ctx, args[1:], ioStreams.Stdout)
	case "knowledge":
		return knowledgeCommand(ctx, args[1:], ioStreams.Stdout)
	case "project":
		return projectCommand(ctx, args[1:], ioStreams.Stdout)
	case "approval":
		return approvalCommand(ctx, args[1:], ioStreams.Stdout)
	case "notification":
		return notificationCommand(ctx, args[1:], ioStreams.Stdout)
	case "model":
		return modelCommand(ctx, args[1:], ioStreams.Stdout)
	case "mcp":
		return mcpCommand(ctx, args[1:], ioStreams.Stdout)
	case "watcher":
		return watcherCommand(ctx, args[1:], ioStreams.Stdout)
	case "trigger":
		return triggerCommand(ctx, args[1:], ioStreams.Stdout)
	case "serve":
		return serveCommand(ctx, args[1:], ioStreams.Stdout)
	case "backup":
		return backupCommand(ctx, args[1:], ioStreams.Stdout)
	case "export":
		return exportCommand(ctx, args[1:], ioStreams.Stdout)
	case "import":
		return importCommand(ctx, args[1:], ioStreams.Stdout)
	case "retention":
		return retentionCommand(ctx, args[1:], ioStreams.Stdout)
	case "storage":
		return storageCommand(ctx, args[1:], ioStreams.Stdout)
	default:
		return usageError("unknown command " + args[0])
	}
}

func versionCommand(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	info := version.Current()
	if *jsonOutput {
		return writePrettyJSON(stdout, info)
	}
	_, err := fmt.Fprintf(stdout, "version=%s\ncommit=%s\nbuild_date=%s\ngo_version=%s\nconfig_schema=%s\ndatabase_schema=%s\nskill_manifest=%s\napi_version=%s\n",
		info.Version, info.Commit, info.BuildDate, info.GoVersion, info.ConfigSchema, info.DatabaseSchema, info.SkillManifest, info.APIVersion)
	return err
}

func releaseCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("release requires check or soak")
	}
	switch args[0] {
	case "check":
		return releaseCheckCommand(ctx, args[1:], stdout)
	case "soak":
		return releaseSoakCommand(ctx, args[1:], stdout)
	case "perf-baseline":
		return releasePerformanceBaselineCommand(ctx, args[1:], stdout)
	case "recovery-drill":
		return releaseRecoveryDrillCommand(ctx, args[1:], stdout)
	default:
		return usageError("release requires check, soak, perf-baseline, or recovery-drill")
	}
}

func releaseCheckCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	quick := fs.Bool("quick", false, "skip release matrix and checksum generation")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := release.RunChecks(ctx, release.Options{Quick: *quick})
	if *jsonOutput {
		if writeErr := writePrettyJSON(stdout, report); writeErr != nil {
			return writeErr
		}
		return err
	}
	for _, check := range report.Checks {
		if check.Detail == "" {
			fmt.Fprintf(stdout, "%s\t%s\n", check.Status, check.Name)
			continue
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", check.Status, check.Name, check.Detail)
	}
	return err
}

func releaseSoakCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release soak", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	duration := fs.Duration("duration", 30*time.Second, "soak duration")
	interval := fs.Duration("interval", time.Second, "sample interval")
	output := fs.String("output", "", "write JSON report path")
	offline := fs.Bool("offline", true, "disable external model/browser/CLI backends")
	maxHeapGrowth := fs.Uint64("max-heap-growth-bytes", 64*1024*1024, "maximum heap growth")
	maxGoroutineGrowth := fs.Int("max-goroutine-growth", 16, "maximum goroutine growth")
	maxSQLiteGrowth := fs.Int64("max-sqlite-growth-bytes", 64*1024*1024, "maximum SQLite growth")
	jsonOutput := fs.Bool("json", false, "print JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("release soak requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	report, err := release.RunSoak(ctx, release.SoakOptions{
		Config:               cfg,
		Duration:             *duration,
		Interval:             *interval,
		Offline:              *offline,
		OutputPath:           *output,
		MaxHeapGrowthBytes:   *maxHeapGrowth,
		MaxGoroutineGrowth:   *maxGoroutineGrowth,
		MaxSQLiteGrowthBytes: *maxSQLiteGrowth,
	})
	if *jsonOutput {
		if writeErr := writePrettyJSON(stdout, report); writeErr != nil {
			return writeErr
		}
		return err
	}
	fmt.Fprintf(stdout, "status=%s\niterations=%d\nheap_growth_bytes=%d\ngoroutine_growth=%d\nsqlite_growth_bytes=%d\nterminal_workflows=%d\nnon_terminal_workflows=%d\nnotifications=%d\nduplicate_notifications=%d\nchild_process_leaks=%d\nworktree_leaks=%d\nbrowser_artifact_leaks=%d\n",
		report.Status, report.Iterations, report.HeapGrowthBytes, report.GoroutineGrowth, report.SQLiteGrowthBytes, report.TerminalWorkflows, report.NonTerminalWorkflows, report.NotificationCount, report.DuplicateNotifications, report.ChildProcessLeaks, report.WorktreeLeaks, report.BrowserArtifactLeaks)
	if *output != "" {
		fmt.Fprintf(stdout, "report=%s\n", *output)
	}
	for _, failure := range report.Failures {
		fmt.Fprintf(stdout, "failure=%s\n", failure)
	}
	return err
}

func releasePerformanceBaselineCommand(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("release perf-baseline", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	workDir := fs.String("work-dir", "", "disposable baseline directory")
	output := fs.String("output", "", "write JSON report path")
	offline := fs.Bool("offline", true, "disable external model/browser/CLI backends")
	concurrency := fs.Int("concurrency", 2, "concurrent workflow count")
	jsonOutput := fs.Bool("json", false, "print JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("release perf-baseline requires --config")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *workDir == "" {
		*workDir, err = os.MkdirTemp("", "pachat-perf-baseline-*")
		if err != nil {
			return err
		}
	} else if err := os.MkdirAll(*workDir, 0o755); err != nil {
		return err
	}
	cfg.App.DataDir = *workDir
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.SQLite.Path = filepath.Join(*workDir, "performance.db")
	report, err := release.RunPerformanceBaseline(ctx, release.PerformanceOptions{
		Config:      cfg,
		Offline:     *offline,
		OutputPath:  *output,
		Concurrency: *concurrency,
	})
	if *jsonOutput {
		if writeErr := writePrettyJSON(stdout, report); writeErr != nil {
			return writeErr
		}
		return err
	}
	fmt.Fprintf(stdout, "status=%s\nstartup_latency_ms=%d\nlocal_query_latency_ms=%d\nhybrid_rag_latency_ms=%d\nworkflow_dispatch_latency_ms=%d\nheap_alloc_bytes=%d\nconcurrent_workflows_completed=%d\nconcurrent_workflows_requested=%d\nconcurrent_workflow_latency_ms=%d\n",
		report.Status, report.StartupLatencyMS, report.LocalQueryLatencyMS, report.HybridRAGLatencyMS, report.WorkflowDispatchLatencyMS, report.HeapAllocBytes, report.ConcurrentWorkflowsCompleted, report.ConcurrentWorkflowsRequested, report.ConcurrentWorkflowLatencyMS)
	if *output != "" {
		fmt.Fprintf(stdout, "report=%s\n", *output)
	}
	for _, failure := range report.Failures {
		fmt.Fprintf(stdout, "failure=%s\n", failure)
	}
	return err
}

func capabilityCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "health") {
		return usageError("capability requires list or health")
	}
	fs := flag.NewFlagSet("capability", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("capability requires --config")
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
	for _, item := range rt.Capabilities.List("") {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\n", item.ID, item.Kind, item.Health, boolText(item.Enabled))
	}
	return nil
}

func boolText(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func workflowCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("workflow requires list, show, pause, resume, cancel, or events")
	}
	if args[0] == "list" {
		return workflowList(ctx, args[1:], stdout)
	}
	if args[0] == "events" {
		return workflowEvents(ctx, args[1:], stdout)
	}
	if len(args) < 1 || (args[0] != "show" && args[0] != "pause" && args[0] != "resume" && args[0] != "cancel") {
		return usageError("unknown workflow subcommand")
	}
	fs := flag.NewFlagSet("workflow "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "workflow id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("workflow command requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := workflow.Store{DB: db.SQL}
	if args[0] == "show" {
		run, err := store.Get(ctx, *id)
		if err != nil {
			return err
		}
		if *jsonOutput {
			return writePrettyJSON(stdout, run)
		}
		_, err = fmt.Fprintf(stdout, "id=%s\nstatus=%s\ntask_id=%s\nproject_id=%s\nskill=%s@%s\n", run.ID, run.Status, run.TaskID, run.ProjectID, run.SkillID, run.SkillVersion)
		return err
	}
	status := map[string]workflow.Status{"pause": workflow.StatusPaused, "resume": workflow.StatusRunning, "cancel": workflow.StatusCancelled}[args[0]]
	if err := store.UpdateStatus(ctx, *id, status); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "workflow_id=%s status=%s\n", *id, status)
	return err
}

func workflowList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("workflow list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("workflow list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	runs, err := (workflow.Store{DB: db.SQL}).List(ctx, 20)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"workflows": runs})
	}
	for _, run := range runs {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", run.ID, run.Status, run.TaskID)
	}
	return nil
}

func workflowEvents(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("workflow events", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "workflow id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("workflow events requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.SQL.QueryContext(ctx, `SELECT node_id, status, evidence_ids_json, result_ref, usage_json, created_at FROM workflow_checkpoints WHERE workflow_id = ? ORDER BY created_at ASC`, *id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var events []map[string]string
	for rows.Next() {
		event := map[string]string{}
		var nodeID, status, evidence, resultRef, usage, createdAt string
		if err := rows.Scan(&nodeID, &status, &evidence, &resultRef, &usage, &createdAt); err != nil {
			return err
		}
		event["node_id"] = nodeID
		event["status"] = status
		event["evidence_ids_json"] = evidence
		event["result_ref"] = resultRef
		event["usage_json"] = usage
		event["created_at"] = createdAt
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"events": events})
	}
	for _, event := range events {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", event["created_at"], event["node_id"], event["status"])
	}
	return nil
}

func runTask(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskInput := fs.String("task", "", "task input")
	projectID := fs.String("project", "", "project id")
	longTask := fs.Bool("long", false, "persist task as running for long-task tracking")
	stream := fs.Bool("stream", false, "stream task progress")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("run requires --config")
	}
	if strings.TrimSpace(*taskInput) == "" {
		return usageError("run requires --task")
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
	if *stream {
		rt.Events = newStreamingEvidenceBus(rt.Events, stdout)
	}

	taskID, err := newID("task")
	if err != nil {
		return err
	}
	status := "running"
	if *longTask {
		if err := rt.Storage.CreateTask(ctx, storage.Task{
			ID:            taskID,
			Title:         summarizeTitle(*taskInput),
			Input:         *taskInput,
			Status:        status,
			LeaderModelID: cfg.Agent.Leader.ModelID,
			PrivacyClass:  "local_private",
		}); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "task_id=%s status=running\n", taskID)
		return err
	}
	if err := rt.Storage.CreateTask(ctx, storage.Task{
		ID:            taskID,
		Title:         summarizeTitle(*taskInput),
		Input:         *taskInput,
		Status:        status,
		LeaderModelID: cfg.Agent.Leader.ModelID,
		PrivacyClass:  "local_private",
	}); err != nil {
		return err
	}
	if *stream {
		streamRunStarted(stdout, taskID)
	}
	result, err := rt.Run(ctx, runtime.RunRequest{
		TaskID:        taskID,
		Input:         *taskInput,
		ProjectID:     strings.TrimSpace(*projectID),
		PrivacyClass:  "local_private",
		LeaderModelID: cfg.Agent.Leader.ModelID,
	})
	if err != nil {
		_ = rt.Storage.FailTask(ctx, taskID, "runtime_failed", err.Error())
		if *stream {
			streamRunFailed(stdout, err)
		}
		return err
	}
	if *stream {
		return streamRunCompleted(ctx, stdout, rt.Workflows, taskID, result)
	}
	_, err = fmt.Fprintf(stdout, "task_id=%s\nstatus=completed\nconfidence=%.2f\nremote_tokens=%d\n\nanswer:\n%s\n",
		taskID, result.Confidence, result.Usage.RemoteTokens, result.Answer)
	return err
}

func chat(ctx context.Context, args []string, ioStreams IO) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("chat requires --config")
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

	mem := memory.NewStore(rt.Storage.SQL)
	sessionID, err := newID("chat")
	if err != nil {
		return err
	}

	fmt.Fprintln(ioStreams.Stdout, "pachat interactive mode. Type /help, /memory, /exit, or /quit. Press Tab after / for command completion.")
	reader, err := newChatLineReader(ioStreams)
	if err != nil {
		return err
	}
	defer reader.Close()

	for {
		line, err := reader.ReadLine()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, readline.ErrInterrupt) {
			fmt.Fprintln(ioStreams.Stdout, "bye")
			return nil
		}
		if err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		switch line {
		case "":
			continue
		case "/exit", "/quit":
			fmt.Fprintln(ioStreams.Stdout, "bye")
			return nil
		case "/help":
			printChatHelp(ioStreams.Stdout)
			continue
		case "/memory":
			if err := printMemory(ctx, mem, ioStreams.Stdout); err != nil {
				return err
			}
			continue
		}
		if err := appendMessage(ctx, mem, sessionID, "user_message", line); err != nil {
			return err
		}
		taskID, err := newID("task")
		if err != nil {
			return err
		}
		if err := rt.Storage.CreateTask(ctx, storage.Task{
			ID:            taskID,
			Title:         summarizeTitle(line),
			Input:         line,
			Status:        "running",
			LeaderModelID: cfg.Agent.Leader.ModelID,
			PrivacyClass:  "local_private",
		}); err != nil {
			return err
		}
		chatResult, err := rt.Chat(ctx, line)
		if err != nil {
			_ = rt.Storage.FailTask(ctx, taskID, "chat_model_failed", err.Error())
			return err
		}
		if err := rt.Storage.CompleteTask(ctx, taskID, chatResult.Answer, 0.9); err != nil {
			return err
		}
		response := chatResult.Answer
		if err := appendMessage(ctx, mem, sessionID, "assistant_message", response); err != nil {
			return err
		}
		fmt.Fprintln(ioStreams.Stdout, response)
	}
	return nil
}

func taskCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("task requires subcommand list, show, usage, or cancel")
	}
	switch args[0] {
	case "list":
		return taskList(ctx, args[1:], stdout)
	case "show":
		return taskShow(ctx, args[1:], stdout)
	case "usage":
		return taskUsage(ctx, args[1:], stdout)
	case "cancel":
		return taskCancel(ctx, args[1:], stdout)
	default:
		return usageError("unknown task subcommand " + args[0])
	}
}

func taskList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	limit := fs.Int("limit", 20, "task limit")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("task list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	tasks, err := db.ListTasks(ctx, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"tasks": tasks})
	}
	for _, task := range tasks {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", task.ID, task.Status, task.Title)
	}
	return nil
}

func taskShow(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskID := fs.String("id", "", "task id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *taskID == "" {
		return usageError("task show requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	task, err := db.GetTask(ctx, *taskID)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, task)
	}
	fmt.Fprintf(stdout, "id=%s\nstatus=%s\ntitle=%s\ninput=%s\n", task.ID, task.Status, task.Title, task.Input)
	if task.FinalAnswer != nil {
		fmt.Fprintf(stdout, "answer=%s\n", *task.FinalAnswer)
	}
	return nil
}

func taskUsage(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("task usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskID := fs.String("id", "", "task id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *taskID == "" {
		return usageError("task usage requires --config and --id")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	rt := runtime.NewLocal(cfg, db, nil)
	report, err := rt.UsageForTask(ctx, *taskID)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, report)
	}
	fmt.Fprintf(stdout, "task_id=%s\ninput_tokens=%d\noutput_tokens=%d\nbillable_units=%.2f\nestimated_cost_usd=%.6f\nunknown_usage=%t\n",
		report.TaskID, report.Total.InputTokens, report.Total.OutputTokens, report.Total.BillableUnits, report.Total.EstimatedCostUSD, report.UnknownUsage)
	for _, workflow := range report.Workflows {
		fmt.Fprintf(stdout, "workflow_id=%s input_tokens=%d output_tokens=%d estimated_cost_usd=%.6f\n", workflow.WorkflowID, workflow.Total.InputTokens, workflow.Total.OutputTokens, workflow.Total.EstimatedCostUSD)
		for _, node := range workflow.Nodes {
			fmt.Fprintf(stdout, "node_id=%s status=%s input_tokens=%d output_tokens=%d estimated_cost_usd=%.6f unknown=%t\n", node.NodeID, node.Status, node.Usage.InputTokens, node.Usage.OutputTokens, node.Usage.EstimatedCostUSD, node.Unknown)
		}
	}
	return nil
}

func taskCancel(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("task cancel", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskID := fs.String("id", "", "task id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *taskID == "" {
		return usageError("task cancel requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.CancelTask(ctx, *taskID); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "task_id=%s status=cancelled\n", *taskID)
	return err
}

func openConfiguredDB(ctx context.Context, configPath string) (config.Config, *storage.DB, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return config.Config{}, nil, err
	}
	db, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return config.Config{}, nil, err
	}
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		db.Close()
		return config.Config{}, nil, err
	}
	return cfg, db, nil
}

func appendMessage(ctx context.Context, mem memory.Store, sessionID string, eventType string, text string) error {
	id, err := newID("mem")
	if err != nil {
		return err
	}
	return mem.Append(ctx, memory.EpisodicEvent{
		ID:        id,
		TaskID:    sessionID,
		EventType: eventType,
		Summary:   summarizeTitle(text),
		Payload:   map[string]string{"text": text},
	})
}

func printMemory(ctx context.Context, mem memory.Store, stdout io.Writer) error {
	events, err := mem.Recent(ctx, 20)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		fmt.Fprintln(stdout, "No local memory yet.")
		return nil
	}
	for _, event := range events {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", event.CreatedAt.Format("2006-01-02T15:04:05Z"), event.EventType, event.Summary)
	}
	return nil
}

func usageError(message string) error {
	return fmt.Errorf("%s\nusage: pachat run|chat|task --config <path>", message)
}

func ExtractTaskID(output string) string {
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, "task_id=") {
			return strings.TrimPrefix(field, "task_id=")
		}
	}
	return ""
}

func newID(prefix string) (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}

func summarizeTitle(input string) string {
	trimmed := strings.TrimSpace(input)
	if len(trimmed) <= 80 {
		return trimmed
	}
	return trimmed[:80]
}
