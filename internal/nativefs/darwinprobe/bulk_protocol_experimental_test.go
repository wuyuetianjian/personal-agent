//go:build nativeprobe_darwin_bulk

package darwinprobe

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

// Synthetic SDK layout: len, returned attribute_set_t, error, name reference,
// object type, flags, file ID, then the NUL-terminated variable name.
func bulkRecord(name string, inode uint64, kind uint32) []byte {
	n := (52 + len(name) + 1 + 7) &^ 7
	b := make([]byte, n)
	put := func(at int, v uint32) { binary.LittleEndian.PutUint32(b[at:], v) }
	put(0, uint32(n))
	put(4, bulkCommonMask)
	put(28, 24)
	put(32, uint32(len(name)+1))
	put(36, kind)
	binary.LittleEndian.PutUint64(b[44:], inode)
	copy(b[52:], name)
	return b
}
func TestBulkParserValid(t *testing.T) {
	b := append(bulkRecord("leaf", 42, 1), bulkRecord("folder", 43, 2)...)
	got, e := parseBulkRecords(b, 2)
	want := []BulkEntry{{Name: "leaf", Inode: 42}, {Name: "folder", Inode: 43, Directory: true}}
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v err %v", got, e)
	}
}
func TestBulkParserRefusals(t *testing.T) {
	tests := map[string]func([]byte) []byte{
		"short":            func(b []byte) []byte { return b[:51] },
		"record-zero":      func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 0); return b },
		"record-unaligned": func(b []byte) []byte { binary.LittleEndian.PutUint32(b, 55); return b },
		"record-overflow":  func(b []byte) []byte { binary.LittleEndian.PutUint32(b, ^uint32(0)); return b },
		"missing-mask":     func(b []byte) []byte { binary.LittleEndian.PutUint32(b[4:], bulkCommonMask&^8); return b },
		"unexpected-mask":  func(b []byte) []byte { binary.LittleEndian.PutUint32(b[8:], 1); return b },
		"entry-error":      func(b []byte) []byte { binary.LittleEndian.PutUint32(b[24:], 5); return b },
		"negative-offset":  func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], ^uint32(0)); return b },
		"metadata-overlap": func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 0); return b },
		"offset-overflow":  func(b []byte) []byte { binary.LittleEndian.PutUint32(b[28:], 0x7fffffff); return b },
		"name-overflow":    func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32:], ^uint32(0)); return b },
		"name-empty":       func(b []byte) []byte { binary.LittleEndian.PutUint32(b[32:], 1); b[52] = 0; return b },
		"missing-nul":      func(b []byte) []byte { b[56] = 'x'; return b },
		"embedded-nul":     func(b []byte) []byte { b[53] = 0; return b },
		"slash":            func(b []byte) []byte { b[53] = '/'; return b },
		"zero-id":          func(b []byte) []byte { binary.LittleEndian.PutUint64(b[44:], 0); return b },
		"unknown-type":     func(b []byte) []byte { binary.LittleEndian.PutUint32(b[36:], 0); return b },
		"fifo":             func(b []byte) []byte { binary.LittleEndian.PutUint32(b[36:], 7); return b },
		"symlink":          func(b []byte) []byte { binary.LittleEndian.PutUint32(b[36:], 5); return b },
		"firmlink":         func(b []byte) []byte { binary.LittleEndian.PutUint32(b[40:], 0x800000); return b },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			if got, e := parseBulkRecords(change(bulkRecord("leaf", 1, 1)), 1); e == nil || len(got) != 0 {
				t.Fatalf("partial/accepted: %+v %v", got, e)
			}
		})
	}
	for _, count := range []int{-1, 0, 2, 1025} {
		if got, e := parseBulkRecords(bulkRecord("leaf", 1, 1), count); e == nil || len(got) != 0 {
			t.Fatalf("count %d: %+v %v", count, got, e)
		}
	}
	for _, name := range []string{".", "..", string(make([]byte, 256))} {
		if _, e := parseBulkRecords(bulkRecord(name, 1, 1), 1); e == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
}
func TestBulkProtocolSuccess(t *testing.T) {
	calls, closed, checked := 0, 0, 0
	ops := bulkOps{query: func(b []byte) (int, error) {
		calls++
		if len(b) != 65536 {
			t.Fatal(len(b))
		}
		if calls == 1 {
			copy(b, bulkRecord("a", 1, 1))
			return 1, nil
		}
		for _, v := range b {
			if v != 0 {
				t.Fatal("stale buffer")
			}
		}
		return 0, nil
	}, check: func() error { checked++; return nil }, close: func() error { closed++; return nil }}
	got, e := collectBulk(context.Background(), ops)
	if e != nil || len(got.Entries) != 1 || got.Calls != 2 || got.RawRecords != 1 || closed != 1 || checked < 2 {
		t.Fatalf("got %+v err %v calls %d close %d check %d", got, e, calls, closed, checked)
	}
}
func TestBulkProtocolFailureCleanup(t *testing.T) {
	fault, cleanup := errors.New("operation fault"), errors.New("close fault")
	for _, stage := range []string{"query", "check", "close", "query-close", "late-check", "late-cancel", "early-cancel", "duplicate", "malformed", "count"} {
		t.Run(stage, func(t *testing.T) {
			calls, closed, checks := 0, 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "early-cancel" {
				cancel()
			}
			ops := bulkOps{query: func(b []byte) (int, error) {
				calls++
				if stage == "query" || stage == "query-close" {
					return 0, fault
				}
				if stage == "count" {
					return 1025, nil
				}
				if stage == "malformed" {
					return 1, nil
				}
				if stage == "late-cancel" {
					cancel()
				}
				if calls == 1 || stage == "duplicate" {
					copy(b, bulkRecord("a", 1, 1))
					return 1, nil
				}
				return 0, nil
			}, check: func() error {
				checks++
				if stage == "check" || (stage == "late-check" && checks > 1) {
					return fault
				}
				return nil
			}, close: func() error {
				closed++
				if stage == "close" || stage == "query-close" {
					return cleanup
				}
				return nil
			}}
			got, e := collectBulk(ctx, ops)
			if e == nil || len(got.Entries) != 0 || got.Calls != 0 || got.RawRecords != 0 || closed != 1 {
				t.Fatalf("got %+v err %v closes %d", got, e, closed)
			}
			if stage == "early-cancel" && calls != 0 {
				t.Fatal("queried after cancellation")
			}
			if stage == "query-close" && (!errors.Is(e, fault) || !errors.Is(e, cleanup)) {
				t.Fatalf("lost cause: %v", e)
			}
			if stage == "late-cancel" && !errors.Is(e, context.Canceled) {
				t.Fatalf("lost cancellation: %v", e)
			}
		})
	}
}
func TestBulkProtocolBudgets(t *testing.T) {
	for _, mode := range []string{"entries", "calls", "raw"} {
		t.Run(mode, func(t *testing.T) {
			calls, closed := 0, 0
			ops := bulkOps{query: func(b []byte) (int, error) {
				calls++
				if mode == "raw" {
					return 1025, nil
				}
				n := 1
				if mode == "entries" {
					n = 129
				}
				at := 0
				for j := 0; j < n; j++ {
					r := bulkRecord(fmt.Sprintf("p%d-%d", calls, j), uint64(calls*200+j), 1)
					copy(b[at:], r)
					at += len(r)
				}
				return n, nil
			}, check: func() error { return nil }, close: func() error { closed++; return nil }}
			got, e := collectBulk(context.Background(), ops)
			if e == nil || len(got.Entries) != 0 || closed != 1 || calls > 16 {
				t.Fatalf("%+v %v calls=%d closed=%d", got, e, calls, closed)
			}
			if mode == "calls" && calls != 16 {
				t.Fatalf("wrong call budget %d", calls)
			}
		})
	}
}

