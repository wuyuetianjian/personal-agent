package mcp

// This is a deliberately small, opt-in stdio implementation, not an MCP SDK.
// The caller owns authorization, durable effect records and reconciliation.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const ModernVersion = "2026-07-28"
const LegacyVersion = "2024-11-05"

type Certainty string

const (
	NotSent       Certainty = "not_sent"
	PossiblySent  Certainty = "possibly_sent"
	KnownResponse Certainty = "known_response"
)

// CallError deliberately omits server messages/data, stderr, argv and env.
// KnownResponse says a reply was received, NOT that an effect was rolled back.
// Only NotSent is safe for a caller to consider retrying without reconciliation.
type CallError struct {
	Certainty Certainty
	Kind      string
	Code      int
	IsRPC     bool
	cause     error
	data      json.RawMessage
}

func (e *CallError) Error() string {
	if e.IsRPC {
		return fmt.Sprintf("mcp %s (%s, RPC %d)", e.Kind, e.Certainty, e.Code)
	}
	return fmt.Sprintf("mcp %s (%s)", e.Kind, e.Certainty)
}
func (e *CallError) Unwrap() error { return e.cause }
func failure(c Certainty, kind string, cause error) *CallError {
	return &CallError{Certainty: c, Kind: kind, cause: cause}
}

// PreflightError marks failure before any tools/call, even if discovery was sent.
func PreflightError(err error) error { return failure(NotSent, "preflight failed", safeCause(err)) }

type CallResult struct {
	RequestID string
	Certainty Certainty
	Raw       json.RawMessage
	IsError   bool
}

type Limits struct{ MaxFrameBytes, MaxPending, MaxPages, MaxTools, MaxListBytes int }

func (l Limits) defaults() Limits {
	if l.MaxFrameBytes <= 0 || l.MaxFrameBytes > 4<<20 {
		l.MaxFrameBytes = 1 << 20
	}
	if l.MaxPending <= 0 || l.MaxPending > 256 {
		l.MaxPending = 32
	}
	if l.MaxPages <= 0 || l.MaxPages > 128 {
		l.MaxPages = 32
	}
	if l.MaxTools <= 0 || l.MaxTools > 10000 {
		l.MaxTools = 1000
	}
	if l.MaxListBytes <= 0 || l.MaxListBytes > 32<<20 {
		l.MaxListBytes = 8 << 20
	}
	return l
}

// Command must be an explicitly trusted absolute executable. No shell is added.
// Launch is NON-SANDBOXED current-user code. Env is the complete explicit child
// environment; nil means empty, NOT os.Environ(). OS-required variables may be
// added by os/exec (notably SystemRoot on Windows). Do not pass credentials in argv.
type StdioConfig struct {
	Command                      string
	Args, Env                    []string
	Dir                          string
	ProbeTimeout, RequestTimeout time.Duration
	Limits                       Limits
}
type Notification struct {
	Method string
	Params json.RawMessage
}
type pending struct {
	id                           string
	raw                          []byte
	ctx                          context.Context
	attempted, writing, terminal bool
	ch                           chan wireReply
}
type wireReply struct {
	result CallResult
	err    error
}
type writeItem struct {
	p   *pending
	raw []byte
	ack chan error
}

type StdioClient struct {
	cfg                    StdioConfig
	cmd                    *exec.Cmd
	stdin, stdout          *os.File
	mu                     sync.Mutex
	pending                map[string]*pending
	next                   uint64
	closed                 bool
	terminal               error
	queue                  chan writeItem
	done, reaped, readDone chan struct{}
	wg                     sync.WaitGroup
	notifications          chan Notification
	dropped                atomic.Uint64
	version                string // immutable after OpenStdio returns
	capabilities           map[string]json.RawMessage
}

