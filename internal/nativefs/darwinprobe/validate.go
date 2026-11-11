package darwinprobe

import (
	"fmt"
	"strings"
)

// MaxDepth bounds retained child descriptors (in addition to the root).
const MaxDepth = 32

func validateComponent(name string) error {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("invalid single path component")
	}
	return nil
}
func validateComponents(parts []string) error {
	if len(parts) > MaxDepth {
		return fmt.Errorf("directory observation exceeds depth %d", MaxDepth)
	}
	for _, part := range parts {
		if err := validateComponent(part); err != nil {
			return err
		}
	}
	return nil
}
