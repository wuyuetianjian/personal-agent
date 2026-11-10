package cli

import (
	"agent/internal/api"
	"agent/internal/config"
	"agent/internal/interaction"
	"agent/internal/model"
	"agent/internal/privacy"
	"agent/internal/webui"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"
)

func readAgentToken(path string) (string, error) {
	token := os.Getenv("PA_AGENT_TOKEN")
	if path != "" {
		st, e := os.Lstat(path)
		if e != nil || !st.Mode().IsRegular() || runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
			return "", fmt.Errorf("auth file must be a private regular file (0600)")
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return "", fmt.Errorf("cannot read auth file")
		}
		token = string(raw)
	}
	token = strings.TrimSpace(token)
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("auth required: PA_AGENT_TOKEN or --auth-file")
	}
	return token, nil
}
func serveAgent(ctx context.Context, args []string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	f := flag.NewFlagSet("serve-agent", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	addr := f.String("addr", "127.0.0.1:7319", "loopback bind")
	parent := f.String("runtime-parent", "", "explicit separate runtime parent")
	workspace := f.String("workspace", "", "explicit workspace; omit for chat-only sessions")
	mcpPath := f.String("mcp-config", "", "explicit private trusted stdio MCP JSON; NON-SANDBOXED current-user code, disabled unless configured and session opts in")
	nonSandboxExec := f.Bool("non-sandbox-exec", false, "NON-SANDBOXED exec with current OS-user access; cwd is not confinement; fresh exact approval per call")
	auth := f.String("auth-file", "", "private bearer token file")
	cfgPath := f.String("config", "configs/config.example.yaml", "model configuration")
	modelID := f.String("model", "", "registered model ID")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *nonSandboxExec && *workspace == "" {
		return fmt.Errorf("NON-SANDBOXED exec requires explicit --workspace; current-user access is not confined by cwd")
	}
	mcpConfig, e := readTrustedMCPConfig(*mcpPath)
	if e != nil {
		return e
	}
	if mcpConfig.Enabled && *workspace == "" {
		return fmt.Errorf("MCP requires explicit --workspace")
	}
	token, e := readAgentToken(*auth)
	if e != nil {
		return e
	}
	host, _, e := net.SplitHostPort(*addr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("explicit loopback IP address required")
	}
	if *modelID == "" {
		return fmt.Errorf("explicit --model required")
	}
	if *parent == "" {
		return fmt.Errorf("explicit --runtime-parent required")
	}
	rt, e := api.OpenInteractionRuntime(ctx, *parent)
	if e != nil {
		return e
	}
	defer rt.Close()
	resolver := agentResolver(*cfgPath, rt)
	if _, e = resolver(ctx, *modelID); e != nil {
		return fmt.Errorf("provider configuration unavailable; check model/config/environment")
	}
	listener, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	defer listener.Close()
	// Startup-only sanitized inventory. GET never reloads config or resolves providers.
	cfg, e := config.Load(*cfgPath)
	if e != nil {
		return fmt.Errorf("model inventory configuration unavailable")
	}
	inventory := []api.OperatorRecord{}
	for _, m := range cfg.Models.Registry {
		selected, resolveErr := resolver(ctx, m.ID)
		inventory = append(inventory, api.OperatorModelSnapshot(m.ID, selected, resolveErr))
	}
	service, e := api.NewInteractionService(rt, api.InteractionOptions{OperatorModels: inventory, Token: token, Host: listener.Addr().String(), Workspace: *workspace, DefaultModel: *modelID, Resolve: resolver, NonSandboxExec: *nonSandboxExec, MCP: mcpConfig, WebAssets: webui.Assets()})
	if e != nil {
		return e
	}
	defer service.Close()
	server := &http.Server{Handler: service, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	if mcpConfig.Enabled {
		fmt.Fprintln(out, interaction.MCPWarning)
	}
	if *nonSandboxExec {
		fmt.Fprintln(out, interaction.NonSandboxExecWarning)
	}
	fmt.Fprintf(out, "rebuilt preview API: http://%s (authentication required)\n", listener.Addr())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case e = <-done:
		if e == http.ErrServerClosed {
			return nil
		}
		return e
	case <-ctx.Done():
		service.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
		_ = server.Close()
		<-done
		return nil
	}
}
func agentResolver(path string, evidence ...*api.InteractionRuntime) api.ProviderResolver {
	return func(ctx context.Context, id string) (api.ProviderSelection, error) {
		cfg, e := config.Load(path)
		if e != nil {
			return api.ProviderSelection{}, e
		}
		registry, e := model.NewRegistry(cfg.Models)
		if e != nil {
			return api.ProviderSelection{}, e
		}
		m, e := registry.Model(id)
		if e != nil {
			return api.ProviderSelection{}, e
		}
		if !m.Provider.Enabled || m.Provider.Type != "openai_compatible" || !m.Capabilities[model.CapabilityChat] {
			return api.ProviderSelection{}, fmt.Errorf("provider chat unavailable")
		}
		endpoint := m.Provider.BaseURL
		if endpoint == "" {
			endpoint, _ = config.EnvValue(m.Provider.BaseURLEnv)
		}
		key, _ := config.EnvValue(m.Provider.APIKeyEnv)
		// Bind the effective privacy policy, not only the registry's original flag.
		if m.Provider.TrustLevel == model.TrustPublicRemote && cfg.Privacy.FailClosedForPublicModels {
			m.Provider.RequirePrivacyGateway = true
		}
		client := &model.OpenAICompatibleClient{Provider: m.Provider, BaseURL: endpoint, APIKey: key, Client: &http.Client{Timeout: 5 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		if m.Provider.RequirePrivacyGateway || m.Provider.TrustLevel == model.TrustPublicRemote {
			secret, _ := config.EnvValue(cfg.Privacy.HMACSecretEnv)
			gateway, e := privacy.NewGateway(secret)
			if e != nil {
				return api.ProviderSelection{}, e
			}
			client.Privacy = gateway
		}
		// Resolving never calls the provider. Only explicit, matching persisted
		// synthetic evidence may enable tool advertisement in new sessions.
		p := api.ProviderSelection{Model: m, Provider: &model.OpenAIStreamProvider{Client: client}, Endpoint: endpoint, ToolCapabilityReason: "tool_capability_unverified"}
		if len(evidence) > 0 && evidence[0] != nil {
			p = evidence[0].WithToolEvidence(ctx, p)
		}
		return p, nil
	}
}

// Only this explicit new flag supplies rebuilt MCP authority; legacy enabled
// flags do not flow into it. Viewing the file never executes its commands.
func readTrustedMCPConfig(path string) (interaction.MCPHostConfig, error) {
	var c interaction.MCPHostConfig
	if path == "" {
		return c, nil
	}
	st, e := os.Lstat(path)
	if e != nil || !st.Mode().IsRegular() || st.Size() > 64<<10 || runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		return c, fmt.Errorf("MCP config must be a private regular file (0600), at most 64 KiB")
	}
	f, e := os.Open(path)
	if e != nil {
		return c, e
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if e != nil || len(raw) > 64<<10 {
		return c, fmt.Errorf("MCP config size limit")
	}
	if _, e = interaction.ProposalDigest("validate-config", interaction.ToolCall{ID: "config", Name: "config", Arguments: string(raw)}); e != nil {
		return c, fmt.Errorf("invalid MCP config JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return c, fmt.Errorf("invalid MCP config")
	}
	return c, interaction.ValidateMCPConfig(c)
}