// OpenStdio negotiates only with side-effect-free discovery. No tools/call is
// ever retried, reconnected, or used as a version probe. ctx owns process lifetime.
func OpenStdio(ctx context.Context, cfg StdioConfig) (*StdioClient, error) {
	if err := ctx.Err(); err != nil {
		return nil, failure(NotSent, "cancelled", err)
	}
	if !filepath.IsAbs(cfg.Command) {
		return nil, failure(NotSent, "absolute trusted command required", nil)
	}
	cfg.Limits = cfg.Limits.defaults()
	if cfg.ProbeTimeout <= 0 || cfg.ProbeTimeout > 10*time.Second {
		cfg.ProbeTimeout = time.Second
	}
	if cfg.RequestTimeout <= 0 || cfg.RequestTimeout > 10*time.Minute {
		cfg.RequestTimeout = 30 * time.Second
	}
	inR, inW, err := os.Pipe()
	if err != nil {
		return nil, failure(NotSent, "pipe", nil)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		inR.Close()
		inW.Close()
		return nil, failure(NotSent, "pipe", nil)
	}
	stderr, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		inR.Close()
		inW.Close()
		outR.Close()
		outW.Close()
		return nil, failure(NotSent, "stderr", nil)
	}
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = append([]string{}, cfg.Env...)
	cmd.Stdin = inR
	cmd.Stdout = outW
	cmd.Stderr = stderr
	err = cmd.Start()
	inR.Close()
	outW.Close()
	stderr.Close()
	if err != nil {
		inW.Close()
		outR.Close()
		return nil, failure(NotSent, "launch failed", nil)
	}
	c := &StdioClient{cfg: cfg, cmd: cmd, stdin: inW, stdout: outR, pending: map[string]*pending{}, queue: make(chan writeItem, cfg.Limits.MaxPending), done: make(chan struct{}), reaped: make(chan struct{}), readDone: make(chan struct{}), notifications: make(chan Notification, 32)}
	c.wg.Add(4)
	go c.readLoop()
	go c.writeLoop()
	go func() {
		defer c.wg.Done()
		select {
		case <-ctx.Done():
			c.fail(ctx.Err())
		case <-c.done:
		}
	}()
	go func() {
		defer c.wg.Done()
		_ = cmd.Wait()
		close(c.reaped)
		select {
		case <-c.readDone:
		case <-time.After(100 * time.Millisecond):
		}
		c.fail(io.EOF)
	}()
	if err = c.negotiate(ctx); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func (c *StdioClient) Version() string       { return c.version }
func (c *StdioClient) Done() <-chan struct{} { return c.reaped }

