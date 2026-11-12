//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit

package windowsprobe

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
)

var errPersistentPermitUnimplemented = errors.New("persistent operation permit owner not implemented")
var errPersistentPermitClosed = errors.New("persistent operation permit closed")
var errPersistentPermitForeign = errors.New("persistent operation permit foreign authority")

type persistentPermitHooks struct {
	openData            func(windows.Handle, string) (windows.Handle, error)
	close               func(windows.Handle) error
	afterClosing        func()
	beforeResourceClose func()
	afterReleaseStart   func()
}
type persistentPermitOwner struct{}
type persistentPermitConnection struct{}
type persistentOperationPermit struct{}
type persistentPermitResource struct{}

func newPersistentPermitOwner(*Root, string, persistentPermitHooks) (*persistentPermitOwner, error) {
	return nil, errPersistentPermitUnimplemented
}
func (*persistentPermitOwner) newConnection(context.Context) (*persistentPermitConnection, error) {
	return nil, errPersistentPermitUnimplemented
}
func (*persistentPermitConnection) begin(context.Context) (*persistentOperationPermit, error) {
	return nil, errPersistentPermitUnimplemented
}
func (*persistentOperationPermit) acquire(context.Context, persistentLeasePurpose) (*persistentPermitResource, error) {
	return nil, errPersistentPermitUnimplemented
}
func (*persistentOperationPermit) borrow(context.Context, *persistentPermitResource, func(windows.Handle) error) error {
	return errPersistentPermitUnimplemented
}
func (*persistentOperationPermit) release() error { return errPersistentPermitUnimplemented }
func (*persistentPermitResource) close() error    { return errPersistentPermitUnimplemented }
func (*persistentPermitConnection) close() error  { return errPersistentPermitUnimplemented }
func (*persistentPermitOwner) close() error       { return errPersistentPermitUnimplemented }
