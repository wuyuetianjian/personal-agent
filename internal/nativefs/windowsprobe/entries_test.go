package windowsprobe

import (
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func directoryRecord(name string, attrs uint32) []byte {
	units := utf16.Encode([]rune(name))
	b := make([]byte, 88+len(units)*2)
	binary.LittleEndian.PutUint32(b[56:], attrs)
	binary.LittleEndian.PutUint32(b[60:], uint32(len(units)*2))
	for i, u := range units {
		binary.LittleEndian.PutUint16(b[88+i*2:], u)
	}
	return b
}
func directoryRecords(records ...[]byte) []byte {
	var result []byte
	for i, original := range records {
		b := append([]byte(nil), original...)
		if i != len(records)-1 {
			n := (len(b) + 7) &^ 7
			b = append(b, make([]byte, n-len(b))...)
			binary.LittleEndian.PutUint32(b, uint32(n))
		}
		result = append(result, b...)
	}
	return result
}
func TestDirectoryRecordsObservedNamesAndIDs(t *testing.T) {
	file := directoryRecord("文件😀.txt", 0x20)
	var id [16]byte
	for i := range id {
		id[i] = byte(i + 1)
		file[72+i] = id[i]
	}
	// ReparseTag is undefined without FILE_ATTRIBUTE_REPARSE_POINT.
	binary.LittleEndian.PutUint32(file[68:], 0xdeadbeef)
	b := directoryRecords(directoryRecord(".", 0x10), file, directoryRecord("..", 0x10), directoryRecord("Child", 0x10))
	got, count, err := parseDirectoryRecords(b)
	want := []Entry{{Name: "文件😀.txt", ObservedID: id}, {Name: "Child", ObservedDirectory: true}}
	if err != nil || count != 4 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got=%+v count=%d err=%v", got, count, err)
	}
	// Names and IDs must not alias a reusable native buffer.
	for i := range b {
		b[i] = 0
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("observations alias native buffer")
	}
}
func TestDirectoryRecordsOnlyDots(t *testing.T) {
	got, n, err := parseDirectoryRecords(directoryRecords(directoryRecord(".", 0x10), directoryRecord("..", 0x10)))
	if err != nil || n != 2 || len(got) != 0 {
		t.Fatalf("%v %d %v", got, n, err)
	}
}
func TestDirectoryRecordsMalformed(t *testing.T) {
	cases := map[string][]byte{"empty": nil, "short": make([]byte, 87)}
	change := func(name string, mutate func([]byte)) { b := directoryRecord("file", 0); mutate(b); cases[name] = b }
	change("zero-name", func(b []byte) { binary.LittleEndian.PutUint32(b[60:], 0) })
	change("odd-name", func(b []byte) { binary.LittleEndian.PutUint32(b[60:], 3) })
	change("oversize-name", func(b []byte) { binary.LittleEndian.PutUint32(b[60:], 0xffffffff) })
	change("truncated-name", func(b []byte) { binary.LittleEndian.PutUint32(b[60:], 10) })
	change("overlap", func(b []byte) { binary.LittleEndian.PutUint32(b, 88) })
	change("misaligned", func(b []byte) { binary.LittleEndian.PutUint32(b, 95) })
	change("beyond-buffer", func(b []byte) { binary.LittleEndian.PutUint32(b, 104) })
	change("missing-final", func(b []byte) { binary.LittleEndian.PutUint32(b, uint32(len(b))) })
	change("high-surrogate", func(b []byte) { binary.LittleEndian.PutUint16(b[88:], 0xd800) })
	change("low-surrogate", func(b []byte) { binary.LittleEndian.PutUint16(b[88:], 0xdc00) })
	change("terminal-high-surrogate", func(b []byte) { binary.LittleEndian.PutUint16(b[len(b)-2:], 0xdbff) })
	change("nul", func(b []byte) { binary.LittleEndian.PutUint16(b[88:], 0) })
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			got, n, err := parseDirectoryRecords(b)
			if err == nil || got != nil || n != 0 {
				t.Fatalf("malformed accepted/partial: %+v %d %v", got, n, err)
			}
		})
	}
}
func TestDirectoryRecordsUnsafeNamesAndAttributes(t *testing.T) {
	for _, name := range []string{".git", "a/b", `a\b`, "a:stream", "CON", "data", "node_modules", "trail.", "trail ", "a\x00b", strings.Repeat("x", 256)} {
		t.Run(name, func(t *testing.T) {
			got, n, err := parseDirectoryRecords(directoryRecords(directoryRecord("good", 0), directoryRecord(name, 0)))
			if err == nil || got != nil || n != 0 {
				t.Fatalf("unsafe name yielded results: %+v %d %v", got, n, err)
			}
		})
	}
	for _, attr := range []uint32{0x400, 0x1000, 0x40, 0x40000, 0x400000} {
		got, n, err := parseDirectoryRecords(directoryRecords(directoryRecord("good", 0), directoryRecord("unsafe", attr)))
		if err == nil || got != nil || n != 0 {
			t.Fatalf("unsafe attr %#x: %+v %d %v", attr, got, n, err)
		}
	}
	b := directoryRecord(strings.Repeat("x", 255), 0)
	if got, n, err := parseDirectoryRecords(b); err != nil || n != 1 || len(got) != 1 {
		t.Fatalf("max name: %v %d %v", got, n, err)
	}
}
func TestDirectoryRecordsRawRecordBudget(t *testing.T) {
	records := make([][]byte, 1025)
	for i := range records {
		records[i] = directoryRecord(".", 0x10)
	}
	if got, n, err := parseDirectoryRecords(directoryRecords(records[:1024]...)); err != nil || n != 1024 || len(got) != 0 {
		t.Fatalf("boundary: len=%d n=%d err=%v", len(got), n, err)
	}
	if got, n, err := parseDirectoryRecords(directoryRecords(records...)); err == nil || n != 0 || got != nil {
		t.Fatalf("raw budget: len=%d n=%d err=%v", len(got), n, err)
	}
}
