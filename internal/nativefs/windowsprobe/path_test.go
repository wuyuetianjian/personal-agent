package windowsprobe

import (
	"strings"
	"testing"
)

func TestComponents(t *testing.T) {
	for _, p := range [][]string{{"a"}, {"src", "File.go"}, {"文档.txt"}} {
		if err := validateComponents(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", `a\b`, "C:", "a:stream", "a\x00", "a\n", "a\x7f", "a.", "a ", "a?", "a*", "a|", "a<", "a>", `a"`, "CON", "nul.txt", "COM1", "COM¹.txt", "lpt²", "LPT³.ext", "AUX", "PRN", "CONIN$", "CONOUT$", ".git", "DATA", "Node_Modules", string([]byte{0xff}), strings.Repeat("x", 256)}
	for _, p := range bad {
		if validateComponents([]string{p}) == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if validateComponents(nil) == nil {
		t.Error("accepted empty path")
	}
	if validateComponents(strings.Split(strings.Repeat("x/", 32)+"x", "/")) == nil {
		t.Error("accepted excess depth")
	}
}
