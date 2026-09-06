package browser

import (
	"errors"
	"strings"
)

var ErrElementNotFound = errors.New("browser element not found")

type LocatedElement struct {
	Selector string
	Role     string
	Text     string
	Target   Target
}

type SemanticNode struct {
	Selector string
	Role     string
	Text     string
	Name     string
}

func LocateSemantic(nodes []SemanticNode, target Target) (LocatedElement, error) {
	for _, node := range nodes {
		if target.Selector != "" && node.Selector == target.Selector {
			return locatedFromNode(node, target), nil
		}
	}
	for _, node := range nodes {
		if target.Text != "" && strings.Contains(strings.ToLower(node.Text), strings.ToLower(target.Text)) {
			return locatedFromNode(node, target), nil
		}
	}
	for _, node := range nodes {
		if target.Role != "" && strings.EqualFold(node.Role, target.Role) {
			name := node.Name
			if name == "" {
				name = node.Text
			}
			if target.Text == "" || strings.Contains(strings.ToLower(name), strings.ToLower(target.Text)) {
				return locatedFromNode(node, target), nil
			}
		}
	}
	return LocatedElement{}, ErrElementNotFound
}

func locatedFromNode(node SemanticNode, target Target) LocatedElement {
	return LocatedElement{
		Selector: node.Selector,
		Role:     node.Role,
		Text:     node.Text,
		Target:   target,
	}
}

func selectorForTarget(target Target) string {
	if target.Selector != "" {
		return target.Selector
	}
	if target.Text != "" {
		return target.Text
	}
	return ""
}

func hasSemanticTarget(target Target) bool {
	return target.Selector != "" || target.Text != "" || target.Role != "" || target.Description != ""
}
