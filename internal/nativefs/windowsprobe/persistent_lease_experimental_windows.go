//go:build windows && nativeprobe_sqlite_lease && nativeprobe_sqlite_identity

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
)

type persistentLeasePurpose uint8

const (
	leaseMain persistentLeasePurpose = iota
	leaseWAL
	leaseSHMMapping
	leaseSHMLock
)

var errPersistentLeaseUnimplemented = errors.New("persistent independent connection lease not implemented")
var errPersistentLeaseClosed = errors.New("persistent connection lease closed")
var errPersistentLeaseForeign = errors.New("persistent connection lease foreign authority")

type persistentLeaseHooks struct {
	beforeResourceClose func()
	afterClosing        func()
	openData            func(windows.Handle, string) (windows.Handle, error)
	afterRegister       func() error
	close               func(windows.Handle) error
	afterRead           func() error
}
type persistentLeaseEvidence struct {
	Connections, Resources int
	Terminal               error
	Closed                 bool
}
type persistentLeaseOwner struct{}
type persistentLeaseConnection struct{}
type persistentFileLease struct{}

func newPersistentLeaseOwner(*Root, string, persistentLeaseHooks) (*persistentLeaseOwner, error) {
	return nil, errPersistentLeaseUnimplemented
}
func (*persistentLeaseOwner) newConnection(context.Context) (*persistentLeaseConnection, error) {
	return nil, errPersistentLeaseUnimplemented
}
func (*persistentLeaseConnection) acquire(context.Context, persistentLeasePurpose) (*persistentFileLease, error) {
	return nil, errPersistentLeaseUnimplemented
}
func (*persistentLeaseConnection) borrow(context.Context, *persistentFileLease, func(windows.Handle) error) error {
	return errPersistentLeaseUnimplemented
}
func (*persistentFileLease) close() error       { return errPersistentLeaseUnimplemented }
func (*persistentLeaseConnection) close() error { return errPersistentLeaseUnimplemented }
func (*persistentLeaseOwner) close() error      { return errPersistentLeaseUnimplemented }
func (*persistentLeaseOwner) readWorkspace(context.Context, *Root, []string) ([]byte, error) {
	return nil, errPersistentLeaseUnimplemented
}
func (*persistentLeaseOwner) evidence() persistentLeaseEvidence { return persistentLeaseEvidence{} }
