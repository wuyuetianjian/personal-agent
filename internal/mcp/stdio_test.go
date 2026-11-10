package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Owned executable fixture: no shell, configured server, network, or user data.
func TestStdioFixture(t *testing.T) {
	mode := os.Getenv("MCP_FIXTURE")
	if mode == "" {
		return
	}
	log, _ := os.OpenFile(os.Getenv("MCP_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	defer log.Close()
	enc := json.NewEncoder(os.Stdout)
	var mu sync.Mutex
	send := func(v any) { mu.Lock(); defer mu.Unlock(); _ = enc.Encode(v) }
	initialized := false
	discoverCount := 0
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		_, _ = log.Write(append(line, '\n'))
		var r struct {
			ID     json.RawMessage            `json:"id"`
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if json.Unmarshal(line, &r) != nil {
			os.Exit(40)
		}
		if r.Method == "notifications/initialized" {
			initialized = true
			continue
		}
		if r.Method == "notifications/cancelled" {
			continue
		}
		reply := func(v any) { send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": v}) }
		rpcerr := func(code int, data any) {
			send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"code": code, "message": "SECRET must not leak", "data": data}})
		}
		legacy := strings.HasPrefix(mode, "legacy")
		if legacy && r.Method != "server/discover" {
			if _, ok := r.Params["_meta"]; ok {
				os.Exit(46)
			}
			if r.Method == "initialize" {
				var version string
				_ = json.Unmarshal(r.Params["protocolVersion"], &version)
				if version != "2024-11-05" || r.Params["capabilities"] == nil || r.Params["clientInfo"] == nil {
					os.Exit(47)
				}
			}
		}
		if !legacy {
			var meta map[string]any
			_ = json.Unmarshal(r.Params["_meta"], &meta)
			if meta["io.modelcontextprotocol/protocolVersion"] != "2026-07-28" || meta["io.modelcontextprotocol/clientCapabilities"] == nil {
				os.Exit(41)
			}
			caps := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
			if len(caps) != 0 {
				os.Exit(42)
			}
		}
		switch r.Method {
		case "server/discover":
			discoverCount++
			switch mode {
			case "legacy-timeout":
				continue
			case "legacy-invalid":
				rpcerr(-32602, nil)
				continue
			case "legacy-zero":
				rpcerr(0, nil)
				continue
			case "legacy-custom":
				rpcerr(-32000, nil)
				continue
			case "legacy", "legacy-bad-version":
				rpcerr(-32601, nil)
				continue
			case "version", "version-intersection":
				if mode == "version" || discoverCount == 1 {
					supported := []string{"2024-11-05"}
					if mode == "version-intersection" {
						supported = []string{"2026-07-28"}
					}
					rpcerr(-32022, map[string]any{"supported": supported, "requested": "2026-07-28"})
					continue
				}
			case "capability-error":
				rpcerr(-32021, nil)
				continue
			case "bad-discover":
				reply(map[string]any{"resultType": "complete", "supportedVersions": []string{"2099-01-01"}})
				continue
			}
			reply(map[string]any{"resultType": "complete", "supportedVersions": []string{"2026-07-28"}, "capabilities": map[string]any{"tools": map[string]any{}}, "ttlMs": 0, "cacheScope": "private"})
		case "initialize":
			if !legacy {
				os.Exit(43)
			}
			version := "2024-11-05"
			if mode == "legacy-bad-version" {
				version = "2099-01-01"
			}
			reply(map[string]any{"protocolVersion": version, "capabilities": map[string]any{"tools": map[string]any{}}})
		case "tools/list":
			if legacy && !initialized {
				os.Exit(44)
			}
			name := "echo"
			cursor := ""
			if mode == "pages" || mode == "loop" {
				if len(r.Params["cursor"]) == 0 {
					cursor = "next"
				} else {
					name = "other"
				}
				if mode == "loop" {
					cursor = "next"
				}
			}
			schema := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"],"additionalProperties":false}`)
			if mode == "unsupported-schema" {
				schema = json.RawMessage(`{"type":"object","$ref":"https://must-not-fetch.invalid/schema"}`)
			}
			if strings.HasPrefix(mode, "numeric-") {
				schema = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":0}}}`)
			}
			if mode == "numeric-input-schema" {
				schema = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":1e1000001}}}`)
			}
			if mode == "numeric-input-schema-negative" {
				schema = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":1e-1000001}}}`)
			}
			if mode == "hidden-input-schema" {
				schema = hiddenReferenceDAG(24)
			}
			out := map[string]any{"tools": []any{map[string]any{"name": name, "inputSchema": schema}}, "ttlMs": 0, "cacheScope": "private"}
			if mode == "invalid-output-schema" {
				out["tools"].([]any)[0].(map[string]any)["outputSchema"] = json.RawMessage(`{"type":"object","required":true}`)
			}
			if strings.HasPrefix(mode, "numeric-output") {
				out["tools"].([]any)[0].(map[string]any)["outputSchema"] = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":0}}}`)
			}
			if mode == "numeric-output-schema" {
				out["tools"].([]any)[0].(map[string]any)["outputSchema"] = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":1e1000001}}}`)
			}
			if mode == "numeric-output-schema-negative" {
				out["tools"].([]any)[0].(map[string]any)["outputSchema"] = json.RawMessage(`{"type":"object","properties":{"x":{"type":"number","minimum":1e-1000001}}}`)
			}
			if mode == "hidden-output-schema" {
				out["tools"].([]any)[0].(map[string]any)["outputSchema"] = hiddenReferenceDAG(24)
			}
			if !legacy {
				out["resultType"] = "complete"
			}
			if cursor != "" {
				out["nextCursor"] = cursor
			}
			reply(out)
			if mode == "block-write" || mode == "spoof-queued" {
				if mode == "spoof-queued" {
					var last string
					_ = json.Unmarshal(r.ID, &last)
					n, _ := strconv.Atoi(last)
					go func() {
						time.Sleep(200 * time.Millisecond)
						send(map[string]any{"jsonrpc": "2.0", "id": strconv.Itoa(n + 2), "result": map[string]any{"resultType": "complete", "content": []any{}}})
					}()
				}
				time.Sleep(time.Hour)
			}
		case "tools/call":
			if mode == "numeric-output" || mode == "numeric-output-negative" {
				number := "1e1000001"
				if mode == "numeric-output-negative" {
					number = "1e-1000001"
				}
				reply(map[string]any{"resultType": "complete", "content": []any{}, "structuredContent": json.RawMessage(`{"x":` + number + `}`)})
				continue
			}

			var args map[string]string
			_ = json.Unmarshal(r.Params["arguments"], &args)
			switch args["x"] {
			case "exit":
				os.Exit(0)
			case "hang":
				continue
			case "malformed":
				fmt.Println(`not JSON SECRET`)
				continue
			case "oversize":
				fmt.Println(strings.Repeat("x", 2<<20))
				continue
			case "bad-id":
				send(map[string]any{"jsonrpc": "2.0", "id": nil, "result": map[string]any{}})
				continue
			case "bad-error":
				send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "error": map[string]any{"message": "SECRET"}})
				continue
			case "both-result-error":
				send(map[string]any{"jsonrpc": "2.0", "id": r.ID, "result": map[string]any{}, "error": map[string]any{"code": 1, "message": "SECRET"}})
				continue
			case "null-resultType":
				reply(map[string]any{"resultType": nil, "content": []any{}})
				continue
			case "duplicate-key":
				fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"isError\":true,\"isError\":false}}\n", r.ID)
				continue
			case "medium-frame":
				reply(map[string]any{"content": []any{}, "padding": strings.Repeat("x", 2000)})
				continue
			case "flood":
				for i := 0; i < 100; i++ {
					send(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progress": i}})
				}
			case "rpc-error":
				rpcerr(-32602, nil)
				continue
			case "tool-error":
				reply(map[string]any{"resultType": "complete", "isError": true, "content": []any{}})
				continue
			case "input_required", "task", "alien":
				reply(map[string]any{"resultType": args["x"], "requestState": "SECRET"})
				continue
			case "slow", "late":
				id := append(json.RawMessage(nil), r.ID...)
				x := args["x"]
				go func() {
					delay := 100 * time.Millisecond
					if x == "late" {
						delay = 500 * time.Millisecond
					}
					time.Sleep(delay)
					send(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"resultType": "complete", "content": []any{}, "echo": x}})
				}()
				continue
			}
			send(map[string]any{"jsonrpc": "2.0", "method": "notifications/progress", "params": map[string]any{"progressToken": r.ID, "progress": 1}})
			send(map[string]any{"jsonrpc": "2.0", "id": 999, "result": map[string]any{}}) // Unknown numeric ID must not match string IDs.
			out := map[string]any{"content": []any{}, "echo": args["x"], "secretInherited": os.Getenv("MCP_PARENT_SECRET") != ""}
			if !legacy {
				out["resultType"] = "complete"
			}
			reply(out)
		default:
			os.Exit(45)
		}
	}
	os.Exit(0)
}

