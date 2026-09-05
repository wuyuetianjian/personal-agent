package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"strings"

	"agent/internal/config"
	"agent/internal/storage"
)

func Run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("missing command")
	}
	switch args[0] {
	case "run":
		return runTask(ctx, args[1:], stdout)
	default:
		return usageError("unknown command " + args[0])
	}
}

func runTask(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	taskInput := fs.String("task", "", "task input")
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
	db, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := storage.Migrate(ctx, db.SQL); err != nil {
		return err
	}

	taskID, err := newID("task")
	if err != nil {
		return err
	}
	answer := "No-op task completed."
	confidence := 1.0
	if err := db.CreateCompletedTask(ctx, storage.Task{
		ID:              taskID,
		Title:           summarizeTitle(*taskInput),
		Input:           *taskInput,
		LeaderModelID:   cfg.Agent.Leader.ModelID,
		PrivacyClass:    "local_private",
		FinalAnswer:     &answer,
		FinalConfidence: &confidence,
	}); err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "task_id=%s status=completed answer=%q\n", taskID, answer)
	return err
}

func usageError(message string) error {
	return fmt.Errorf("%s\nusage: personal-agent run --config <path> --task <task>", message)
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
