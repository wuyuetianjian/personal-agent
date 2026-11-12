//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_access

package windowsprobe

import (
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"sync"
)

var errPersistentAccessUnimplemented = errors.New("qualified xAccess observation not implemented")

type persistentAccessProbe struct {
	mu             sync.Mutex
	root           *Root
	canonical      string
	lineage        []persistentObject // borrowed root-owned handles, usable only under root.mu
	last, terminal error
	open           func(windows.Handle, string) (windows.Handle, error)
	close          func(windows.Handle) error
	afterLineage   func() error // fault injection AFTER mandatory native qualification
}

func newPersistentAccessProbe(r *Root, canonical string) (*persistentAccessProbe, error) {
	if r == nil || canonical == "" || !filepath.IsAbs(canonical) {
		return nil, errUnsafe
	}
	if e := validateComponents([]string{filepath.Base(canonical)}); e != nil {
		return nil, e
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h == 0 {
		return nil, errPersistentClosed
	}
	var buf [32768]uint16
	n, e := windows.GetFinalPathNameByHandle(r.h, &buf[0], uint32(len(buf)), 0)
	if e != nil {
		return nil, e
	}
	if n == 0 || n >= uint32(len(buf)) {
		return nil, errUnsafe
	}
	dir := strings.TrimPrefix(windows.UTF16ToString(buf[:n]), `\\?\`)
	if canonical != filepath.Join(dir, filepath.Base(canonical)) {
		return nil, errUnsafe
	}
	a := &persistentAccessProbe{root: r, canonical: canonical}
	for _, h := range append(append([]windows.Handle{}, r.ancestors...), r.h) {
		s, e := inspect(h)
		if e != nil {
			return nil, e
		}
		if s.Standard.Directory == 0 || s.ID.Volume != r.id.Volume {
			return nil, errUnsafe
		}
		a.lineage = append(a.lineage, persistentObject{h, s.ID})
	}
	if a.lineage[len(a.lineage)-1].id != r.id {
		return nil, errUnsafe
	}
	return a, nil
}

// Lock order is observer.mu -> root.mu. Root never calls the observer. No Root
// method which reacquires root.mu may be invoked while an observation is active.
func (a *persistentAccessProbe) checkLineage() error {
	r := a.root
	if r.h == 0 || len(a.lineage) != len(r.ancestors)+1 {
		return errPersistentClosed
	}
	for i, o := range a.lineage {
		h := r.h
		if i < len(r.ancestors) {
			h = r.ancestors[i]
		}
		if h != o.h {
			return errUnsafe
		}
		s, e := inspect(h)
		if e != nil {
			return e
		}
		if s.ID != o.id || s.Standard.Directory == 0 || s.ID.Volume != r.id.Volume {
			return errUnsafe
		}
	}
	if a.afterLineage != nil {
		return a.afterLineage()
	}
	return nil
}
func (a *persistentAccessProbe) Observe(name string, flags int32) (yes bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() { a.last = err }()
	if a.terminal != nil {
		return false, a.terminal
	}
	// SQLite ACCESS_EXISTS=0, ACCESS_READWRITE=1, ACCESS_READ=2.
	if flags < 0 || flags > 2 {
		return false, errUnsafe
	}
	allowed := false
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if name == a.canonical+suffix {
			allowed = true
			break
		}
	}
	if !allowed {
		return false, errUnsafe
	}
	base := filepath.Base(name)
	if e := validateComponents([]string{base}); e != nil {
		return false, e
	}
	a.root.mu.Lock()
	defer a.root.mu.Unlock()
	if e := a.checkLineage(); e != nil {
		a.terminal = e
		return false, e
	}
	var h windows.Handle
	defer func() {
		if h != 0 && h != windows.InvalidHandle {
			owned := h
			h = 0
			e := a.consumeMetadata(owned)
			if e != nil {
				a.terminal = errors.Join(a.terminal, e)
				err = errors.Join(err, e)
				yes = false
			}
		}
	}()
	if a.open != nil {
		h, err = a.open(a.root.h, base)
	} else {
		h, err = persistentOpen(a.root.h, base, false)
	}
	if err != nil {
		// Only the exact leaf not-found outcome without an owned handle is absence.
		// A joined/ambiguous error or an ancestor failure never qualifies as absence.
		missing := err == windows.STATUS_OBJECT_NAME_NOT_FOUND && (h == 0 || h == windows.InvalidHandle)
		if e := a.checkLineage(); e != nil {
			a.terminal = e
			return false, errors.Join(err, e)
		}
		if missing {
			return false, nil
		}
		return false, err
	}
	s, e := inspect(h)
	if e != nil {
		return false, e
	}
	if s.Standard.Directory != 0 || s.ID.Volume != a.root.id.Volume {
		return false, errUnsafe
	}
	if e = exactName(h, base); e != nil {
		return false, e
	}
	if e = a.checkLineage(); e != nil {
		a.terminal = e
		return false, e
	}
	if flags == 0 {
		return s.Standard.Size > 0, nil
	}
	if flags == 1 {
		return s.Basic.Attributes&windows.FILE_ATTRIBUTE_READONLY == 0, nil
	}
	return true, nil
}

// A trusted cleanup callback may report an unknown close outcome by panic.
// Convert it to terminal uncertainty after consuming the numeric handle once.
func (a *persistentAccessProbe) consumeMetadata(h windows.Handle) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("metadata close panic: %v", p)
		}
	}()
	if a.close != nil {
		return a.close(h)
	}
	return windows.CloseHandle(h)
}
