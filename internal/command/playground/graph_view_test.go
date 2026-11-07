package playground

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/fga"
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

func largeGraph() fga.Graph {
	var tds []openfga.TypeDefinition
	tds = append(tds, openfga.TypeDefinition{Type: "user"})
	for i := 0; i < 12; i++ {
		rels := map[string]openfga.Userset{}
		meta := map[string]openfga.RelationMetadata{}
		for _, r := range []string{"a", "b", "c", "d", "e"} {
			rels[r] = openfga.This()
			meta[r] = openfga.RelationMetadata{DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("user")}}
		}
		if i > 0 {
			rels["parent"] = openfga.This()
			meta["parent"] = openfga.RelationMetadata{DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType(fmt.Sprintf("t%d", i-1))}}
		}
		tds = append(tds, openfga.TypeDefinition{Type: fmt.Sprintf("t%d", i), Relations: rels, Metadata: &openfga.Metadata{Relations: meta}})
	}
	return fga.ParseModel(&openfga.AuthorizationModel{ID: "big", SchemaVersion: "1.1", TypeDefinitions: tds})
}

func loadedFocused(g fga.Graph) tea.Model {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "m", graph: g})
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter")) // focuses the pane
	return m
}

func TestGraphSmallModelOpensExpanded(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "model-1", graph: sampleGraph()})
	mm := m.(Model)
	if len(mm.graphExpanded) != len(mm.graph.Types) || mm.graphSel != "" {
		t.Fatalf("expanded=%v sel=%q", mm.graphExpanded, mm.graphSel)
	}
	if !strings.Contains(ansi.Strip(mm.graphVP.View()), "=owner") {
		t.Error("small model should show detail")
	}
}

func TestGraphLargeModelOpensCompactWithSelection(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "big", graph: largeGraph()})
	mm := m.(Model)
	if len(mm.graphExpanded) != 0 {
		t.Error("large model must open compact")
	}
	if mm.graphSel == "" {
		t.Error("large model must select a card")
	}
}

func TestGraphTabCyclesSelection(t *testing.T) {
	m := loadedFocused(largeGraph())
	start := m.(Model).graphSel
	m, _ = m.Update(key("tab"))
	next := m.(Model).graphSel
	if next == start {
		t.Fatal("tab did not move selection")
	}
	m, _ = m.Update(key("shift+tab"))
	if m.(Model).graphSel != start {
		t.Fatal("shift+tab did not go back")
	}
	for range m.(Model).graphBounds {
		m, _ = m.Update(key("tab"))
	}
	if m.(Model).graphSel != start {
		t.Fatal("tab did not wrap to start")
	}
}

func TestGraphEnterTogglesExpansion(t *testing.T) {
	m := loadedFocused(largeGraph())
	sel := m.(Model).graphSel
	m, _ = m.Update(key("enter"))
	mm := m.(Model)
	if !strings.Contains(ansi.Strip(mm.graphVP.View()), "←user") {
		t.Error("expanded card should show relation detail")
	}
	if !mm.graphExpanded[sel] || !strings.Contains(mm.status, "expanded") {
		t.Fatalf("expand failed: %v %q", mm.graphExpanded, mm.status)
	}
	m, _ = m.Update(key("enter"))
	mm = m.(Model)
	if strings.Contains(ansi.Strip(mm.graphVP.View()), "←user") {
		t.Error("collapsed card should hide relation detail")
	}
	if mm.graphExpanded[sel] || !strings.Contains(mm.status, "collapsed") {
		t.Fatalf("collapse failed: %v %q", mm.graphExpanded, mm.status)
	}
}

func TestGraphCapitalEExpandsAndCollapsesAll(t *testing.T) {
	m := loadedFocused(largeGraph())
	m, _ = m.Update(key("E"))
	if n := len(m.(Model).graphExpanded); n != len(m.(Model).graph.Types) {
		t.Fatalf("E expanded %d", n)
	}
	m, _ = m.Update(key("E"))
	if len(m.(Model).graphExpanded) != 0 {
		t.Fatal("second E should collapse all")
	}
}

func TestGraphStateResetsOnReloadAndWeightedToggle(t *testing.T) {
	m := loadedFocused(largeGraph())
	m, _ = m.Update(key("E"))
	m, _ = m.Update(modelLoadedMsg{modelID: "again", graph: largeGraph()})
	if len(m.(Model).graphExpanded) != 0 {
		t.Fatal("reload should reset expansion")
	}
	m, _ = m.Update(key("v"))
	if mm := m.(Model); mm.graphSel != "" || len(mm.graphBounds) != 0 {
		t.Fatal("weighted view should clear selection")
	}
	m, _ = m.Update(key("v"))
	if m.(Model).graphSel == "" {
		t.Fatal("selection should be restored")
	}
}

func TestGraphTabNoopWithoutTypesOrInWeighted(t *testing.T) {
	m := loadedFocused(fga.Graph{})
	m, _ = m.Update(key("tab"))
	if m.(Model).graphSel != "" {
		t.Fatal("sel changed on empty graph")
	}
	m = loadedFocused(largeGraph())
	m, _ = m.Update(key("v"))
	before := m.(Model)
	y := before.graphVP.YOffset()
	m, _ = m.Update(key("tab"))
	after := m.(Model)
	if after.graphSel != "" || after.graphBounds != nil || after.graphVP.YOffset() != y {
		t.Fatal("tab changed state in weighted view")
	}
}

func TestGraphTabFromNoSelectionPicksFirstThenLast(t *testing.T) {
	m := loadedFocused(sampleGraph())
	m, _ = m.Update(key("tab"))
	mm := m.(Model)
	if mm.graphSel != mm.graphBounds[0].Type {
		t.Fatalf("tab picked %q, want first", mm.graphSel)
	}
	mm.graphSel = ""
	m, _ = mm.Update(key("shift+tab"))
	mm = m.(Model)
	if mm.graphSel != mm.graphBounds[len(mm.graphBounds)-1].Type {
		t.Fatalf("shift+tab picked %q, want last", mm.graphSel)
	}
}
