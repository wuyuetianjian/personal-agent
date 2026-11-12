//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_persistent && nativeprobe_sqlite_main_read && nativeprobe_sqlite_existing_wal && nativeprobe_sqlite_access && nativeprobe_sqlite_private_shm && nativeprobe_sqlite_shm_resources && nativeprobe_sqlite_shm_slots && nativeprobe_sqlite_private_sql && nativeprobe_sqlite_private_sql_interop

package windowsprobe

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const privateSQLInteropEnv = "AGENT_PRIVATE_SQL_INTEROP_FIXTURE"
const interopBatch = 128

func interopValue(i int) string {
	return fmt.Sprintf("row-%06d:", i) + strings.Repeat(string(rune('a'+i%26)), 2048)
}

type interopReply struct {
	Kind                           string
	Count, Busy, Log, Checkpointed int
	Error                          string
}

// Stock's default VFS is used only in this independent process. The writer
// connection remains open through reader release, append and checkpoint epochs.
func privateSQLInteropChild(t *testing.T) bool {
	path := os.Getenv(privateSQLInteropEnv)
	if path == "" {
		return false
	}
	ctx := context.Background()
	db, e := sql.Open("sqlite", existingWALURI(path, "rwc", ""))
	shmResourceCheck(t, e)
	db.SetMaxOpenConns(1)
	c, e := db.Conn(ctx)
	if e != nil {
		shmResourceCheck(t, errors.Join(e, db.Close()))
	}
	var tx *sql.Tx
	defer func() {
		var er error
		if tx != nil {
			er = tx.Rollback()
		}
		shmResourceCheck(t, errors.Join(er, c.Close(), db.Close()))
	}()
	var mode string
	shmResourceCheck(t, c.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode))
	if mode != "wal" {
		t.Fatal(mode)
	}
	_, e = c.ExecContext(ctx, "PRAGMA wal_autocheckpoint=0")
	shmResourceCheck(t, e)
	_, e = c.ExecContext(ctx, "CREATE TABLE fixture(value TEXT NOT NULL)")
	shmResourceCheck(t, e)
	checkpoint := func() interopReply {
		r := interopReply{Kind: "checkpoint"}
		shmResourceCheck(t, c.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&r.Busy, &r.Log, &r.Checkpointed))
		return r
	}
	baseline := checkpoint()
	if baseline.Busy != 0 || baseline.Log != 0 || baseline.Checkpointed != 0 {
		t.Fatalf("baseline checkpoint %+v", baseline)
	}
	count := 0
	appendBatch := func() {
		if tx != nil {
			shmResourceCheck(t, tx.Rollback())
			tx = nil
		}
		w, e := c.BeginTx(ctx, nil)
		shmResourceCheck(t, e)
		p, e := w.PrepareContext(ctx, "INSERT INTO fixture VALUES(?)")
		if e != nil {
			shmResourceCheck(t, errors.Join(e, w.Rollback()))
		}
		for i := count; i < count+interopBatch; i++ {
			if _, e = p.ExecContext(ctx, interopValue(i)); e != nil {
				shmResourceCheck(t, errors.Join(e, p.Close(), w.Rollback()))
			}
		}
		shmResourceCheck(t, p.Close())
		shmResourceCheck(t, w.Commit())
		count += interopBatch
		tx, e = c.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		shmResourceCheck(t, e)
		var observed int
		shmResourceCheck(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM fixture").Scan(&observed))
		if observed != count {
			t.Fatalf("held stock count=%d expected=%d", observed, count)
		}
	}
	send := func(r interopReply) { shmResourceCheck(t, json.NewEncoder(os.Stdout).Encode(r)) }
	appendBatch()
	send(interopReply{Kind: "ready", Count: count})
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		switch scanner.Text() {
		case "append":
			appendBatch()
			send(interopReply{Kind: "append", Count: count})
		case "checkpoint":
			if tx != nil {
				shmResourceCheck(t, tx.Rollback())
				tx = nil
			}
			r := checkpoint()
			shmResourceCheck(t, c.QueryRowContext(ctx, "SELECT count(*) FROM fixture").Scan(&r.Count))
			send(r)
		case "stop":
			if tx != nil {
				shmResourceCheck(t, tx.Rollback())
				tx = nil
			}
			send(interopReply{Kind: "stopped", Count: count})
			return true
		default:
			t.Fatal("unexpected stock command")
		}
	}
	shmResourceCheck(t, scanner.Err())
	t.Fatal("stock controller vanished without stop")
	return true
}

