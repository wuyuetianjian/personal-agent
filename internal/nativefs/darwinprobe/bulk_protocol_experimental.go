//go:build nativeprobe_darwin_bulk

package darwinprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	bulkCommonMask uint32 = 0xa2040009 // RETURNED_ATTRS|ERROR|NAME|OBJTYPE|FLAGS|FILEID
	bulkBufferSize        = 64 << 10
	bulkMaxCalls          = 16
	bulkMaxRaw            = 1024
	bulkMaxEntries        = 256
	bulkFixedSize         = 52
)

// BulkEntry is transient metadata, never authorization for a content open.
type BulkEntry struct {
	Name      string
	Inode     uint64
	Directory bool
}
type BulkObservation struct {
	Entries           []BulkEntry
	Calls, RawRecords int
}
type bulkOps struct {
	query func([]byte) (int, error)
	check func() error
	close func() error
}

var errBulkRefused = errors.New("experimental bulk metadata refused")

// The native bridge requests FSOPT_PACK_INVAL_ATTRS. This fixes field slots,
// but the returned mask still decides whether a slot is meaningful. Decode
// bytes, not Go structs: packed 64-bit fields can have 4-byte alignment.
func parseBulkRecords(b []byte, count int) ([]BulkEntry, error) {
	if count < 1 || count > bulkMaxRaw || len(b) > bulkBufferSize {
		return nil, errBulkRefused
	}
	entries := make([]BulkEntry, 0, count)
	for i := 0; i < count; i++ {
		if len(b) < bulkFixedSize {
			return nil, errBulkRefused
		}
		size := uint64(binary.LittleEndian.Uint32(b))
		if size < bulkFixedSize || size%8 != 0 || size > uint64(len(b)) {
			return nil, errBulkRefused
		}
		r := b[:int(size)]
		if binary.LittleEndian.Uint32(r[4:]) != bulkCommonMask {
			return nil, errBulkRefused
		}
		for off := 8; off < 24; off += 4 {
			if binary.LittleEndian.Uint32(r[off:]) != 0 {
				return nil, errBulkRefused
			}
		}
		if code := binary.LittleEndian.Uint32(r[24:]); code != 0 {
			return nil, fmt.Errorf("%w: entry error %d", errBulkRefused, code)
		}
		relative := int64(int32(binary.LittleEndian.Uint32(r[28:])))
		length := uint64(binary.LittleEndian.Uint32(r[32:]))
		start := int64(28) + relative
		if start < bulkFixedSize || length < 2 || length > 256 || uint64(start) > size || length > size-uint64(start) {
			return nil, errBulkRefused
		}
		raw := r[int(start):int(uint64(start)+length)]
		if raw[len(raw)-1] != 0 {
			return nil, errBulkRefused
		}
		name := string(raw[:len(raw)-1])
		if validateComponent(name) != nil {
			return nil, errBulkRefused
		}
		kind := binary.LittleEndian.Uint32(r[36:])
		flags := binary.LittleEndian.Uint32(r[40:])
		id := binary.LittleEndian.Uint64(r[44:])
		if (kind != 1 && kind != 2) || flags&0x800000 != 0 || id == 0 {
			return nil, errBulkRefused
		}
		entries = append(entries, BulkEntry{Name: name, Inode: id, Directory: kind == 2})
		b = b[int(size):]
	}
	return entries, nil
}

// Owns one fresh scan descriptor via ops.close, even on early cancellation.
// A failed/uncertain close is reported once and never retried. Nothing partial
// escapes on any error, including cancellation observed during final cleanup.
func collectBulk(ctx context.Context, ops bulkOps) (out BulkObservation, err error) {
	defer func() {
		err = errors.Join(err, ops.close())
		if ctx != nil {
			err = errors.Join(err, ctx.Err())
		}
		if err != nil {
			out = BulkObservation{}
		}
	}()
	if ctx == nil {
		return BulkObservation{}, errBulkRefused
	}
	buffer := make([]byte, bulkBufferSize)
	seen := map[string]bool{}
	for out.Calls < bulkMaxCalls {
		if e := ctx.Err(); e != nil {
			return out, e
		}
		if e := ops.check(); e != nil {
			return out, e
		}
		clear(buffer)
		out.Calls++
		count, e := ops.query(buffer)
		if e != nil {
			return out, e
		}
		if e = ctx.Err(); e != nil {
			return out, e
		}
		if count == 0 {
			if e = ops.check(); e != nil {
				return out, e
			}
			return out, nil
		}
		if count < 0 || count > bulkMaxRaw-out.RawRecords {
			return out, errBulkRefused
		}
		out.RawRecords += count
		entries, e := parseBulkRecords(buffer, count)
		if e != nil {
			return out, e
		}
		if len(entries) > bulkMaxEntries-len(out.Entries) {
			return out, errBulkRefused
		}
		for _, entry := range entries {
			if seen[entry.Name] {
				return out, errBulkRefused
			}
			seen[entry.Name] = true
			out.Entries = append(out.Entries, entry)
		}
	}
	return out, errBulkRefused // EOF not established within the call budget.
}
