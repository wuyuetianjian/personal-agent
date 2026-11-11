package darwinprobe

import (
	"strings"
	"testing"
)

func TestComponents(t *testing.T) {
	for _, s := range []string{"", ".", "..", "/abs", "a/b", "a\x00b", strings.Repeat("a", 256)} {
		if validateComponent(s) == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"a", "hello world", "中文"} {
		if err := validateComponent(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	if validateComponents(make([]string, MaxDepth+1)) == nil {
		t.Fatal("unbounded depth")
	}
	if err := validateComponents(nil); err != nil {
		t.Fatal(err)
	}
}
