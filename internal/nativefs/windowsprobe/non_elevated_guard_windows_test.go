//go:build windows && nativeprobe_non_elevated_guard

package windowsprobe

import (
	"agent/scripts/windows-non-elevated-launcher/tokencheck"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// Must run before m.Run: no native test is admissible under an unqualified token.
func TestMain(m *testing.M) {
	if os.Getenv("PERSONAL_AGENT_FILTERED_CHILD") != "1" {
		fmt.Fprintln(os.Stderr, "TOKEN_GUARD_BLOCKED: launcher marker absent")
		os.Exit(1)
	}
	s, e := tokencheck.CheckCurrent()
	if e != nil {
		fmt.Fprintf(os.Stderr, "TOKEN_GUARD_BLOCKED: %v\n", e)
		os.Exit(1)
	}
	raw, e := json.Marshal(s)
	if e != nil {
		fmt.Fprintln(os.Stderr, "TOKEN_GUARD_BLOCKED: snapshot encoding", e)
		os.Exit(1)
	}
	fmt.Printf("TOKEN_GUARD_OK %s\n", raw)
	os.Exit(m.Run())
}
