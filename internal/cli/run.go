package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"strings"

	"agent/internal/config"
	"agent/internal/memory"
	"agent/internal/storage"
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
	case "run":
		return runTask(ctx, args[1:], ioStreams.Stdout)
	case "chat":
		return chat(ctx, args[1:], ioStreams)
	case "task":
		return taskCommand(ctx, args[1:], ioStreams.Stdout)
	default:
		return usageError("unknown command " + args[0])
	}
}

func runTask(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskInput := fs.String("task", "", "task input")
	longTask := fs.Bool("long", false, "persist task as running for long-task tracking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("run requires --config")
	}
	if strings.TrimSpace(*taskInput) == "" {
		return usageError("run requires --task")
	}

	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()

	taskID, err := newID("task")
	if err != nil {
		return err
	}
	status := "completed"
	answer := "No-op task completed."
	var finalAnswer *string = &answer
	confidence := 1.0
	var finalConfidence *float64 = &confidence
	if *longTask {
		status = "running"
		finalAnswer = nil
		finalConfidence = nil
	}
	if err := db.CreateTask(ctx, storage.Task{
		ID:              taskID,
		Title:           summarizeTitle(*taskInput),
		Input:           *taskInput,
		Status:          status,
		LeaderModelID:   cfg.Agent.Leader.ModelID,
		PrivacyClass:    "local_private",
		FinalAnswer:     finalAnswer,
		FinalConfidence: finalConfidence,
	}); err != nil {
		return err
	}

	if *longTask {
		_, err = fmt.Fprintf(stdout, "task_id=%s status=running\n", taskID)
		return err
	}
	_, err = fmt.Fprintf(stdout, "task_id=%s status=completed answer=%q\n", taskID, answer)
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
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()

	mem := memory.NewStore(db.SQL)
	sessionID, err := newID("chat")
	if err != nil {
		return err
	}

	fmt.Fprintln(ioStreams.Stdout, "pachat interactive mode. Type /memory, /exit, or /quit.")
	scanner := bufio.NewScanner(ioStreams.Stdin)
	for {
		fmt.Fprint(ioStreams.Stdout, "> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "":
			continue
		case "/exit", "/quit":
			fmt.Fprintln(ioStreams.Stdout, "bye")
			return nil
		case "/memory":
			if err := printMemory(ctx, mem, ioStreams.Stdout); err != nil {
				return err
			}
			continue
		}
		if err := appendMessage(ctx, mem, sessionID, "user_message", line); err != nil {
			return err
		}
		response := "Recorded message in local memory."
		if err := appendMessage(ctx, mem, sessionID, "assistant_message", response); err != nil {
			return err
		}
		fmt.Fprintln(ioStreams.Stdout, response)
	}
	return scanner.Err()
}

func taskCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("task requires subcommand list, show, or cancel")
	}
	switch args[0] {
	case "list":
		return taskList(ctx, args[1:], stdout)
	case "show":
		return taskShow(ctx, args[1:], stdout)
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
	fmt.Fprintf(stdout, "id=%s\nstatus=%s\ntitle=%s\ninput=%s\n", task.ID, task.Status, task.Title, task.Input)
	if task.FinalAnswer != nil {
		fmt.Fprintf(stdout, "answer=%s\n", *task.FinalAnswer)
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
