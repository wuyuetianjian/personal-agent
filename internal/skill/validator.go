package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"agent/internal/capability"
)

var (
	ErrInvalidManifest   = errors.New("invalid skill manifest")
	ErrUnknownCapability = errors.New("skill references unknown capability")
)

func Validate(m Manifest, capabilities *capability.Registry) error {
	if m.ID == "" || m.Version == "" || m.Name == "" {
		return fmt.Errorf("%w: id, version, and name are required", ErrInvalidManifest)
	}
	if m.Status == "" {
		m.Status = StatusDraft
	}
	if m.Status != StatusDraft && m.Status != StatusActive && m.Status != StatusDeprecated && m.Status != StatusDisabled {
		return fmt.Errorf("%w: unknown status %q", ErrInvalidManifest, m.Status)
	}
	if m.Permissions.MaxLevel == "destructive" || m.Permissions.MaxLevel == "admin" {
		return fmt.Errorf("%w: permission escalation is not allowed", ErrInvalidManifest)
	}
	seen := map[string]bool{}
	for _, node := range m.Workflow.Nodes {
		if node.ID == "" || seen[node.ID] {
			return fmt.Errorf("%w: invalid or duplicate node %q", ErrInvalidManifest, node.ID)
		}
		seen[node.ID] = true
		if node.Capability == "" && node.Role == "" {
			return fmt.Errorf("%w: node %q has no execution target", ErrInvalidManifest, node.ID)
		}
		if node.Capability != "" && capabilities != nil {
			if _, ok := capabilities.Lookup(node.Capability); !ok {
				return fmt.Errorf("%w: %s", ErrUnknownCapability, node.Capability)
			}
		}
	}
	for _, node := range m.Workflow.Nodes {
		for _, dep := range node.DependsOn {
			if dep == node.ID || !seen[dep] {
				return fmt.Errorf("%w: invalid dependency %q for node %q", ErrInvalidManifest, dep, node.ID)
			}
		}
	}
	return nil
}

func Checksum(m Manifest) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func Normalize(m Manifest) Manifest {
	if m.Status == "" {
		m.Status = StatusDraft
	}
	sort.Slice(m.Workflow.Nodes, func(i, j int) bool { return m.Workflow.Nodes[i].ID < m.Workflow.Nodes[j].ID })
	return m
}
