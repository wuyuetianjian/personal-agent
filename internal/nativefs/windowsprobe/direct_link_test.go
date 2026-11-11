package windowsprobe

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDirectLinkDecoder(t *testing.T) {
	good := utf16.Encode([]rune(`\Device\HarddiskVolume12`))
	cases := []struct {
		name        string
		b           []uint16
		n, returned uint32
		ok          bool
	}{{"valid", good, uint32(len(good) * 2), uint32(len(good) * 2), true}, {"odd", good, 3, 3, false}, {"over", good, 999, 999, false}, {"zero", good, 0, 0, false}, {"nul", append(append([]uint16{}, good...), 0, 'x'), uint32((len(good) + 2) * 2), uint32((len(good) + 2) * 2), false}, {"surrogate", []uint16{0xd800}, 2, 2, false}, {"chain", utf16.Encode([]rune(`\??\C:`)), 12, 12, false}, {"returned", good, uint32(len(good) * 2), 999, false},
		{"returned-less-than-length", good, uint32(len(good) * 2), uint32(len(good)*2 - 2), false},
		{"returned-odd", append(append([]uint16{}, good...), 0), uint32(len(good) * 2), uint32(len(good)*2 + 1), false},
		{"returned-with-terminator-capacity", append(append([]uint16{}, good...), 0), uint32(len(good) * 2), uint32(len(good)*2 + 2), true},
		{"isolated-low-surrogate", []uint16{0xdc00}, 2, 2, false},
	}
	for _, target := range []string{`\Device\HarddiskVolume1\x`, `\Device\HarddiskVolume1:stream`, `\GLOBAL??\C:`, `C:\`, `\\server\share`, `\Device\HarddiskVolumeé`, `\Device\HarddiskVolume` + strings.Repeat("1", 65)} {
		b := utf16.Encode([]rune(target))
		cases = append(cases, struct {
			name        string
			b           []uint16
			n, returned uint32
			ok          bool
		}{target, b, uint32(len(b) * 2), uint32(len(b) * 2), false})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, e := decodeDirectLink(c.b, c.n, c.returned)
			if (e == nil) != c.ok {
				t.Fatalf("anchor=%q err=%v", a, e)
			}
			if c.ok && a != `\Device\HarddiskVolume12\` {
				t.Fatal(a)
			}
		})
	}
}
func TestDirectLinkProtocol(t *testing.T) {
	for _, stage := range []string{"ok", "context", "open", "open-owned-error", "zero", "rights", "inherit", "query", "decode", "close", "path"} {
		t.Run(stage, func(t *testing.T) {
			fault := errors.New("fault")
			closed, target, nativeCalls := 0, 0, 0
			ops := directLinkOps{context: func() error {
				nativeCalls++
				if stage == "context" {
					return fault
				}
				return nil
			}, open: func(s string) (uintptr, error) {
				if s != `\??\D:` {
					t.Fatal(s)
				}
				if stage == "open" {
					return 0, fault
				}
				if stage == "open-owned-error" {
					return 9, fault
				}
				if stage == "zero" {
					return 0, nil
				}
				return 9, nil
			}, qualify: func(uintptr) error {
				if stage == "rights" || stage == "inherit" {
					return fault
				}
				return nil
			}, query: func(uintptr) ([]uint16, uint32, uint32, error) {
				if stage == "query" {
					return nil, 0, 0, fault
				}
				b := utf16.Encode([]rune(`\Device\HarddiskVolume12`))
				if stage == "decode" {
					b[0] = 0
				}
				return b, uint32(len(b) * 2), uint32(len(b) * 2), nil
			}, close: func(uintptr) error {
				closed++
				if stage == "close" {
					return fault
				}
				return nil
			}}
			path := `D:\fixture`
			if stage == "path" {
				path = `D:relative`
			}
			e := withDirectLinkAnchor(path, ops, func(a string, c []string) error {
				target++
				if closed != 1 || a != `\Device\HarddiskVolume12\` || len(c) != 1 || c[0] != "fixture" {
					t.Fatal("bad acquisition boundary")
				}
				return nil
			})
			if (e == nil) != (stage == "ok") || target != map[bool]int{true: 1, false: 0}[stage == "ok"] {
				t.Fatalf("err=%v target=%d", e, target)
			}
			expected := 1
			if stage == "path" || stage == "context" || stage == "open" || stage == "zero" {
				expected = 0
			}
			if stage == "path" && nativeCalls != 0 {
				t.Fatal("invalid path reached native context")
			}
			if closed != expected {
				t.Fatalf("close=%d expected=%d", closed, expected)
			}
		})
	}
}

func TestDirectLinkHandleQualification(t *testing.T) {
	for _, c := range []struct {
		access, length, flags uint32
		ok                    bool
	}{{1, 56, 0, true}, {3, 56, 0, false}, {0, 56, 0, false}, {1, 7, 0, false}, {1, 57, 0, false}, {1, 56, 1, false}} {
		if e := qualifyDirectLinkHandle(c.access, c.length, c.flags); (e == nil) != c.ok {
			t.Fatalf("%+v err=%v", c, e)
		}
	}
}

// A failed validation and uncertain close must both survive wrapping/joining.
func TestDirectLinkErrorPreservation(t *testing.T) {
	validation, cleanup := errors.New("qualification fault"), errors.New("close fault")
	closes, targets := 0, 0
	ops := directLinkOps{context: func() error { return nil }, open: func(string) (uintptr, error) { return 9, nil }, qualify: func(uintptr) error { return validation }, close: func(uintptr) error { closes++; return cleanup }}
	err := withDirectLinkAnchor(`D:\fixture`, ops, func(string, []string) error { targets++; return nil })
	if !errors.Is(err, validation) || !errors.Is(err, cleanup) || closes != 1 || targets != 0 {
		t.Fatalf("err=%v closes=%d targets=%d", err, closes, targets)
	}
}
