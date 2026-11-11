//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"errors"
	"io"
	"math"
	"sync"
	"sync/atomic"
	"unsafe"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// NewExperimentalTempVFSWithScratch retains an independently qualified root.
// Only anonymous TEMP_DB is supported, never persisted main/WAL/SHM storage.
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
		return nil, errors.Join(errors.New("allocate TEMP IO methods"), o.Close(), s.Close())
	}
	*(*sqlite3.Tsqlite3_io_methods)(unsafe.Pointer(methods)) = sqlite3.Tsqlite3_io_methods{
		FiVersion: 1,
		FxClose:   tempSlot(tempIOClose), FxRead: tempSlot(tempIORead), FxWrite: tempSlot(tempIOWrite),
		FxTruncate: tempSlot(tempIOTruncate), FxSync: tempSlot(tempIOSync), FxFileSize: tempSlot(tempIOSize),
		FxLock: tempSlot(tempIOLock), FxUnlock: tempSlot(tempIOUnlock), FxCheckReservedLock: tempSlot(tempIOReserved),
		FxFileControl: tempSlot(tempIOControl), FxSectorSize: tempSlot(tempIOSector), FxDeviceCharacteristics: tempSlot(tempIOCharacteristics),
	}
	o.scratch = s
	o.ioMethods = methods
	return o, nil
}

// Registry contains Go-owned records, never Go pointers in foreign POD.
// Registry lookup releases its lock before owner.mu; owner->registry is the
// only nested lock order. No registry/owner lock spans scratch or kernel IO.
type tempIORecord struct {
	owner          *ExperimentalTempVFS
	file           *scratchFile
	address, token uintptr
	mu             sync.Mutex // serializes callbacks before the backend's own lock
	refs           int        // owner.mu
	closing        bool       // owner.mu
	drained        chan struct{}
	level          int32 // record.mu
}

var tempIORecords = struct {
	sync.Mutex
	next    uintptr
	entries map[uintptr]*tempIORecord
}{entries: make(map[uintptr]*tempIORecord)}

