package manager

import (
	"testing"
)

func TestParse(t *testing.T) {
	input := "composition.krateo.io\nwidgets.templates.krateo.io\ncore.krateo.io"
	groups := parse(input)

	for _, g := range []string{
		"composition.krateo.io",
		"widgets.templates.krateo.io",
		"core.krateo.io",
	} {
		if !isManagedGroup(groups, g) {
			t.Errorf("expected %q to be a managed group", g)
		}
	}

	if isManagedGroup(groups, "unknown.group.io") {
		t.Error("expected unknown.group.io to not be a managed group")
	}
}

func TestParseIgnoresBlankLines(t *testing.T) {
	input := "\ncomposition.krateo.io\n\ncore.krateo.io\n"
	groups := parse(input)
	if len(groups) != 2 {
		t.Errorf("expected 2 groups, got %d", len(groups))
	}
}

func TestMustLoad(t *testing.T) {
	groups := mustLoad("managed_groups")
	if len(groups) == 0 {
		t.Fatal("expected MustLoad to return non-empty groups from embedded file")
	}
}
