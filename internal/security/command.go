package security

import (
	"errors"
	"strings"
)

var ErrUnsafeCommand = errors.New("unsafe command")

type CommandSpec struct {
	Program string
	Args    []string
	Shell   bool
}

func (c CommandSpec) Validate() error {
	if strings.TrimSpace(c.Program) == "" {
		return ErrUnsafeCommand
	}
	if containsNUL(c.Program) {
		return ErrUnsafeCommand
	}
	if c.Shell {
		return nil
	}
	if hasShellMeta(c.Program) {
		return ErrUnsafeCommand
	}
	for _, arg := range c.Args {
		if containsNUL(arg) || hasShellMeta(arg) {
			return ErrUnsafeCommand
		}
	}
	return nil
}

func hasShellMeta(value string) bool {
	return strings.ContainsAny(value, "|&;<>`$(){}[]*?!\n\r")
}

func containsNUL(value string) bool {
	return strings.ContainsRune(value, '\x00')
}