func fixtureConfig(t *testing.T, mode string) (StdioConfig, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "wire.jsonl")
	probe := 2 * time.Second
	if mode == "legacy-timeout" {
		probe = 200 * time.Millisecond
	}
	return StdioConfig{Command: exe, Args: []string{"-test.run=^TestStdioFixture$"}, Env: []string{"MCP_FIXTURE=" + mode, "MCP_LOG=" + log}, ProbeTimeout: probe, RequestTimeout: 5 * time.Second}, log
}
func startFixture(t *testing.T, mode string) (*StdioClient, string) {
	t.Helper()
	cfg, log := fixtureConfig(t, mode)
	c, e := OpenStdio(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { c.Close() })
	return c, log
}
func TestDualEraNegotiation(t *testing.T) {
	for _, mode := range []string{"modern", "legacy", "legacy-invalid", "legacy-custom", "legacy-zero", "legacy-timeout", "version-intersection"} {
		t.Run(mode, func(t *testing.T) {
			c, log := startFixture(t, mode)
			want := ModernVersion
			if strings.HasPrefix(mode, "legacy") {
				want = LegacyVersion
			}
			if c.Version() != want {
				t.Fatal(c.Version())
			}
			list, e := c.ListTools(context.Background())
			if e != nil || len(list.Tools) != 1 {
				t.Fatalf("%+v %v", list, e)
			}
			if len(list.Tools[0].SchemaDigest) != 64 {
				t.Fatal("schema digest missing")
			}
			r, e := c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"ok"}`))
			if e != nil || r.Certainty != KnownResponse || !strings.Contains(string(r.Raw), `"echo":"ok"`) {
				t.Fatalf("%+v %v", r, e)
			}
			c.Close()
			b, _ := os.ReadFile(log)
			inits := strings.Count(string(b), `"method":"initialize"`)
			if (want == ModernVersion && inits != 0) || (want == LegacyVersion && inits != 1) {
				t.Fatal(string(b))
			}
		})
	}
}
func TestNoLegacyDowngrade(t *testing.T) {
	for _, mode := range []string{"version", "bad-discover", "legacy-bad-version", "capability-error"} {
		t.Run(mode, func(t *testing.T) {
			cfg, log := fixtureConfig(t, mode)
			c, e := OpenStdio(context.Background(), cfg)
			if c != nil {
				c.Close()
			}
			if e == nil {
				t.Fatal("accepted incompatible peer")
			}
			b, _ := os.ReadFile(log)
			if mode != "legacy-bad-version" && strings.Contains(string(b), `"method":"initialize"`) {
				t.Fatal(string(b))
			}
			if strings.Contains(e.Error(), "SECRET") {
				t.Fatal(e)
			}
		})
	}
}
func TestToolCallCertainty(t *testing.T) {
	for _, x := range []string{"tool-error", "rpc-error", "input_required", "task", "alien", "exit", "hang", "malformed", "oversize", "bad-id", "bad-error", "both-result-error", "null-resultType", "duplicate-key"} {
		t.Run(x, func(t *testing.T) {
			c, log := startFixture(t, "modern")
			if _, e := c.ListTools(context.Background()); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r, e := c.CallTool(ctx, "echo", json.RawMessage(fmt.Sprintf(`{"x":%q}`, x)))
			if e == nil {
				t.Fatal("failure reported success")
			}
			want := KnownResponse
			if x == "exit" || x == "hang" || x == "malformed" || x == "oversize" || x == "bad-id" || x == "bad-error" || x == "both-result-error" || x == "duplicate-key" {
				want = PossiblySent
			}
			if r.Certainty != want {
				t.Fatalf("%+v %v", r, e)
			}
			var ce *CallError
			if !errors.As(e, &ce) || ce.Certainty != want {
				t.Fatalf("missing facts: %v", e)
			}
			if strings.Contains(e.Error(), "SECRET") {
				t.Fatal("error leaked server data")
			}
			c.Close()
			b, _ := os.ReadFile(log)
			if strings.Count(string(b), `"method":"tools/call"`) != 1 {
				t.Fatal(string(b))
			}
		})
	}
}
func TestPreflightNeverSent(t *testing.T) {
	c, log := startFixture(t, "modern")
	_, _ = c.ListTools(context.Background())
	for _, raw := range []string{`no`, `[]`, `null`, `{}`, `{"x":1}`, `{"x":"ok","extra":true}`} {
		r, e := c.CallTool(context.Background(), "echo", json.RawMessage(raw))
		if e == nil || r.Certainty != NotSent {
			t.Fatalf("%s %+v %v", raw, r, e)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, e := c.CallTool(ctx, "echo", json.RawMessage(`{"x":"ok"}`))
	if e == nil || r.Certainty != NotSent {
		t.Fatalf("%+v %v", r, e)
	}
	c.Close()
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), `"method":"tools/call"`) {
		t.Fatal(string(b))
	}
}
func TestPaginationLimits(t *testing.T) {
	c, _ := startFixture(t, "pages")
	l, e := c.ListTools(context.Background())
	if e != nil || len(l.Tools) != 2 {
		t.Fatalf("%+v %v", l, e)
	}
	c2, _ := startFixture(t, "loop")
	if _, e := c2.ListTools(context.Background()); e == nil {
		t.Fatal("cursor loop accepted")
	}
	for _, limit := range []Limits{{MaxPages: 1}, {MaxTools: 1}, {MaxListBytes: 20}} {
		cfg, _ := fixtureConfig(t, "pages")
		cfg.Limits = limit
		c, e := OpenStdio(context.Background(), cfg)
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.ListTools(context.Background())
		c.Close()
		if e == nil {
			t.Fatal("list cap not enforced")
		}
	}
}
func TestConcurrentDemuxAndLateCancellation(t *testing.T) {
	c, _ := startFixture(t, "modern")
	_, _ = c.ListTools(context.Background())
	var wg sync.WaitGroup
	for _, x := range []string{"slow", "fast"} {
		wg.Add(1)
		go func(x string) {
			defer wg.Done()
			r, e := c.CallTool(context.Background(), "echo", json.RawMessage(fmt.Sprintf(`{"x":%q}`, x)))
			if e != nil || !strings.Contains(string(r.Raw), `"echo":"`+x+`"`) {
				t.Errorf("%+v %v", r, e)
			}
		}(x)
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan wireReply, 1)
	go func() { r, e := c.CallTool(ctx, "echo", json.RawMessage(`{"x":"late"}`)); result <- wireReply{r, e} }()
	waitForPendingCall(t, c, "late")
	cancel()
	reply := <-result
	r, e := reply.result, reply.err
	if e == nil || r.Certainty != PossiblySent {
		t.Fatalf("%+v %v", r, e)
	}
	time.Sleep(600 * time.Millisecond)
	r, e = c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"after"}`))
	if e != nil || !strings.Contains(string(r.Raw), `"echo":"after"`) {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestEnvironmentAndCleanup(t *testing.T) {
	t.Setenv("MCP_PARENT_SECRET", "DO_NOT_INHERIT")
	c, _ := startFixture(t, "modern")
	_, _ = c.ListTools(context.Background())
	r, e := c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"ok"}`))
	if e != nil || !strings.Contains(string(r.Raw), `"secretInherited":false`) {
		t.Fatalf("%+v %v", r, e)
	}
	c.Close()
	c.Close()
	select {
	case <-c.Done():
	case <-time.After(time.Second):
		t.Fatal("process not reaped")
	}
	r, e = c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"ok"}`))
	if e == nil || r.Certainty != NotSent {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestQueuedCancellationVersusBlockedWrite(t *testing.T) {
	c, log := startFixture(t, "block-write")
	if _, e := c.ListTools(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	first := make(chan wireReply, 1)
	go func() {
		r, e := c.request(ctx, c.Version(), "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"x": strings.Repeat("x", 900000)}})
		first <- wireReply{r, e}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		writing := false
		for _, p := range c.pending {
			writing = writing || p.writing
		}
		c.mu.Unlock()
		if writing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not enter pipe write")
		}
		time.Sleep(time.Millisecond)
	}
	queued, done := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer done()
	r, e := c.request(queued, c.Version(), "tools/call", map[string]any{"name": "echo", "arguments": map[string]any{"x": "queued"}})
	if e == nil || r.Certainty != NotSent {
		t.Fatalf("queued request: %+v %v", r, e)
	}
	reply := <-first
	if reply.err == nil || reply.result.Certainty != PossiblySent {
		t.Fatalf("mid-write: %+v %v", reply.result, reply.err)
	}
	c.Close()
	b, _ := os.ReadFile(log)
	if strings.Contains(string(b), "queued") {
		t.Fatal("cancelled queued request transmitted")
	}
}
func TestPendingBound(t *testing.T) {
	cfg, _ := fixtureConfig(t, "modern")
	cfg.Limits.MaxPending = 1
	c, e := OpenStdio(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() { defer close(finished); _, _ = c.CallTool(ctx, "echo", json.RawMessage(`{"x":"hang"}`)) }()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		busy := false
		for _, p := range c.pending {
			busy = strings.Contains(string(p.raw), `"method":"tools/call"`) && p.attempted
		}
		c.mu.Unlock()
		if busy {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no pending call")
		}
		time.Sleep(time.Millisecond)
	}
	if _, e = c.ListTools(context.Background()); e == nil {
		t.Fatal("pending cap not enforced")
	}
	cancel()
	<-finished
}
func TestNotificationsAreBounded(t *testing.T) {
	c, _ := startFixture(t, "modern")
	r, e := c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"flood"}`))
	if e != nil {
		t.Fatalf("%+v %v", r, e)
	}
	if c.DroppedNotifications() == 0 {
		t.Fatal("unbounded notifications")
	}
	select {
	case n := <-c.Notifications():
		if n.Method != "notifications/progress" {
			t.Fatal(n)
		}
	default:
		t.Fatal("missing notification")
	}
}
func TestSchemaPinAndUnsupportedNeverCall(t *testing.T) {
	for _, mode := range []string{"modern", "unsupported-schema", "invalid-output-schema"} {
		c, log := startFixture(t, mode)
		digest := ""
		if mode == "modern" {
			digest = "incorrect"
		}
		r, e := c.CallToolPinned(context.Background(), "echo", json.RawMessage(`{"x":"ok"}`), digest)
		if e == nil || r.Certainty != NotSent {
			t.Fatalf("%+v %v", r, e)
		}
		c.Close()
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), `"method":"tools/call"`) {
			t.Fatal("preflight transmitted tool call")
		}
	}
}
func TestExplicitUnsupportedAndCommand(t *testing.T) {
	for _, name := range []string{"http", "streamable-http", "sse", "tasks", "input_required"} {
		if UnsupportedCapability(name) == nil {
			t.Fatal(name)
		}
	}
	if _, e := OpenStdio(context.Background(), StdioConfig{Command: "relative-command"}); e == nil {
		t.Fatal("ambiguous executable accepted")
	}
}

func waitForPendingCall(t *testing.T, c *StdioClient, value string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.mu.Lock()
		found := false
		for _, p := range c.pending {
			found = found || (p.attempted && !p.writing && strings.Contains(string(p.raw), `"method":"tools/call"`) && strings.Contains(string(p.raw), `"x":"`+value+`"`))
		}
		c.mu.Unlock()
		if found {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tools/call was not sent")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestResponseCannotCompleteUnsentQueuedRequest(t *testing.T) {
	c, _ := startFixture(t, "spoof-queued")
	if _, e := c.ListTools(context.Background()); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = c.request(ctx, c.Version(), "tools/call", map[string]any{"x": strings.Repeat("x", 900000)})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		writing := false
		for _, p := range c.pending {
			writing = writing || p.writing
		}
		c.mu.Unlock()
		if writing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no writer")
		}
		time.Sleep(time.Millisecond)
	}
	r, e := c.request(ctx, c.Version(), "tools/call", map[string]any{"x": "queued"})
	if e == nil || r.Certainty != NotSent {
		t.Fatalf("unsent request accepted forged reply: %+v %v", r, e)
	}
	c.Close()
	<-finished
}

func TestConfiguredFrameLimitBelowScannerBuffer(t *testing.T) {
	cfg, _ := fixtureConfig(t, "modern")
	cfg.Limits.MaxFrameBytes = 1024
	c, e := OpenStdio(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r, e := c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":"medium-frame"}`))
	if e == nil || r.Certainty != PossiblySent {
		t.Fatalf("configured frame cap bypassed: %+v %v", r, e)
	}
}