func TestBulkParserBoundariesAndNoPartial(t *testing.T) {
	name := string(make([]byte, 255))
	raw := []byte(name)
	for i := range raw {
		raw[i] = 'a'
	}
	name = string(raw)
	got, e := parseBulkRecords(bulkRecord(name, 9, 1), 1)
	if e != nil || len(got) != 1 || got[0].Name != name {
		t.Fatalf("255-byte name: %+v %v", got, e)
	}
	good := bulkRecord("good", 1, 1)
	bad := bulkRecord("bad", 2, 1)
	binary.LittleEndian.PutUint32(bad[24:], 5)
	if got, e := parseBulkRecords(append(good, bad...), 2); e == nil || len(got) != 0 {
		t.Fatalf("partial: %+v %v", got, e)
	}
}
func TestBulkProtocolEOFBudgetAndNilContext(t *testing.T) {
	for _, mode := range []string{"empty", "sixteenth-eof", "nil-context", "interrupt-no-retry"} {
		t.Run(mode, func(t *testing.T) {
			calls, closed := 0, 0
			interrupted := errors.New("interrupted query")
			ops := bulkOps{query: func(b []byte) (int, error) {
				calls++
				if mode == "interrupt-no-retry" {
					return 0, interrupted
				}
				if mode == "empty" || calls == 16 {
					return 0, nil
				}
				copy(b, bulkRecord(fmt.Sprint(calls), uint64(calls), 1))
				return 1, nil
			}, check: func() error { return nil }, close: func() error { closed++; return nil }}
			var ctx context.Context = context.Background()
			if mode == "nil-context" {
				ctx = nil
			}
			got, e := collectBulk(ctx, ops)
			if closed != 1 {
				t.Fatal(closed)
			}
			switch mode {
			case "empty":
				if e != nil || got.Calls != 1 || len(got.Entries) != 0 {
					t.Fatalf("%+v %v", got, e)
				}
			case "sixteenth-eof":
				if e != nil || got.Calls != 16 || len(got.Entries) != 15 {
					t.Fatalf("%+v %v", got, e)
				}
			case "nil-context":
				if e == nil || calls != 0 || len(got.Entries) != 0 {
					t.Fatalf("%+v %v calls=%d", got, e, calls)
				}
			case "interrupt-no-retry":
				if !errors.Is(e, interrupted) || calls != 1 || len(got.Entries) != 0 {
					t.Fatalf("%+v %v calls=%d", got, e, calls)
				}
			}
		})
	}
}
func FuzzBulkParser(f *testing.F) {
	f.Add(bulkRecord("seed", 1, 1), 1)
	f.Add([]byte{0}, 1)
	f.Fuzz(func(t *testing.T, b []byte, count int) {
		if len(b) > bulkBufferSize+1 {
			return
		}
		entries, e := parseBulkRecords(b, count)
		if e != nil && len(entries) != 0 {
			t.Fatal("partial result on malformed data")
		}
		if e == nil {
			if len(entries) != count {
				t.Fatal("bad count")
			}
			for _, entry := range entries {
				if validateComponent(entry.Name) != nil || entry.Inode == 0 {
					t.Fatal("invalid successful entry")
				}
			}
		}
	})
}
