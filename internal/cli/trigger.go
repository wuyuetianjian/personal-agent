package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"agent/internal/trigger"
)

func triggerCommand(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError("trigger requires create, list, show, enable, disable, run-now, or history")
	}
	switch args[0] {
	case "create":
		return triggerCreate(ctx, args[1:], stdout)
	case "list":
		return triggerList(ctx, args[1:], stdout)
	case "show":
		return triggerShow(ctx, args[1:], stdout)
	case "enable":
		return triggerSetEnabled(ctx, args[1:], stdout, true)
	case "disable":
		return triggerSetEnabled(ctx, args[1:], stdout, false)
	case "run-now":
		return triggerRunNow(ctx, args[1:], stdout)
	case "history":
		return triggerHistory(ctx, args[1:], stdout)
	default:
		return usageError("unknown trigger subcommand " + args[0])
	}
}

func triggerCreate(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("trigger create", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "trigger id")
	projectID := fs.String("project", "default", "project id")
	triggerType := fs.String("type", string(trigger.TypeManual), "trigger type")
	enabled := fs.Bool("enabled", true, "enable trigger")
	skillID := fs.String("skill", "", "skill id")
	interval := fs.Duration("interval", 0, "interval duration")
	eventSource := fs.String("event-source", "", "event source")
	eventType := fs.String("event-type", "", "event type")
	condition := fs.String("condition", "", "condition query")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("trigger create requires --config")
	}
	if *id == "" {
		generated, err := newID("trigger")
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
	tr := trigger.Trigger{
		ID:        *id,
		ProjectID: *projectID,
		Type:      trigger.Type(*triggerType),
		Enabled:   *enabled,
		SkillID:   *skillID,
		PolicyRef: "default",
		BudgetRef: "default",
	}
	switch tr.Type {
	case trigger.TypeInterval:
		tr.Schedule.Interval = *interval
	case trigger.TypeEvent:
		tr.Event.Source = *eventSource
		tr.Event.Type = *eventType
	case trigger.TypeConditionWatch:
		tr.Condition.Evaluator = "false_to_true"
		tr.Condition.Query = *condition
	case trigger.TypeManual, trigger.TypeGoal:
	default:
		return trigger.ErrInvalidTrigger
	}
	if err := (trigger.Store{DB: db.SQL}).Put(ctx, tr); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trigger_id=%s type=%s enabled=%t\n", tr.ID, tr.Type, tr.Enabled)
	return nil
}

func triggerList(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("trigger list", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" {
		return usageError("trigger list requires --config")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	items, err := (trigger.Store{DB: db.SQL}).List(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"triggers": items})
	}
	for _, item := range items {
		fmt.Fprintf(stdout, "%s\t%s\t%t\t%s\n", item.ID, item.Type, item.Enabled, item.ProjectID)
	}
	return nil
}

func triggerShow(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("trigger show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "trigger id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("trigger show requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	tr, err := (trigger.Store{DB: db.SQL}).Get(ctx, *id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, tr)
	}
	fmt.Fprintf(stdout, "id=%s\ntype=%s\nenabled=%t\nproject_id=%s\n", tr.ID, tr.Type, tr.Enabled, tr.ProjectID)
	return nil
}

func triggerSetEnabled(ctx context.Context, args []string, stdout io.Writer, enabled bool) error {
	fs := flag.NewFlagSet("trigger enabled", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "trigger id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("trigger enable/disable requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := (trigger.Store{DB: db.SQL}).SetEnabled(ctx, *id, enabled); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trigger_id=%s enabled=%t\n", *id, enabled)
	return nil
}

func triggerRunNow(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("trigger run-now", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "trigger id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("trigger run-now requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := (trigger.Daemon{Store: trigger.Store{DB: db.SQL}, Now: time.Now}).RunNow(ctx, *id); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "trigger_id=%s status=fired\n", *id)
	return nil
}

func triggerHistory(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("trigger history", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", "", "config file path")
	id := fs.String("id", "", "trigger id")
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *configPath == "" || *id == "" {
		return usageError("trigger history requires --config and --id")
	}
	_, db, err := openConfiguredDB(ctx, *configPath)
	if err != nil {
		return err
	}
	defer db.Close()
	history, err := (trigger.Store{DB: db.SQL}).History(ctx, *id, 50)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writePrettyJSON(stdout, map[string]any{"history": history})
	}
	for _, item := range history {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", item.CreatedAt.Format(time.RFC3339), item.Status, item.Message)
	}
	return nil
}
