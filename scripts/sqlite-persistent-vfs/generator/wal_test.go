package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinkedWALModeRefusesUntilNativePrivateRED(t *testing.T) {
	for _, mode := range []string{"proposal", "output"} {
		t.Run(mode, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "wal.go.txt")
			args := []string{"-linked-wal", "-source", "deliberately-unread-source"}
			if mode == "proposal" {
				args = append(args, "-propose")
			} else {
				args = append(args, "-manifest", "linked-manifest.json", "-cache-root", t.TempDir(), "-out", output)
			}
			err := run(args)
			if err == nil || !strings.Contains(err.Error(), "linked WAL generation requires verified private-method RED") {
				t.Fatalf("expected explicit WAL stage refusal, got %v", err)
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatalf("refusal produced output: %v", err)
			}
		})
	}
}

func TestLinkedWALCannotAliasMainMode(t *testing.T) {
	err := run([]string{"-linked", "-linked-wal", "-source", "deliberately-unread-source", "-propose"})
	if err == nil || !strings.Contains(err.Error(), "linked modes are mutually exclusive") {
		t.Fatalf("expected mode conflict, got %v", err)
	}
}