func tempPinRecord(r *tempIORecord) *tempIORecord {
	if r == nil {
		return nil
	}
	o := r.owner
	o.mu.Lock()
	if r.closing {
		o.mu.Unlock()
		return nil
	}
	r.refs++
	o.stats.InflightCallbacks++
	o.mu.Unlock()
	r.mu.Lock()
	return r
}
func tempPin(p uintptr) *tempIORecord {
	if p == 0 {
		return nil
	}
	token := atomic.LoadUintptr(&(*tempVFSFile)(unsafe.Pointer(p)).Token)
	tempIORecords.Lock()
	r := tempIORecords.entries[token]
	tempIORecords.Unlock()
	if r == nil || r.address != p {
		return nil
	}
	return tempPinRecord(r)
}
func tempUnpin(r *tempIORecord) {
	r.mu.Unlock()
	o := r.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	r.refs--
	o.stats.InflightCallbacks--
	if r.closing && r.refs == 0 {
		close(r.drained)
	}
}
func tempOpenScratch(o *ExperimentalTempVFS, file, out uintptr) int32 {
	// Caller already acquired an open callback reference, keeping allocations
	// alive even if owner.Close revokes publication while Create is in flight.
	o.mu.Lock()
	store := o.scratch
	o.mu.Unlock()
	f, err := store.Create()
	if err != nil {
		if errors.Is(err, errScratchLimit) {
			return sqlite3.SQLITE_FULL
		}
		return sqlite3.SQLITE_CANTOPEN
	}
	o.mu.Lock()
	if o.closing {
		o.mu.Unlock()
		e := f.Close()
		o.mu.Lock()
		o.ioCloseErr = errors.Join(o.ioCloseErr, e)
		o.mu.Unlock()
		return sqlite3.SQLITE_CANTOPEN
	}
	tempIORecords.Lock()
	if tempIORecords.next == ^uintptr(0) {
		tempIORecords.Unlock()
		o.mu.Unlock()
		e := f.Close()
		o.mu.Lock()
		o.ioCloseErr = errors.Join(o.ioCloseErr, e)
		o.mu.Unlock()
		return sqlite3.SQLITE_FULL
	}
	tempIORecords.next++
	token := tempIORecords.next
	r := &tempIORecord{owner: o, file: f, address: file, token: token}
	tempIORecords.entries[token] = r
	tempIORecords.Unlock()
	pod := (*tempVFSFile)(unsafe.Pointer(file))
	atomic.StoreUintptr(&pod.Token, token)
	if out != 0 {
		*(*int32)(unsafe.Pointer(out)) = tempOpenFlags
	}
	atomic.StoreUintptr(&pod.Base.FpMethods, o.ioMethods) // publish methods last
	o.stats.LastFileID = f.id.ID
	o.stats.LastVolume = f.id.Volume
	o.stats.Opened++
	o.stats.LiveFiles++
	o.mu.Unlock()
	return sqlite3.SQLITE_OK
}
func tempIOClose(_ *libc.TLS, p uintptr) int32 {
	if p == 0 {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	pod := (*tempVFSFile)(unsafe.Pointer(p))
	token := atomic.LoadUintptr(&pod.Token)
	tempIORecords.Lock()
	r := tempIORecords.entries[token]
	tempIORecords.Unlock()
	if r == nil || r.address != p {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	o := r.owner
	o.mu.Lock()
	if r.closing {
		o.mu.Unlock()
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	r.closing = true
	r.drained = make(chan struct{})
	o.stats.InflightCallbacks++
	tempIORecords.Lock()
	delete(tempIORecords.entries, token)
	tempIORecords.Unlock()
	atomic.StoreUintptr(&pod.Token, 0)
	atomic.StoreUintptr(&pod.Base.FpMethods, 0)
	if r.refs == 0 {
		close(r.drained)
	}
	drained := r.drained
	o.mu.Unlock()
	<-drained // no registry/owner lock, waits only previously pinned callbacks
	err := r.file.Close()
	o.mu.Lock()
	o.stats.LiveFiles--
	o.stats.InflightCallbacks--
	o.ioCloseErr = errors.Join(o.ioCloseErr, err)
	if err == nil {
		o.stats.ClosedSuccessfully++
	} else {
		o.closing = true
	}
	o.mu.Unlock()
	if err != nil {
		return sqlite3.SQLITE_IOERR_CLOSE
	}
	return sqlite3.SQLITE_OK
}
func tempValidAmount(n int32) bool            { return n >= 0 && n <= 1<<20 }
func tempValidOffset(n int32, off int64) bool { return off >= 0 && off <= math.MaxInt64-int64(n) }
func tempIORead(_ *libc.TLS, p, out uintptr, n int32, off int64) int32 {
	if !tempValidAmount(n) || (out == 0 && n != 0) {
		return sqlite3.SQLITE_IOERR_READ
	}
	var b []byte
	if n != 0 {
		b = unsafe.Slice((*byte)(unsafe.Pointer(out)), int(n))
		clear(b)
	}
	if !tempValidOffset(n, off) {
		return sqlite3.SQLITE_IOERR_READ
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_READ
	}
	defer tempUnpin(r)
	o := r.owner
	o.mu.Lock()
	o.stats.BackendReadCalls++
	o.mu.Unlock()
	got, err := r.file.ReadAt(b, off)
	o.mu.Lock()
	if got > 0 {
		o.stats.ReadBytes += uint64(got)
	}
	o.mu.Unlock()
	if got < 0 || got > len(b) {
		clear(b)
		return sqlite3.SQLITE_IOERR_READ
	}
	if err == nil && got == len(b) {
		return sqlite3.SQLITE_OK
	}
	if errors.Is(err, io.EOF) && got < len(b) {
		clear(b[got:])
		return sqlite3.SQLITE_IOERR_SHORT_READ
	}
	clear(b)
	return sqlite3.SQLITE_IOERR_READ
}
func tempIOWrite(_ *libc.TLS, p, in uintptr, n int32, off int64) int32 {
	if !tempValidAmount(n) || !tempValidOffset(n, off) || (in == 0 && n != 0) {
		return sqlite3.SQLITE_IOERR_WRITE
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_WRITE
	}
	defer tempUnpin(r)
	var b []byte
	if n != 0 {
		b = unsafe.Slice((*byte)(unsafe.Pointer(in)), int(n))
	}
	o := r.owner
	o.mu.Lock()
	o.stats.BackendWriteCalls++
	o.mu.Unlock()
	got, err := r.file.WriteAt(b, off)
	o.mu.Lock()
	if got > 0 {
		o.stats.WrittenBytes += uint64(got)
	}
	o.mu.Unlock()
	if err == nil && got == len(b) {
		size, e := r.file.Size()
		if e != nil {
			return sqlite3.SQLITE_IOERR_WRITE
		}
		o.mu.Lock()
		if uint64(size) > o.stats.PeakKernelSize {
			o.stats.PeakKernelSize = uint64(size)
		}
		o.mu.Unlock()
		return sqlite3.SQLITE_OK
	}
	if errors.Is(err, errScratchLimit) && !errors.Is(err, errScratchUncertain) {
		return sqlite3.SQLITE_FULL
	}
	return sqlite3.SQLITE_IOERR_WRITE
}
func tempIOTruncate(_ *libc.TLS, p uintptr, size int64) int32 {
	if size < 0 {
		return sqlite3.SQLITE_IOERR_TRUNCATE
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_TRUNCATE
	}
	defer tempUnpin(r)
	err := r.file.Truncate(size)
	if err == nil {
		return sqlite3.SQLITE_OK
	}
	if errors.Is(err, errScratchLimit) && !errors.Is(err, errScratchUncertain) {
		return sqlite3.SQLITE_FULL
	}
	return sqlite3.SQLITE_IOERR_TRUNCATE
}
func tempIOSync(_ *libc.TLS, p uintptr, flags int32) int32 {
	base := flags & ^int32(sqlite3.SQLITE_SYNC_DATAONLY)
	if base != sqlite3.SQLITE_SYNC_NORMAL && base != sqlite3.SQLITE_SYNC_FULL {
		return sqlite3.SQLITE_IOERR_FSYNC
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_FSYNC
	}
	defer tempUnpin(r)
	if err := r.file.Sync(); err != nil {
		return sqlite3.SQLITE_IOERR_FSYNC
	}
	return sqlite3.SQLITE_OK
}
func tempIOSize(_ *libc.TLS, p, out uintptr) int32 {
	if out == 0 {
		return sqlite3.SQLITE_IOERR_FSTAT
	}
	*(*int64)(unsafe.Pointer(out)) = 0
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_FSTAT
	}
	defer tempUnpin(r)
	size, err := r.file.Size()
	if err != nil {
		return sqlite3.SQLITE_IOERR_FSTAT
	}
	*(*int64)(unsafe.Pointer(out)) = size
	return sqlite3.SQLITE_OK
}
func tempIOLock(_ *libc.TLS, p uintptr, level int32) int32 {
	if level < sqlite3.SQLITE_LOCK_SHARED || level > sqlite3.SQLITE_LOCK_EXCLUSIVE {
		return sqlite3.SQLITE_IOERR_LOCK
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_LOCK
	}
	defer tempUnpin(r)
	// Exclusive anonymous object: upgrades may skip intermediate levels.
	if level > r.level {
		r.level = level
	}
	return sqlite3.SQLITE_OK
}
func tempIOUnlock(_ *libc.TLS, p uintptr, level int32) int32 {
	if level != sqlite3.SQLITE_LOCK_NONE && level != sqlite3.SQLITE_LOCK_SHARED {
		return sqlite3.SQLITE_IOERR_UNLOCK
	}
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_UNLOCK
	}
	defer tempUnpin(r)
	if level > r.level {
		return sqlite3.SQLITE_IOERR_UNLOCK
	}
	r.level = level
	return sqlite3.SQLITE_OK
}
func tempIOReserved(_ *libc.TLS, p, out uintptr) int32 {
	if out == 0 {
		return sqlite3.SQLITE_IOERR_CHECKRESERVEDLOCK
	}
	*(*int32)(unsafe.Pointer(out)) = 0
	r := tempPin(p)
	if r == nil {
		return sqlite3.SQLITE_IOERR_CHECKRESERVEDLOCK
	}
	defer tempUnpin(r)
	if r.level >= sqlite3.SQLITE_LOCK_RESERVED {
		*(*int32)(unsafe.Pointer(out)) = 1
	}
	return sqlite3.SQLITE_OK
}
func tempIOControl(*libc.TLS, uintptr, int32, uintptr) int32 { return sqlite3.SQLITE_NOTFOUND }
func tempIOSector(*libc.TLS, uintptr) int32                  { return 4096 }
func tempIOCharacteristics(*libc.TLS, uintptr) int32         { return 0 }

// Copied, current metadata only. No raw handles, token IDs, filenames, or hooks.
type ExperimentalTempFileEvidence struct {
	Volume uint64
	ID     [16]byte
	Size   int64
}

func (o *ExperimentalTempVFS) Evidence() ([]ExperimentalTempFileEvidence, error) {
	tempIORecords.Lock()
	records := make([]*tempIORecord, 0)
	for _, r := range tempIORecords.entries {
		if r.owner == o {
			records = append(records, r)
		}
	}
	tempIORecords.Unlock()
	if len(records) == 0 {
		return nil, errors.New("no live TEMP objects to observe")
	}
	result := make([]ExperimentalTempFileEvidence, 0, len(records))
	for _, record := range records {
		r := tempPinRecord(record)
		if r == nil {
			return nil, errors.New("TEMP object closed during observation")
		}
		evidence, err := func() (ExperimentalTempFileEvidence, error) {
			defer tempUnpin(r)
			f := r.file
			f.mu.Lock()
			defer f.mu.Unlock()
			f.store.mu.Lock()
			defer f.store.mu.Unlock()
			if err := f.ready(); err != nil {
				return ExperimentalTempFileEvidence{}, err
			}
			id, size, err := scratchOwnedInspect(f.h)
			if err != nil {
				return ExperimentalTempFileEvidence{}, err
			}
			if id != f.id || size != f.size {
				return ExperimentalTempFileEvidence{}, errUnsafe
			}
			return ExperimentalTempFileEvidence{Volume: id.Volume, ID: id.ID, Size: size}, nil
		}()
		if err != nil {
			return nil, err
		}
		result = append(result, evidence)
	}
	return result, nil
}
