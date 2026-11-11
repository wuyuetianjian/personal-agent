//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
)

// ErrProtected identifies an actual protected-object refusal, not a sharing error.
var ErrProtected = errors.New("windowsprobe: protected object")

// ExperimentalReadExcluding is available only in the explicit main-identity
// experiment. It is not complete private-storage protection or production API.
func (r *Root) ExperimentalReadExcluding(ctx context.Context, components []string, protected []windows.Handle) ([]byte, error) {
	return nil, errors.New("windowsprobe: protected-object read not implemented")
}
