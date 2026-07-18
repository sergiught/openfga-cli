package playground

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestGraphViewToggle covers the `v` key switching the model pane between the
// node-link diagram and the weighted-graph view (and back). The pane must be
// focused first, like the other graph keys.
func TestGraphViewToggle(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(key("3"))     // model graph section
	m, _ = m.Update(key("enter")) // focus the pane

	diagram := ansi.Strip(m.(Model).viewString())
	if strings.Contains(diagram, "weighted graph") {
		t.Fatalf("expected the node-link diagram first, got weighted graph:\n%s", diagram)
	}

	// The focused pane footer advertises the toggle (the view you'd switch to).
	if !strings.Contains(diagram, "v weighted") {
		t.Errorf("model footer should advertise 'v weighted' on the diagram")
	}

	m, _ = m.Update(key("v"))
	weighted := ansi.Strip(m.(Model).viewString())
	if !strings.Contains(weighted, "weighted graph") {
		t.Fatalf("v did not switch to the weighted graph:\n%s", weighted)
	}
	if !strings.Contains(weighted, "v diagram") {
		t.Errorf("model footer should advertise 'v diagram' on the weighted graph")
	}

	m, _ = m.Update(key("v"))
	back := ansi.Strip(m.(Model).viewString())
	if strings.Contains(back, "weighted graph") {
		t.Fatalf("v did not switch back to the diagram:\n%s", back)
	}
}
