package cli

import (
	"agent/internal/api"
	"agent/internal/model"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"
)

func probeAgentProvider(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("probe-agent-provider", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	cfg := f.String("config", "configs/config.example.yaml", "configured provider/model")
	id := f.String("model", "", "registered model ID")
	parent := f.String("runtime-parent", "", "same explicit rebuilt runtime parent as serve-agent; stop service first")
	consent := f.Bool("send-synthetic", false, "send only synthetic content to configured endpoint; ordinary API usage may be charged; never executes a tool")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 || *id == "" || *parent == "" {
		return fmt.Errorf("probe-agent-provider requires --model and --runtime-parent")
	}
	if !*consent {
		return fmt.Errorf("requires --send-synthetic: sends synthetic content to your configured endpoint and may incur ordinary API usage; no files/conversation sent and no tool executed")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	rt, err := api.OpenInteractionRuntime(ctx, *parent)
	if err != nil {
		return err
	}
	defer rt.Close()
	p, err := agentResolver(*cfg)(ctx, *id)
	if err != nil {
		return fmt.Errorf("provider configuration unavailable; no probe sent")
	}
	attempt, err := rt.BeginToolProbe(ctx, p)
	if err != nil {
		return fmt.Errorf("cannot invalidate old evidence; no probe sent")
	}
	fmt.Fprintln(out, "Sending synthetic content only to the configured provider endpoint; ordinary API usage may be charged. No tool is executed.")
	result := model.ProbeToolCapability(ctx, p.Provider, p.Model)
	// Even a cancelled probe may record its failure, bounded independently. The
	// durable in-progress invalidation already fails closed if this write fails.
	save, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err = rt.FinishToolProbe(save, p, attempt, result); err != nil {
		return fmt.Errorf("probe evidence save could not be confirmed; inspect capability status before using tools")
	}
	state := rt.WithToolEvidence(save, p)
	fmt.Fprintln(out, state.ToolCapabilityReason)
	if !state.ToolsSupported {
		return fmt.Errorf("current tool verification could not be confirmed; probe reason: %s", result.Reason)
	}
	fmt.Fprintln(out, "Synthetic tool-call evidence only, not broad compatibility. Create a new workspace session; exec still requires separate opt-in and exact approval.")
	return nil
}
