//go:build windows

package windowsprobe

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// OpenDirectory derives authority over an exact descendant directory, not an
// arbitrary pathname. The returned root independently owns the full ancestor
// I/O-handle lineage: closing its parent does not release its conventional
// rename guards. Total derived depth from NewRoot is limited to 32.
// The original trusted-root preconditions and all probe limitations still apply.
func (r *Root) OpenDirectory(components []string) (*Root, error) {
	if err := validateComponents(components); err != nil {
		return nil, err
	}
	return r.openDirectoryNamespace(components)
}

// openDirectoryNamespace is for validated initial host root selection as well
// as tool traversal. The public method additionally applies protected names.
func (r *Root) openDirectoryNamespace(components []string) (*Root, error) {
	if err := validateNames(components, false); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h == 0 {
		return nil, errors.New("windowsprobe: closed root")
	}
	if r.depth+len(components) > 32 {
		return nil, errUnsafe
	}
	root, err := inspect(r.h)
	if err != nil {
		return nil, err
	}
	if root.ID != r.id || root.Standard.Directory == 0 {
		return nil, errUnsafe
	}
	// Keep independently owned guards even when deriving from a derived root.
	// Duplicates retain the original sharing contract; they do not tighten it.
	handles := make([]windows.Handle, 0, len(r.ancestors)+1+len(components))
	transferred := false
	defer func() {
		if !transferred {
			for i := len(handles) - 1; i >= 0; i-- {
				windows.CloseHandle(handles[i])
			}
		}
	}()
	for _, source := range append(append([]windows.Handle(nil), r.ancestors...), r.h) {
		var h windows.Handle
		if err := windows.DuplicateHandle(windows.CurrentProcess(), source, windows.CurrentProcess(), &h, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
			return nil, err
		}
		handles = append(handles, h)
	}
	parent := handles[len(handles)-1]
	type observation struct {
		h    windows.Handle
		s    snapshot
		name string
	}
	observations := make([]observation, 0, len(components))
	for index, name := range components {
		metadata, err := openRelative(parent, name, false)
		if err != nil {
			return nil, fmt.Errorf("directory component %d metadata open: %w", index, err)
		}
		s, err := inspect(metadata)
		if err == nil && (s.Standard.Directory == 0 || s.ID.Volume != r.id.Volume) {
			err = errUnsafe
		}
		if err == nil {
			err = exactName(metadata, name)
		}
		var io windows.Handle
		var qualified snapshot
		if err == nil {
			io, qualified, err = acquireIOGuard(parent, name, s)
		}
		closeErr := windows.CloseHandle(metadata)
		if io != 0 {
			handles = append(handles, io)
		}
		if err = errors.Join(err, closeErr); err != nil {
			return nil, fmt.Errorf("directory component %d qualify/io guard: %w", index, err)
		}
		observations = append(observations, observation{io, qualified, name})
		parent = io
	}
	for index, g := range observations {
		now, err := inspect(g.h)
		if err != nil {
			return nil, err
		}
		if !unchanged(g.s, now) {
			return nil, fmt.Errorf("directory component %d changed (%s): %w", index, snapshotChanges(g.s, now), errUnsafe)
		}
		if err := exactName(g.h, g.name); err != nil {
			return nil, err
		}
	}
	// Recheck the retained source root without resolving its old pathname.
	now, err := inspect(r.h)
	if err != nil {
		return nil, err
	}
	if now.ID != r.id || now.Standard.Directory == 0 {
		return nil, errUnsafe
	}
	last := observations[len(observations)-1]
	child := &Root{h: parent, id: last.s.ID, ancestors: handles[:len(handles)-1], depth: r.depth + len(components)}
	transferred = true
	return child, nil
}
