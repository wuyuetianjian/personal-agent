package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	agentEvent "agent/internal/event"
	"agent/internal/notification"
	"agent/internal/trigger"
)

func notificationCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || (args[0] != "list" && args[0] != "read") {
		return usageError("notification requires list or read")
	}
	fs := flag.NewFlagSet("notification "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "notification id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("notification requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := notification.Store{DB: db.SQL}
	if args[0] == "read" {
		if *id == "" {
			return usageError("notification read requires --id")
		}
		if err := store.MarkRead(ctx, *id); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "notification_id=%s status=read\n", *id)
		return nil
	}
	items, err := store.List(ctx, 50)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"notifications": items})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.ID, item.Status, item.Title)
	}
	return nil
}

func modelCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "discover" {
		return usageError("model requires discover")
	}
	fs := flag.NewFlagSet("model discover", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("model discover requires --config")
	}
	cfg, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	ids := make([]string, 0, len(cfg.Models.Registry))
	for _, model := range cfg.Models.Registry {
		ids = append(ids, model.ID)
	}
	if len(ids) == 0 && cfg.Agent.Leader.ModelID != "" {
		ids = append(ids, cfg.Agent.Leader.ModelID)
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"models": ids})
	}
	for _, id := range ids {
		fmt.Fprintln(stdout, id)
	}
	return nil
}

func watcherCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "scan" {
		return usageError("watcher requires scan")
	}
	fs := flag.NewFlagSet("watcher scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	projectID := fs.String("project", "default", "project id")
	path := fs.String("path", "", "path to scan")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *configPath == "" || *path == "" {
		return usageError("watcher scan requires --config and --path")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	count, err := (trigger.FileWatcher{Events: agentEvent.Store{DB: db.SQL}}).Scan(ctx, *projectID, *path)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "events=%d\n", count)
	return nil
}
