// agent-fixture is a deterministic local-only real-Go server for SDK/UI tests.
package main

import (
	"agent/internal/api"
	"agent/internal/apifixture"
	"agent/internal/interaction"
	"agent/internal/mcpfixture"
	"agent/internal/webui"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--mcp-peer" {
		mcpfixture.Serve()
		return
	}
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	addr := flag.String("addr", "127.0.0.1:0", "loopback IP")
	parent := flag.String("runtime-parent", "", "empty creates a temporary runtime")
	workspace := flag.String("workspace", "", "empty creates a temporary workspace")
	operatorFixture := flag.Bool("operator-fixture", false, "seed owned read-only metadata inventory fixtures")
	queuedHookRun := flag.Bool("queued-hook-run", false, "seed one owned queued Hook-pinned Skill run without starting an executor")
	safeMCP := flag.Bool("safe-mcp", false, "explicit owned stdio MCP helper only")
	mcpResult := flag.String("mcp-result", "", "owned helper result mode")
	safeExec := flag.Bool("safe-exec", false, "opt in to fixed safe exec in an owned temporary workspace only")
	chatOnly := flag.Bool("chat-only-fixture", false, "simulate unavailable native adapters; not native-platform evidence")
	flag.Parse()
	if *queuedHookRun && (*workspace != "" || *parent != "" || *operatorFixture || *safeExec || *safeMCP || *chatOnly) {
		return fmt.Errorf("queued-hook-run requires an exclusive owned temporary workspace/runtime")
	}
	if *operatorFixture && (*workspace != "" || *parent != "") {
		return fmt.Errorf("operator-fixture requires owned temporary workspace/runtime")
	}
	if (*safeExec || *safeMCP) && (*workspace != "" || *parent != "" || *chatOnly) {
		return fmt.Errorf("safe-exec/safe-mcp requires owned temporary workspace/runtime, without chat-only-fixture")
	}

	token := os.Getenv("PA_AGENT_TOKEN")
	if token == "" {
		return fmt.Errorf("PA_AGENT_TOKEN is required")
	}
	host, _, e := net.SplitHostPort(*addr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("loopback required")
	}
	if *parent == "" {
		*parent, e = os.MkdirTemp("", "agent-fixture-runtime-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(*parent)
	}
	if *workspace == "" {
		*workspace, e = os.MkdirTemp("", "agent-fixture-workspace-")
		if e != nil {
			return e
		}
		defer os.RemoveAll(*workspace)
	}
	f, e := os.OpenFile(filepath.Join(*workspace, "note.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e == nil {
		_, e = f.WriteString("old\n")
		f.Close()
		if e != nil {
			return e
		}
	} else if !os.IsExist(e) {
		return e
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 5*time.Minute)
	defer cancel()
	rt, e := api.OpenInteractionRuntime(ctx, *parent)
	if e != nil {
		return e
	}
	defer rt.Close()
	if *operatorFixture {
		if e = seedOperatorFixture(ctx, rt.DB.SQL); e != nil {
			return e
		}
	}
	l, e := net.Listen("tcp", *addr)
	if e != nil {
		return e
	}
	defer l.Close()
	serviceWorkspace := *workspace
	if *chatOnly {
		serviceWorkspace = ""
	}
	mcpConfig := interaction.MCPHostConfig{}
	if *safeMCP {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		mcpConfig = interaction.MCPHostConfig{Enabled: true, Servers: []interaction.MCPServerConfig{{ID: "owned-fixture", Command: exe, Args: []string{"--mcp-peer"}, Environment: []string{"PA_MCP_MARKER=" + filepath.Join(*workspace, "mcp-marker.txt"), "PA_MCP_RESULT=" + *mcpResult}, TimeoutMillis: 3000}}}
	}
	service, e := api.NewInteractionService(rt, api.InteractionOptions{Token: token, Host: l.Addr().String(), Workspace: serviceWorkspace, NonSandboxExec: *safeExec, MCP: mcpConfig, DefaultModel: "fixture", WebAssets: webui.Assets(), Resolve: func(context.Context, string) (api.ProviderSelection, error) {
		selection := apifixture.Selection()
		if *safeExec {
			selection.Provider = apifixture.Provider{ExecWorkspace: *workspace}
		}
		return selection, nil
	}})
	if e != nil {
		return e
	}
	defer service.Close()
	info := map[string]string{"url": "http://" + l.Addr().String(), "workspace": *workspace, "runtime_parent": *parent, "provider": "offline fixture; not real model evidence"}
	if *queuedHookRun {
		seed, err := seedQueuedHookRun(ctx, service, interaction.NewStore(rt.DB.SQL), l.Addr().String(), token)
		if err != nil {
			return err
		}
		for key, value := range seed {
			info[key] = value
		}
	}
	var handler http.Handler = service
	if *chatOnly {
		handler = chatOnlyDisplayFixture(service)
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}
	done := make(chan error, 1)
	go func() { done <- server.Serve(l) }()
	_ = json.NewEncoder(os.Stdout).Encode(info)
	select {
	case <-ctx.Done():
		service.Close()
		_ = server.Close()
		<-done
		return nil
	case e = <-done:
		return e
	}
}

// This test-only wire adapter labels the simulated platform limitation explicitly.
// Real authority remains more restrictive: the underlying service has no workspace.
func chatOnlyDisplayFixture(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/v1/status" {
			next.ServeHTTP(w, r)
			return
		}
		rec := httptest.NewRecorder()
		next.ServeHTTP(rec, r)
		for k, values := range rec.Header() {
			w.Header()[k] = values
		}
		if rec.Code != http.StatusOK {
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return
		}
		var value map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
			http.Error(w, "fixture status error", 500)
			return
		}
		reason := "Fixture simulation: native adapters unavailable; chat-only supported. Not native macOS/Windows evidence."
		value["workspace"] = interaction.WorkspaceCapabilities{Files: interaction.Capability{Reason: reason}, Exec: interaction.Capability{Reason: reason}}
		_ = json.NewEncoder(w).Encode(value)
	})
}
