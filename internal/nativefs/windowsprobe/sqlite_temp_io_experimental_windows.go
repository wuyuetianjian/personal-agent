//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"errors"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// NewExperimentalTempVFSWithScratch retains an independently qualified root.
// This stage still refuses EVERY xOpen; it does not grant any writable VFS.
func NewExperimentalTempVFSWithScratch(root *Root) (*ExperimentalTempVFS, error) {
	return newExperimentalTempVFSWithScratch(root, scratchHooks{})
}
func newExperimentalTempVFSWithScratch(root *Root, hooks scratchHooks) (*ExperimentalTempVFS, error) {
	s, err := newScratchStore(root, scratchLimits{Files: 4, FileBytes: 16 << 20, TotalBytes: 32 << 20, IOBytes: 1 << 20}, hooks)
	if err != nil {
		return nil, err
	}
	o, err := NewExperimentalTempVFS()
	if err != nil {
		return nil, errors.Join(err, s.Close())
	}
	tls := libc.NewTLS()
	defer tls.Close()
	methods := libc.Xcalloc(tls, 1, uint64(unsafe.Sizeof(sqlite3.Tsqlite3_io_methods{})))
	if methods == 0 {
		return nil, errors.Join(errors.New("allocate refusing IO methods"), o.Close(), s.Close())
	}
	*(*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(methods)) = sqlite3.Tsqlite3_io_methods{
		FiVersion: 1,
		FxClose:   tempSlot(tempIORefuseClose), FxRead: tempSlot(tempIORefuseRead), FxWrite: tempSlot(tempIORefuseWrite),
		FxTruncate: tempSlot(tempIORefuseTruncate), FxSync: tempSlot(tempIORefuseSync), FxFileSize: tempSlot(tempIORefuseSize),
		FxLock: tempSlot(tempIORefuseLock), FxUnlock: tempSlot(tempIORefuseUnlock), FxCheckReservedLock: tempSlot(tempIORefuseReserved),
		FxFileControl: tempSlot(tempIORefuseControl), FxSectorSize: tempSlot(tempIORefuseSector), FxDeviceCharacteristics: tempSlot(tempIORefuseCharacteristics),
	}
	o.scratch = s
	o.ioMethods = methods
	return o, nil
}

// These static top-level stubs have no backend or token registry. The foreign
// table is owned by the registration but is never published to a sqlite3_file.
func tempIORefuseClose(*libc.TLS, uintptr) int32 { return sqlite3.SQLITE_IOERR_CLOSE }
func tempIORefuseRead(_ *libc.TLS, _ uintptr, out uintptr, n int32, _ int64) int32 {
	if n >= 0 && n <= 1<<20 && out != 0 {
		clear(unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n)))
	}
	return sqlite3.SQLITE_IOERR_READ
}
func tempIORefuseWrite(*libc.TLS, uintptr, uintptr, int32, int64) int32 {
	return sqlite3.SQLITE_IOERR_WRITE
}
func tempIORefuseTruncate(*libc.TLS, uintptr, int64) int32 { return sqlite3.SQLITE_IOERR_TRUNCATE }
func tempIORefuseSync(*libc.TLS, uintptr, int32) int32     { return sqlite3.SQLITE_IOERR_FSYNC }
func tempIORefuseSize(_ *libc.TLS, _ uintptr, out uintptr) int32 {
	if out != 0 {
		*(*int64)(unsafe.Pointer(out)) = 0
	}
	return sqlite3.SQLITE_IOERR_FSTAT
}
func tempIORefuseLock(*libc.TLS, uintptr, int32) int32   { return sqlite3.SQLITE_IOERR_LOCK }
func tempIORefuseUnlock(*libc.TLS, uintptr, int32) int32 { return sqlite3.SQLITE_IOERR_UNLOCK }
func tempIORefuseReserved(_ *libc.TLS, _ uintptr, out uintptr) int32 {
	if out != 0 {
		*(*int32)(unsafe.Pointer(out)) = 0
	}
	return sqlite3.SQLITE_IOERR_CHECKRESERVEDLOCK
}
func tempIORefuseControl(*libc.TLS, uintptr, int32, uintptr) int32 { return sqlite3.SQLITE_NOTFOUND }
func tempIORefuseSector(*libc.TLS, uintptr) int32                  { return 4096 }
func tempIORefuseCharacteristics(*libc.TLS, uintptr) int32         { return 0 }
