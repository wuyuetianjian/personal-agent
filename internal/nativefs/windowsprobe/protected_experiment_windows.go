//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

// ErrProtected identifies an actual protected-object refusal, not a sharing error.
var ErrProtected = errProtected

// ExperimentalReadExcluding is available only in the explicit main-identity
// experiment. It is not complete private-storage protection or production API.
// The caller must supply 1..16 live, trusted file handles and retain their
// ownership throughout this call. The experiment's SQLite caller also keeps the
// same SQL connection borrowed until this read and duplicate cleanup complete.
// No saved pathname or ID can substitute for those currently owned objects.
func (r *Root) ExperimentalReadExcluding(ctx context.Context, components []string, protected []windows.Handle) (data []byte, err error) {
	if len(protected) == 0 || len(protected) > 16 {
		return nil, errUnsafe
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	owned := make([]windows.Handle, 0, len(protected))
	defer func() {
		for i := len(owned) - 1; i >= 0; i-- {
			if closeErr := windows.CloseHandle(owned[i]); closeErr != nil {
				err = errors.Join(err, closeErr)
				data = nil
			}
		}
	}()
	excluded := make(map[idInfo]struct{}, len(protected))
	for _, source := range protected {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if source == 0 || source == windows.InvalidHandle {
			return nil, errUnsafe
		}
		var duplicate windows.Handle
		if err = windows.DuplicateHandle(windows.CurrentProcess(), source, windows.CurrentProcess(), &duplicate, windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE, false, 0); err != nil {
			return nil, err
		}
		owned = append(owned, duplicate)
		var metadata snapshot
		metadata, err = inspect(duplicate)
		if err != nil {
			return nil, err
		}
		if metadata.Standard.Directory != 0 {
			return nil, errUnsafe
		}
		excluded[metadata.ID] = struct{}{}
	}
	return r.read(ctx, components, excluded)
}
