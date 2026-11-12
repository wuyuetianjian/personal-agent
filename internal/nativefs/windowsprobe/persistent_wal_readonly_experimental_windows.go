//go:build windows && nativeprobe_sqlite_identity && nativeprobe_sqlite_lease && nativeprobe_sqlite_permit && nativeprobe_sqlite_existing_wal

package windowsprobe

import (
	"context"
	"errors"
)

const leaseWALReadOnly persistentLeasePurpose = 4

var errExistingWALReadOnlyUnimplemented = errors.New("fresh readonly WAL resource not implemented")

func (p *persistentOperationPermit) acquireReadOnlyWAL(context.Context) (*persistentPermitResource, error) {
	return nil, errExistingWALReadOnlyUnimplemented
}