func TestNumericAndReferenceWireCertainty(t *testing.T) {
	if isolatedSchemaTest(t) {
		return
	}
	for _, tc := range []struct {
		mode, input string
		certainty   Certainty
		calls       int
	}{
		{"numeric-input", `{"x":1e1000001}`, NotSent, 0},
		{"numeric-input", `{"x":1e-1000001}`, NotSent, 0},
		{"numeric-input-schema", `{"x":1}`, NotSent, 0},
		{"numeric-input-schema-negative", `{"x":1}`, NotSent, 0},
		{"numeric-output-schema", `{"x":1}`, NotSent, 0},
		{"numeric-output-schema-negative", `{"x":1}`, NotSent, 0},
		{"hidden-input-schema", `{}`, NotSent, 0},
		{"hidden-output-schema", `{"x":"ok"}`, NotSent, 0},
		{"numeric-output", `{"x":1}`, KnownResponse, 1},
		{"numeric-output-negative", `{"x":1}`, KnownResponse, 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			c, log := startFixture(t, tc.mode)
			r, e := c.CallTool(context.Background(), "echo", json.RawMessage(tc.input))
			if e == nil || r.Certainty != tc.certainty {
				t.Fatalf("certainty=%s want=%s err=%v", r.Certainty, tc.certainty, e)
			}
			var ce *CallError
			if !errors.As(e, &ce) || ce.Certainty != tc.certainty {
				t.Fatal("typed certainty missing", e)
			}
			c.Close()
			b, _ := os.ReadFile(log)
			if n := strings.Count(string(b), `"method":"tools/call"`); n != tc.calls {
				t.Fatalf("tools/call count=%d want=%d", n, tc.calls)
			}
		})
	}
}
