package api

import (
	"agent/internal/storage"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const runtimeMarker = "personal-agent runtime-rebuild-v1\n"

type InteractionRuntime struct {
	DB        *storage.DB
	lock      *os.File
	Directory string
}

// OpenInteractionRuntime never opens legacy active paths. The kernel lock survives
// stale lock files but not process exit; offline recovery is legal only under it.
func OpenInteractionRuntime(ctx context.Context, parent string) (*InteractionRuntime, error) {
	if parent == "" {
		return nil, fmt.Errorf("explicit runtime parent required")
	}
	abs, err := filepath.Abs(parent)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	abs = canonical
	dir := filepath.Join(abs, "runtime-rebuild-v1")
	if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("unsafe runtime namespace")
	}
	if err = secureRuntimeDirectory(dir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "process.lock")
	if st, e := os.Lstat(lockPath); e == nil && !st.Mode().IsRegular() {
		return nil, fmt.Errorf("unsafe lock file")
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = lockRuntime(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("runtime already in use")
	}
	r := &InteractionRuntime{lock: f, Directory: dir}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	marker := filepath.Join(dir, "namespace")
	raw, err := os.ReadFile(marker)
	if errors.Is(err, os.ErrNotExist) {
		entries, e := os.ReadDir(dir)
		if e != nil {
			return nil, e
		}
		for _, v := range entries {
			if v.Name() != "process.lock" {
				return nil, fmt.Errorf("unknown runtime namespace; preserve old data and use a fresh parent")
			}
		}
		if err = os.WriteFile(marker, []byte(runtimeMarker), 0600); err != nil {
			return nil, err
		}
	} else if err != nil || string(raw) != runtimeMarker {
		return nil, fmt.Errorf("unsupported runtime namespace")
	}
	for _, name := range []string{"namespace", "agent.db", "agent.db-wal", "agent.db-shm"} {
		if st, e := os.Lstat(filepath.Join(dir, name)); e == nil && !st.Mode().IsRegular() {
			return nil, fmt.Errorf("unsafe runtime file")
		}
	}
	dbPath := filepath.Join(dir, "agent.db")
	// Namespace identity is not schema compatibility. Check existing files under
	// the process lock, before OpenSQLite enables WAL or Close can checkpoint it.
	if _, err = os.Stat(dbPath); err == nil {
		if err = storage.ValidateSQLiteFile(ctx, dbPath); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	db, err := storage.OpenSQLite(ctx, dbPath)
	if err != nil {
		return nil, err
	}
	r.DB = db
	if err = os.Chmod(filepath.Join(dir, "agent.db"), 0600); err != nil {
		return nil, err
	}
	if err = storage.Migrate(ctx, db.SQL); err != nil {
		return nil, err
	}
	if _, err = db.SQL.ExecContext(ctx, storage.ProviderEvidenceDDL); err != nil {
		return nil, err
	}
	ok = true
	return r, nil
}
func (r *InteractionRuntime) Close() error {
	var err error
	if r.DB != nil {
		err = r.DB.Close()
		r.DB = nil
	}
	if r.lock != nil {
		err = errors.Join(err, unlockRuntime(r.lock), r.lock.Close())
		r.lock = nil
	}
	return err
}
