package browser

import (
	"errors"
	"testing"
)

func TestLocateSemanticPrefersSelectorThenTextThenRole(t *testing.T) {
	nodes := []SemanticNode{
		{Selector: "#save", Role: "button", Text: "Save"},
		{Selector: "#delete", Role: "button", Text: "Delete"},
	}

	located, err := LocateSemantic(nodes, Target{Selector: "#delete", Text: "Save"})
	if err != nil {
		t.Fatalf("LocateSemantic() selector error = %v", err)
	}
	if located.Selector != "#delete" {
		t.Fatalf("selector match = %q, want #delete", located.Selector)
	}

	located, err = LocateSemantic(nodes, Target{Text: "Sav"})
	if err != nil {
		t.Fatalf("LocateSemantic() text error = %v", err)
	}
	if located.Selector != "#save" {
		t.Fatalf("text match = %q, want #save", located.Selector)
	}

	located, err = LocateSemantic(nodes, Target{Role: "button", Text: "Delete"})
	if err != nil {
		t.Fatalf("LocateSemantic() role error = %v", err)
	}
	if located.Selector != "#delete" {
		t.Fatalf("role match = %q, want #delete", located.Selector)
	}
}

func TestLocateSemanticNotFound(t *testing.T) {
	_, err := LocateSemantic([]SemanticNode{{Selector: "#save", Text: "Save"}}, Target{Text: "Missing"})
	if !errors.Is(err, ErrElementNotFound) {
		t.Fatalf("LocateSemantic() error = %v, want %v", err, ErrElementNotFound)
	}
}
