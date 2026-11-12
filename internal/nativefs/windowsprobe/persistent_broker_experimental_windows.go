//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
)

type persistentRole uint8

const (
	persistentMain persistentRole = iota
	persistentWAL
	persistentSHM
)

var errPersistentPending = errors.New("persistent broker not ready")
var errPersistentClosed = errors.New("persistent broker closed")
var errPersistentUnimplemented = errors.New("persistent broker acquisition not implemented")

type persistentHooks struct {
	afterMetadata   func(persistentRole) error
	afterRegistered func(persistentRole) error
	close           func(windows.Handle) error
}
type persistentEvidence struct {
	Published int
	Closed    bool
	Terminal  error
}
type persistentBroker struct{}

func newPersistentBroker(*Root, string, persistentHooks) (*persistentBroker, error) {
	return nil, errPersistentUnimplemented
}
func (*persistentBroker) acquireExisting(context.Context, persistentRole) error {
	return errPersistentUnimplemented
}
func (*persistentBroker) readWorkspace(context.Context, *Root, []string) ([]byte, error) {
	return nil, errPersistentUnimplemented
}
func (*persistentBroker) evidence() persistentEvidence { return persistentEvidence{} }
func (*persistentBroker) close() error                 { return errPersistentUnimplemented }