// Notifications are untrusted, bounded and lossy. They never reset deadlines.
func (c *StdioClient) Notifications() <-chan Notification { return c.notifications }
func (c *StdioClient) DroppedNotifications() uint64       { return c.dropped.Load() }
func (c *StdioClient) Close() error {
	c.fail(errors.New("closed"))
	<-c.reaped
	c.wg.Wait()
	return nil
}
func (c *StdioClient) fail(cause error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.terminal = cause
	for id, p := range c.pending {
		p.terminal = true
		certainty := NotSent
		if p.attempted {
			certainty = PossiblySent
		}
		p.ch <- wireReply{CallResult{RequestID: id, Certainty: certainty}, failure(certainty, "transport closed", safeCause(cause))}
		delete(c.pending, id)
	}
	close(c.done)
	c.mu.Unlock()
	// Kill only the direct process. No claim of descendant containment.
	_ = c.cmd.Process.Kill()
	_ = c.stdin.Close()
	_ = c.stdout.Close()
}
func safeCause(e error) error {
	if errors.Is(e, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return nil
}
func (c *StdioClient) writeLoop() {
	defer c.wg.Done()
	for {
		select {
		case <-c.done:
			return
		case item := <-c.queue:
			if item.p != nil {
				c.mu.Lock()
				p := item.p
				if p.terminal || p.ctx.Err() != nil || c.closed {
					c.mu.Unlock()
					continue
				}
				p.attempted = true
				p.writing = true
				c.mu.Unlock()
			}
			// Closing the pipe/process releases blocked writes on supported native OSes.
			timer := time.AfterFunc(c.cfg.RequestTimeout, func() { c.fail(context.DeadlineExceeded) })
			_, err := c.stdin.Write(item.raw)
			timer.Stop()
			if item.p != nil {
				c.mu.Lock()
				item.p.writing = false
				c.mu.Unlock()
			}
			if item.ack != nil {
				item.ack <- err
			}
			if err != nil {
				c.fail(err)
				return
			}
		}
	}
}
func (c *StdioClient) request(ctx context.Context, version, method string, params map[string]any) (CallResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.RequestTimeout)
	defer cancel()
	if e := ctx.Err(); e != nil {
		return CallResult{Certainty: NotSent}, failure(NotSent, "cancelled", e)
	}
	body := make(map[string]any, len(params)+1)
	for k, v := range params {
		body[k] = v
	}
	if version == ModernVersion {
		body["_meta"] = map[string]any{"io.modelcontextprotocol/protocolVersion": version, "io.modelcontextprotocol/clientCapabilities": map[string]any{}, "io.modelcontextprotocol/clientInfo": map[string]any{"name": "personal-agent", "version": "1"}}
	}
	c.mu.Lock()
	if c.closed || len(c.pending) >= c.cfg.Limits.MaxPending || c.next == ^uint64(0) {
		c.mu.Unlock()
		return CallResult{Certainty: NotSent}, failure(NotSent, "unavailable or pending limit", nil)
	}
	c.next++
	id := strconv.FormatUint(c.next, 10)
	raw, e := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": body})
	if e != nil || len(raw)+1 > c.cfg.Limits.MaxFrameBytes {
		c.mu.Unlock()
		return CallResult{Certainty: NotSent}, failure(NotSent, "request frame limit or invalid JSON", nil)
	}
	p := &pending{id: id, raw: append(raw, '\n'), ctx: ctx, ch: make(chan wireReply, 1)}
	c.pending[id] = p
	c.mu.Unlock()
	select {
	case c.queue <- writeItem{p: p, raw: p.raw}:
	case <-ctx.Done():
	case <-c.done:
	}
	select {
	case reply := <-p.ch:
		return reply.result, reply.err
	case <-ctx.Done():
		c.mu.Lock()
		if p.terminal {
			c.mu.Unlock()
			r := <-p.ch
			return r.result, r.err
		}
		p.terminal = true
		delete(c.pending, id)
		attempted, writing := p.attempted, p.writing
		c.mu.Unlock()
		certainty := NotSent
		if attempted {
			certainty = PossiblySent
		}
		if writing {
			c.fail(ctx.Err())
		} else if attempted {
			c.cancelRequest(id)
		}
		return CallResult{RequestID: id, Certainty: certainty}, failure(certainty, "cancelled", ctx.Err())
	}
}
func (c *StdioClient) cancelRequest(id string) {
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": id}})
	select {
	case c.queue <- writeItem{raw: append(raw, '\n')}:
	case <-c.done:
	default:
		c.fail(errors.New("cancellation queue full"))
	}
}
func (c *StdioClient) initialized(ctx context.Context) error {
	ack := make(chan error, 1)
	item := writeItem{raw: []byte("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\"}\n"), ack: ack}
	select {
	case c.queue <- item:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return failure(NotSent, "closed", nil)
	}
	select {
	case e := <-ack:
		return e
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return failure(NotSent, "closed", nil)
	}
}
func (c *StdioClient) readLoop() {
	defer c.wg.Done()
	defer close(c.readDone)
	defer close(c.notifications)
	scanner := bufio.NewScanner(c.stdout)
	scanner.Buffer(make([]byte, min(4096, c.cfg.Limits.MaxFrameBytes)), c.cfg.Limits.MaxFrameBytes)
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(raw)+1 > c.cfg.Limits.MaxFrameBytes {
			c.fail(errors.New("frame limit"))
			return
		}
		if _, e := decodeWireJSON(raw); e != nil {
			c.fail(errors.New("malformed frame"))
			return
		}
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
			Result  json.RawMessage `json:"result"`
			Error   json.RawMessage `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.JSONRPC != "2.0" {
			c.fail(errors.New("invalid envelope"))
			return
		}
		if msg.Method != "" {
			if len(msg.ID) != 0 || len(msg.Result) != 0 || len(msg.Error) != 0 {
				c.fail(errors.New("unsupported server request"))
				return
			}
			if len(msg.Params) > 0 && msg.Params[0] != '{' {
				c.fail(errors.New("invalid notification"))
				return
			}
			n := Notification{msg.Method, append(json.RawMessage(nil), msg.Params...)}
			select {
			case c.notifications <- n:
			default:
				c.dropped.Add(1)
			}
			continue
		}
		id, valid := responseID(msg.ID)
		if !valid || (len(msg.Result) == 0) == (len(msg.Error) == 0) {
			c.fail(errors.New("invalid response"))
			return
		}
		var rpc *CallError
		if len(msg.Error) > 0 {
			var e struct {
				Code    *int            `json:"code"`
				Message *string         `json:"message"`
				Data    json.RawMessage `json:"data"`
			}
			if json.Unmarshal(msg.Error, &e) != nil || e.Code == nil || e.Message == nil {
				c.fail(errors.New("invalid RPC error"))
				return
			}
			rpc = &CallError{Certainty: KnownResponse, Kind: "RPC error", Code: *e.Code, IsRPC: true, data: e.Data}
		} else if len(msg.Result) == 0 || msg.Result[0] != '{' {
			c.fail(errors.New("invalid result"))
			return
		}
		c.mu.Lock()
		p := c.pending[id]
		if p != nil && !p.attempted {
			c.mu.Unlock()
			c.fail(errors.New("response for unsent request"))
			return
		}
		if p != nil && !p.terminal {
			p.terminal = true
			delete(c.pending, id)
			r := wireReply{result: CallResult{RequestID: id, Certainty: KnownResponse, Raw: append(json.RawMessage(nil), msg.Result...)}}
			if rpc != nil {
				r.err = rpc
			}
			p.ch <- r
		}
		c.mu.Unlock()
	}
	c.fail(io.EOF)
}
func responseID(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return "", false
		}
		return id, true
	}
	// Numeric IDs are valid but cannot match the string IDs this client issues.
	if _, err := strconv.ParseInt(string(raw), 10, 64); err != nil {
		return "", false
	}
	return "numeric:" + string(raw), true
}
