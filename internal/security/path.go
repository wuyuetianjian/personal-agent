package security

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var ErrPathOutsideRoot = errors.New("path outside allowed root")

type PathPolicy struct {
	Root string
}

func (p PathPolicy) Resolve(candidate string) (string, error) {
	if strings.TrimSpace(candidate) == "" || containsNUL(candidate) {
		return "", ErrPathOutsideRoot
	}
	root := p.Root
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absCandidate, err := filepath.Abs(filepath.Join(absRoot, candidate))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absCandidate)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", ErrPathOutsideRoot
	}
	return absCandidate, nil
}