type interopStock struct {
	path    string
	input   io.WriteCloser
	lines   <-chan string
	done    <-chan error
	cmd     *exec.Cmd
	mu      sync.Mutex
	stopped bool
	result  error
}

func (s *interopStock) exchange(command string) (interopReply, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r interopReply
	if s.stopped {
		return r, errors.New("stock already stopped")
	}
	if command != "" {
		if _, e := fmt.Fprintln(s.input, command); e != nil {
			return r, e
		}
	}
	select {
	case line, ok := <-s.lines:
		if !ok {
			return r, errors.New("stock response EOF")
		}
		if e := json.Unmarshal([]byte(line), &r); e != nil {
			return r, e
		}
		if r.Error != "" {
			return r, errors.New(r.Error)
		}
		return r, nil
	case <-time.After(20 * time.Second):
		return r, errors.New("stock response timeout")
	}
}
func (s *interopStock) stop() error {
	s.mu.Lock()
	if s.stopped {
		e := s.result
		s.mu.Unlock()
		return e
	}
	s.mu.Unlock()
	r, e := s.exchange("stop")
	if e == nil && r.Kind != "stopped" {
		e = fmt.Errorf("unexpected stop reply %+v", r)
	}
	e = errors.Join(e, s.input.Close())
	select {
	case err := <-s.done:
		e = errors.Join(e, err)
	case <-time.After(10 * time.Second):
		e = errors.Join(e, errors.New("stock exit timeout"), s.cmd.Process.Kill())
		select {
		case err := <-s.done:
			e = errors.Join(e, err)
		case <-time.After(5 * time.Second):
			e = errors.Join(e, errors.New("stock kill not confirmed"))
		}
	}
	s.mu.Lock()
	s.stopped = true
	s.result = e
	s.mu.Unlock()
	return e
}
func newInteropStock(t *testing.T) *interopStock {
	t.Helper()
	path := filepath.Join(localRootPath(t), "interop.db")
	cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.timeout=150s")
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(k, privateSQLInteropEnv) && !strings.EqualFold(k, existingWALChildEnv) && !strings.EqualFold(k, privateSQLRetainedEnv) {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, privateSQLInteropEnv+"="+path)
	cmd.Stderr = os.Stderr
	out, e := cmd.StdoutPipe()
	shmResourceCheck(t, e)
	in, e := cmd.StdinPipe()
	if e != nil {
		shmResourceCheck(t, errors.Join(e, out.Close()))
	}
	if e = cmd.Start(); e != nil {
		shmResourceCheck(t, errors.Join(e, in.Close(), out.Close()))
	}
	lines := make(chan string, 16)
	done := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(out)
		for scan.Scan() {
			line := scan.Text()
			if strings.HasPrefix(line, "{") {
				lines <- line
			}
		}
		close(lines)
		done <- errors.Join(scan.Err(), cmd.Wait())
	}()
	s := &interopStock{path: path, input: in, lines: lines, done: done, cmd: cmd}
	t.Cleanup(func() { shmResourceCheck(t, s.stop()) })
	r, e := s.exchange("")
	shmResourceCheck(t, e)
	if r.Kind != "ready" || r.Count != interopBatch {
		t.Fatalf("ready %+v", r)
	}
	baseline, e := sql.Open("sqlite", existingWALURI(path, "ro", "")+"&immutable=1")
	shmResourceCheck(t, e)
	var mainCount int
	qerr := baseline.QueryRow("SELECT count(*) FROM fixture").Scan(&mainCount)
	shmResourceCheck(t, errors.Join(qerr, baseline.Close()))
	if mainCount != 0 {
		t.Fatalf("rows not exclusively WAL: %d", mainCount)
	}
	st, e := os.Stat(path + "-wal")
	shmResourceCheck(t, e)
	if st.Size() < 256*1024 {
		t.Fatalf("WAL not multi-page: %d", st.Size())
	}
	interopStockRows(t, path, interopBatch)
	return s
}
func interopStockRows(t *testing.T, path string, n int) {
	t.Helper()
	db, e := sql.Open("sqlite", existingWALURI(path, "ro", ""))
	shmResourceCheck(t, e)
	rows, e := db.Query("SELECT value FROM fixture")
	if e != nil {
		shmResourceCheck(t, errors.Join(e, db.Close()))
	}
	seen := make(map[string]bool, n)
	for rows.Next() {
		var value string
		if e = rows.Scan(&value); e != nil {
			break
		}
		if seen[value] {
			e = errors.New("duplicate stock payload")
			break
		}
		seen[value] = true
	}
	e = errors.Join(e, rows.Err(), rows.Close(), db.Close())
	shmResourceCheck(t, e)
	interopCheckValues(t, seen, n)
}
func interopCheckValues(t *testing.T, seen map[string]bool, n int) {
	t.Helper()
	if len(seen) != n {
		t.Fatalf("snapshot rows=%d want=%d", len(seen), n)
	}
	for i := 0; i < n; i++ {
		if !seen[interopValue(i)] {
			t.Fatalf("missing full payload %d", i)
		}
	}
}
func interopPrivateRows(t *testing.T, f *privateSQLFixture) (*privateSQLRows, map[string]bool) {
	t.Helper()
	r, e := f.s.query(context.Background(), "SELECT value FROM fixture")
	shmResourceCheck(t, e)
	t.Cleanup(func() { shmResourceCheck(t, r.close()) })
	if !r.next() {
		t.Fatalf("private first row: %v", r.err())
	}
	var value string
	shmResourceCheck(t, r.scan(&value))
	privateSQLWitness(t, f, r)
	return r, map[string]bool{value: true}
}
func interopDrain(t *testing.T, r *privateSQLRows, seen map[string]bool, n int) {
	t.Helper()
	for r.next() {
		var value string
		shmResourceCheck(t, r.scan(&value))
		if seen[value] {
			t.Fatal("duplicate row")
		}
		seen[value] = true
	}
	shmResourceCheck(t, errors.Join(r.err(), r.close()))
	interopCheckValues(t, seen, n)
}
func TestPrivateSQLStockAppendSnapshot(t *testing.T) {
	if privateSQLInteropChild(t) {
		return
	}
	stock := newInteropStock(t)
	f := newPrivateSQLFixture(t, &privateSQLStock{stock.path, stock.stop})
	r, seen := interopPrivateRows(t, f)
	reply, e := stock.exchange("append")
	shmResourceCheck(t, e)
	if reply.Kind != "append" || reply.Count != 2*interopBatch {
		t.Fatalf("append %+v", reply)
	}
	interopStockRows(t, stock.path, 2*interopBatch)
	privateSQLWitness(t, f, r)
	interopDrain(t, r, seen, interopBatch)
	newer, seen := interopPrivateRows(t, f)
	interopDrain(t, newer, seen, 2*interopBatch)
	shmResourceCheck(t, f.s.close(context.Background()))
	interopStockRows(t, stock.path, 2*interopBatch)
}
func TestPrivateSQLQuiescentCheckpointReopen(t *testing.T) {
	if privateSQLInteropChild(t) {
		return
	}
	stock := newInteropStock(t)
	old := newPrivateSQLFixture(t, &privateSQLStock{stock.path, stock.stop})
	r, seen := interopPrivateRows(t, old)
	interopDrain(t, r, seen, interopBatch)
	shmResourceCheck(t, old.s.close(context.Background()))
	reply, e := stock.exchange("checkpoint")
	shmResourceCheck(t, e)
	if reply.Kind != "checkpoint" || reply.Busy != 0 || reply.Log != 0 || reply.Checkpointed != 0 || reply.Count != interopBatch {
		t.Fatalf("quiescent truncate %+v", reply)
	}
	st, e := os.Stat(stock.path + "-wal")
	shmResourceCheck(t, e)
	if st.Size() != 0 {
		t.Fatalf("TRUNCATE WAL size=%d", st.Size())
	}
	interopStockRows(t, stock.path, interopBatch)
	reply, e = stock.exchange("append")
	shmResourceCheck(t, e)
	if reply.Kind != "append" || reply.Count != 2*interopBatch {
		t.Fatalf("reappend %+v", reply)
	}
	rejected, e := sql.Open("sqlite", old.uri)
	shmResourceCheck(t, e)
	var value string
	qerr := rejected.QueryRow("SELECT value FROM fixture").Scan(&value)
	cerr := rejected.Close()
	if qerr == nil || cerr != nil {
		t.Fatalf("retired binding query=%v close=%v", qerr, cerr)
	}
	fresh := newPrivateSQLFixture(t, &privateSQLStock{stock.path, stock.stop})
	r, seen = interopPrivateRows(t, fresh)
	interopDrain(t, r, seen, 2*interopBatch)
	shmResourceCheck(t, fresh.s.close(context.Background()))
	interopStockRows(t, stock.path, 2*interopBatch)
}
