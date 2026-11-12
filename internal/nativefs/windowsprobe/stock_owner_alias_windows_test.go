//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sidecar_diagnostic && nativeprobe_stock_epoch && nativeprobe_stock_alias

package windowsprobe

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stockAliasResult struct {
	Phase      string
	Code       int
	Message    string
	CloseError string
	Rows       int
	Mode       string
}

// Isolated stock process. A reported SQLite error is data for this diagnostic,
// not permission to call an unrelated setup/timeout failure a security refusal.
func TestStockOwnerAliasChild(t *testing.T) {
	name, payload := os.Getenv("AGENT_STOCK_ALIAS_DB"), os.Getenv("AGENT_STOCK_ALIAS_PAYLOAD")
	if name == "" || payload == "" {
		t.Fatal("missing isolated alias fixture")
	}
	db, e := sql.Open("sqlite", name)
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(1)
	result := stockAliasResult{}
	reject := func(phase string, e error) bool {
		if e == nil {
			return false
		}
		var coded interface{ Code() int }
		if !errors.As(e, &coded) {
			t.Errorf("non-SQLite fixture error at %s: %v", phase, e)
			return true
		}
		result.Phase = phase
		result.Code = coded.Code()
		result.Message = e.Error()
		return true
	}
	func() {
		if reject("journal-mode-read", db.QueryRow("PRAGMA journal_mode").Scan(&result.Mode)) {
			return
		}
		if result.Mode != "wal" {
			t.Error("alias mode not WAL; no mode changes permitted")
			return
		}
		var n int
		if reject("baseline-select", db.QueryRow("SELECT count(*) FROM fixture WHERE v=?", payload).Scan(&n)) {
			return
		}
		if n != 1 {
			t.Errorf("alias did not read checkpointed baseline: count %d", n)
			return
		}
		if _, e = db.Exec("INSERT INTO fixture SELECT v FROM fixture WHERE v=?", payload); reject("insert-select", e) {
			return
		}
		if reject("committed-select", db.QueryRow("SELECT count(*) FROM fixture WHERE v=?", payload).Scan(&result.Rows)) {
			return
		}
		if result.Rows != 2 {
			t.Errorf("alias committed rows %d", result.Rows)
			return
		}
		result.Phase = "committed"
	}()
	if e = db.Close(); e != nil {
		result.CloseError = e.Error()
		t.Errorf("alias close: %v", e)
	}
	raw, e := json.Marshal(result)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fmt.Printf("STOCK_ALIAS_RESULT %s\n", raw); e != nil {
		t.Fatal(e)
	}
}
func runStockAliasChild(t *testing.T, name, payload string) stockAliasResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStockOwnerAliasChild$", "-test.timeout=40s")
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = append(os.Environ(), "AGENT_STOCK_ALIAS_DB="+name, "AGENT_STOCK_ALIAS_PAYLOAD="+payload)
	raw, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("alias subprocess setup/execution (not SQLite refusal): %v; %s", e, raw)
	}
	var result stockAliasResult
	n := 0
	s := bufio.NewScanner(bytes.NewReader(raw))
	for s.Scan() {
		if strings.HasPrefix(s.Text(), "STOCK_ALIAS_RESULT ") {
			n++
			if e = json.Unmarshal([]byte(strings.TrimPrefix(s.Text(), "STOCK_ALIAS_RESULT ")), &result); e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = s.Err(); e != nil {
		t.Fatal(e)
	}
	if n != 1 || result.Phase == "" || result.CloseError != "" {
		t.Fatalf("invalid child result %+v count%d", result, n)
	}
	if result.Code == 0 && (result.Phase != "committed" || result.Rows != 2 || result.Mode != "wal") {
		t.Fatalf("incomplete alias SQL control %+v", result)
	}
	if result.Code != 0 && result.Message == "" {
		t.Fatal("missing native SQLite rejection details")
	}
	return result
}
func stockAliasTarget(t *testing.T, name string) idInfo {
	t.Helper()
	p, e := windows.UTF16PtrFromString(name)
	if e != nil {
		t.Fatal(e)
	}
	// Deliberately follow this test-created file symlink, independently of SQLite.
	h, e := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		t.Fatalf("native alias target setup %v", e)
	}
	s, e := inspect(h)
	ce := windows.CloseHandle(h)
	if e != nil || ce != nil {
		t.Fatalf("alias target metadata %v %v", e, ce)
	}
	if s.Standard.Directory != 0 || s.Standard.Links != 1 {
		t.Fatal("alias target not same single-link regular main")
	}
	return s.ID
}
func TestStockOwnerAliasDiagnostic(t *testing.T) {
	dir := localRootPath(t)
	name := filepath.Join(dir, "epoch.db")
	owner := newStockEpochOwner(t, name)
	payload := "synthetic-alias-baseline-payload-" + filepath.Base(dir)
	if _, e := owner.conn.ExecContext(context.Background(), "UPDATE fixture SET v=?", payload); e != nil {
		t.Fatal(e)
	}
	var busy, log, checkpointed int
	if e := owner.conn.QueryRowContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); e != nil || busy != 0 {
		t.Fatalf("checkpoint baseline %d/%d/%d: %v", busy, log, checkpointed, e)
	}
	main, e := os.ReadFile(name)
	if e != nil || !bytes.Contains(main, []byte(payload)) {
		t.Fatalf("baseline payload not in main: %v", e)
	}
	ids := stockEpochIDs(t, name)
	excluded := map[idInfo]struct{}{}
	for _, id := range ids {
		excluded[id] = struct{}{}
	}
	stockEpochLockWitness(t, name, 0x40000002, 510, true)
	stockEpochLockWitness(t, name+"-shm", 128, 1, true)
	alias := filepath.Join(dir, "alias.db")
	if e = os.Symlink(name, alias); e != nil {
		t.Fatalf("real symlink setup failed, not security rejection: %v", e)
	}
	st, e := os.Lstat(alias)
	if e != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("fixture is not symlink %v", e)
	}
	if stockAliasTarget(t, alias) != ids[0] {
		t.Fatal("symlink target differs from live owner main")
	}
	t.Log("STOCK_ALIAS_NATIVE_CONTROL: real file symlink follows to same single-link main identity while canonical owner retains main/DMS")
	result := runStockAliasChild(t, alias, payload)
	t.Logf("stock alias SQL phase=%s code=%d mode=%q committedRows=%d error=%q", result.Phase, result.Code, result.Mode, result.Rows, result.Message)
	if now := stockEpochIDs(t, name); now != ids {
		t.Fatal("canonical family changed during alias experiment")
	}
	var ownerRows int
	if e = owner.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM fixture WHERE v=?", payload).Scan(&ownerRows); e != nil || (ownerRows != 1 && ownerRows != 2) {
		t.Fatalf("canonical owner post-alias rows %d: %v", ownerRows, e)
	}
	t.Logf("canonical owner visible rows after alias=%d", ownerRows)
	stockEpochLockWitness(t, name, 0x40000002, 510, true)
	stockEpochLockWitness(t, name+"-shm", 128, 1, true)
	r := sidecarRoot(t, dir)
	sidecarNeighbor(t, r, dir, excluded)
	walName := alias + "-wal"
	raw, e := os.ReadFile(walName)
	walExists := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("alias WAL observation failed: %v", e)
	}
	shmExists := false
	if _, e = os.Stat(alias + "-shm"); e == nil {
		shmExists = true
	} else if !errors.Is(e, os.ErrNotExist) {
		t.Fatal(e)
	}
	t.Logf("alias family facts: WAL exists=%t bytes=%d SHM exists=%t", walExists, len(raw), shmExists)
	hasPayload := bytes.Contains(raw, []byte(payload))
	if !hasPayload {
		if result.Code == 0 && !walExists && !shmExists && ownerRows == 2 {
			t.Log("STOCK_ALIAS_NO_ALTERNATE_OBSERVED: successful alias commit visible through canonical owner, no alternate WAL/SHM observed. Factual post-close observation, NOT general security proof")
			return
		}
		if result.Code == 0 {
			t.Fatal("successful alias commit did not leave payload-bearing WAL; investigate, not a security PASS")
		}
		t.Logf("STOCK_ALIAS_SQLITE_REJECTED: phase=%s exactCode=%d; no payload found in alias WAL. Particular hostile alias operation refused, NOT general safety proof", result.Phase, result.Code)
		return
	}
	// Even a rejected SQL operation could have emitted sensitive WAL bytes. Never
	// suppress the data-path test solely because result.Code is nonzero.
	if len(raw) < 32 || len(raw) > Limit || !(bytes.Equal(raw[:4], []byte{0x37, 0x7f, 0x06, 0x82}) || bytes.Equal(raw[:4], []byte{0x37, 0x7f, 0x06, 0x83})) {
		t.Fatal("alias WAL header/size control failed")
	}
	_, wal := sidecarPin(t, r, "alias.db-wal")
	if _, known := excluded[wal.ID]; known {
		t.Fatal("alias WAL is not a fresh unregistered object")
	}
	if shmExists {
		_, shm := sidecarPin(t, r, "alias.db-shm")
		if _, known := excluded[shm.ID]; known {
			t.Fatal("alias SHM reused canonical identity")
		}
	}
	got, e := r.read(context.Background(), []string{"alias.db-wal"}, excluded)
	if e != nil || !bytes.Contains(got, []byte(payload)) {
		t.Fatalf("alias candidate content read inconclusive: %v", e)
	}
	excluded[wal.ID] = struct{}{}
	got, e = r.read(context.Background(), []string{"alias.db-wal"}, excluded)
	if !errors.Is(e, errProtected) || len(got) != 0 {
		t.Fatalf("registered alias identity control %v", e)
	}
	sidecarNeighbor(t, r, dir, excluded)
	t.Log("STOCK_ALIAS_GAP_CONFIRMED: live canonical stock owner does not register hostile alias WAL; original-three-ID model returns synthetic payload; added alias identity refuses. Hostile SQLite multipath use, NOT normal compatibility or private security GREEN")
}
