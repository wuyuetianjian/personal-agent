//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_access

package windowsprobe

import (
	"errors"
	"golang.org/x/sys/windows"
)

var errPersistentAccessUnimplemented = errors.New("qualified xAccess observation not implemented")

type persistentAccessProbe struct {
	root      *Root
	canonical string
	last      error
	open      func(windows.Handle, string) (windows.Handle, error)
	close     func(windows.Handle) error
}

func (a *persistentAccessProbe) Observe(name string, flags int32) (bool, error) {
	a.last = errPersistentAccessUnimplemented
	return false, a.last
}
