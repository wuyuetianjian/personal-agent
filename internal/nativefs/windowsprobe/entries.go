package windowsprobe

import (
	"encoding/binary"
	"unicode/utf16"
)

// Entry is a directory observation, not authorization to read a file, evidence
// of a single hardlink, or persistent identity. IDs are scoped to the observed
// directory's volume. Enumeration is not a filesystem snapshot.
type Entry struct {
	Name              string
	ObservedID        [16]byte
	ObservedDirectory bool
}

// parseDirectoryRecords consumes only the bytes actually returned by the kernel.
// Explicit offsets avoid treating native struct tail padding as part of a name.
// Only literal dot entries are omitted; unsafe records fail the entire result.
func parseDirectoryRecords(data []byte) ([]Entry, int, error) {
	const header = 88
	const forbidden = 0x400 | 0x1000 | 0x40 | 0x40000 | 0x400000 // reparse, offline, device, recall
	var entries []Entry
	count := 0
	for {
		if len(data) < header || count == 1024 {
			return nil, 0, errUnsafe
		}
		next := uint64(binary.LittleEndian.Uint32(data[0:4]))
		attrs := binary.LittleEndian.Uint32(data[56:60])
		nameBytes := uint64(binary.LittleEndian.Uint32(data[60:64]))
		if nameBytes == 0 || nameBytes%2 != 0 || nameBytes > 510 || nameBytes > uint64(len(data)-header) || attrs&forbidden != 0 {
			return nil, 0, errUnsafe
		}
		end := header + int(nameBytes)
		if next != 0 && (next%8 != 0 || next < uint64(end) || next > uint64(len(data)-header)) {
			return nil, 0, errUnsafe
		}
		units := make([]uint16, int(nameBytes)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[header+i*2:])
		}
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u == 0 || u >= 0xdc00 && u <= 0xdfff {
				return nil, 0, errUnsafe
			}
			if u >= 0xd800 && u <= 0xdbff {
				if i+1 == len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return nil, 0, errUnsafe
				}
				i++
			}
		}
		name := string(utf16.Decode(units))
		count++
		if name != "." && name != ".." {
			if err := validateComponents([]string{name}); err != nil {
				return nil, 0, err
			}
			entry := Entry{Name: name, ObservedDirectory: attrs&0x10 != 0}
			copy(entry.ObservedID[:], data[72:88])
			entries = append(entries, entry)
		}
		if next == 0 {
			return entries, count, nil
		}
		data = data[int(next):]
	}
}

// directoryQueryCompletion uses the routine's nonpending status, not IOSB.Status.
// At termination Information is not consumed. Only successful pages carry bytes.
func directoryQueryCompletion(status uint32, information uintptr, capacity int) (int, bool, error) {
	if status == 0x80000006 { // STATUS_NO_MORE_FILES
		return 0, true, nil
	}
	if status != 0 || information == 0 || capacity <= 0 || information > uintptr(capacity) {
		return 0, false, errUnsafe
	}
	return int(information), false, nil
}
