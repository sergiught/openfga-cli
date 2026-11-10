package playground

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/ui/shell"
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

func TestGraphResizeKeepsSelectionAndExpansion(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "big", graph: largeGraph()})
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter"))
	m, _ = m.Update(key("tab"))
	m, _ = m.Update(key("enter"))
	before := m.(Model)
	sel := before.graphSel
	if sel == "" || !before.graphExpanded[sel] {
		t.Fatalf("setup: sel=%q expanded=%v", sel, before.graphExpanded)
	}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 160, Height: 30})
	after := m.(Model)
	if after.graphSel != sel || !after.graphExpanded[sel] || len(after.graphExpanded) != len(before.graphExpanded) {
		t.Errorf("state lost: sel=%q expanded=%v", after.graphSel, after.graphExpanded)
	}
	if len(after.graphBounds) == 0 {
		t.Error("bounds empty after resize")
	}
	if after.graphVP.Height() == before.graphVP.Height() {
		t.Fatal("viewport height did not change; test is vacuous")
	}
	if !strings.Contains(ansi.Strip(after.graphVP.GetContent()), "←user") {
		t.Error("expanded card detail missing after resize")
	}
	wantBounds := fmt.Sprint(after.graphBounds)
	fresh := after
	fresh.renderGraph()
	if fmt.Sprint(fresh.graphBounds) != wantBounds {
		t.Error("bounds are stale relative to the resized viewport")
	}
}

func TestGraphFooterAndHelpAdvertiseExpand(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "big", graph: largeGraph()})
	m, _ = m.Update(key("3"))
	m, _ = m.Update(key("enter"))
	out := ansi.Strip(m.(Model).viewString())
	if !strings.Contains(out, "tab select") || !strings.Contains(out, "↵ expand") {
		t.Errorf("footer lacks expand hint:\n%s", out)
	}
	m, _ = m.Update(key("?"))
	if h := ansi.Strip(view(m)); !strings.Contains(h, "expand/collapse all") {
		t.Errorf("help lacks 'expand/collapse all':\n%s", h)
	}
}

func TestGraphFooterNoExpandHintOnEmptyModel(t *testing.T) {
	m := loadedFocused(fga.Graph{})
	if out := ansi.Strip(m.(Model).viewString()); strings.Contains(out, "tab select") {
		t.Errorf("empty model should show no expand hint:\n%s", out)
	}
}

func TestGraphClickSelectsThenExpands(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(modelLoadedMsg{modelID: "big", graph: largeGraph()})
	m, _ = m.Update(key("3"))
	mm := m.(Model)
	bx, by := mm.sh.MainBodyOrigin()
	target := mm.graphBounds[1] // not the initially selected card
	// The bottom border row: a hit-test that ignored the pinned legend's height
	// would land below the card.
	click := tea.MouseClickMsg{X: bx + target.X + 1, Y: by + mm.graphHeaderHeight() + target.Y + target.H - 1, Button: tea.MouseLeft}
	m, _ = m.Update(click)
	mm = m.(Model)
	if mm.graphSel != target.Type || mm.focus != shell.FocusPanel {
		t.Fatalf("click should select %s and focus panel", target.Type)
	}
	m, _ = m.Update(click)
	if !m.(Model).graphExpanded[target.Type] {
		t.Error("second click should expand")
	}
}

func TestGraphClickOnEmptyCanvasKeepsSelection(t *testing.T) {
	g := largeGraph()
	g.Types = g.Types[:2] // narrow canvas, far smaller than the pane
	m := loadedFocused(g)
	m, _ = m.Update(key("tab"))
	mm := m.(Model)
	sel := mm.graphSel
	bx, by := mm.sh.MainBodyOrigin()
	right := 0
	for _, b := range mm.graphBounds {
		right = max(right, b.X+b.W)
	}
	w, _ := mm.contentSize()
	if bx+right+1 >= bx+w {
		t.Skip("canvas not narrower than the pane")
	}
	m, _ = m.Update(tea.MouseClickMsg{X: bx + right + 1, Y: by + mm.graphHeaderHeight() + mm.graphBounds[0].Y + 1, Button: tea.MouseLeft})
	mm = m.(Model)
	if mm.graphSel != sel || len(mm.graphExpanded) != 2 {
		t.Fatalf("empty-canvas click changed state: sel=%q expanded=%v", mm.graphSel, mm.graphExpanded)
	}
}

func TestGraphClickBelowCardIsIgnored(t *testing.T) {
	m := loadedFocused(largeGraph())
	mm := m.(Model)
	sel := mm.graphSel
	bx, by := mm.sh.MainBodyOrigin()
	// The row just below a card belongs to no card bound.
	b0 := mm.graphBounds[0]
	m, _ = m.Update(tea.MouseClickMsg{X: bx + b0.X + 1, Y: by + mm.graphHeaderHeight() + b0.Y + b0.H, Button: tea.MouseLeft})
	mm = m.(Model)
	if mm.graphSel != sel || len(mm.graphExpanded) != 0 {
		t.Fatalf("click past card changed state: sel=%q expanded=%v", mm.graphSel, mm.graphExpanded)
	}
}

func TestGraphClickOutsidePaneIgnored(t *testing.T) {
	m := loadedFocused(largeGraph())
	mm := m.(Model)
	mm.graphVP.SetXOffset(180)
	sel := mm.graphSel
	// Screen (5, 10) is in the sidebar; with the pan it maps into a card.
	if typ, ok := mm.graphCardAt(5, 10); ok {
		t.Fatalf("sidebar click resolved to card %q", typ)
	}
	var next tea.Model = mm
	next, _ = next.Update(tea.MouseClickMsg{X: 5, Y: 10, Button: tea.MouseLeft})
	if got := next.(Model).graphSel; got != sel {
		t.Fatalf("sidebar click changed selection %q -> %q", sel, got)
	}
}

func TestGraphKeysIgnoredInOptimizerView(t *testing.T) {
	m := loadedFocused(largeGraph())
	mm := m.(Model)
	mm.opt.view = optBench
	sel, nexp := mm.graphSel, len(mm.graphExpanded)
	var cur tea.Model = mm
	for _, k := range []string{"tab", "shift+tab", "enter", "E"} {
		cur, _ = cur.Update(key(k))
		got := cur.(Model)
		if got.graphSel != sel || len(got.graphExpanded) != nexp {
			t.Fatalf("%s changed graph state in bench view: sel=%q expanded=%d", k, got.graphSel, len(got.graphExpanded))
		}
	}
}

func TestGraphCardClickIgnoredWhileEditorOpen(t *testing.T) {
	m := loadedFocused(largeGraph())
	m, _ = m.Update(key("e"))
	mm := m.(Model)
	if !mm.editorOpen {
		t.Fatal("editor did not open")
	}
	sel := mm.graphSel
	bx, by := mm.sh.MainBodyOrigin()
	b := mm.graphBounds[0]
	click := tea.MouseClickMsg{X: bx + b.X + 1, Y: by + b.Y + 1, Button: tea.MouseLeft}
	m, _ = m.Update(click)
	m, _ = m.Update(click)
	got := m.(Model)
	if got.graphSel != sel || len(got.graphExpanded) != 0 {
		t.Fatalf("editor-open click changed graph: sel=%q expanded=%v", got.graphSel, got.graphExpanded)
	}
}

func TestGraphTabKeepsTitleVisibleOnWideCard(t *testing.T) {
	g := largeGraph()
	wide := strings.Repeat("w", 70)
	g.Types[3].Relations[0].Name = wide
	m := loadedFocused(g)
	m, _ = m.Update(key("E"))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 70, Height: 30})
	for i := 0; i < 14; i++ {
		m, _ = m.Update(key("tab"))
		mm := m.(Model)
		b, ok := mm.cardBounds(mm.graphSel)
		if !ok {
			t.Fatal("selected card has no bounds")
		}
		if b.X-2 < mm.graphVP.XOffset() {
			t.Fatalf("title of %s hidden: card X=%d xoffset=%d", b.Type, b.X, mm.graphVP.XOffset())
		}
	}
}

func TestGraphTabDoesNotScrollWhenCardVisible(t *testing.T) {
	m := loadedFocused(largeGraph())
	m, _ = m.Update(key("tab"))
	mm := m.(Model)
	if mm.graphVP.YOffset() != 0 || mm.graphTarget != 0 {
		t.Fatalf("tab to a visible card scrolled: y=%d target=%v", mm.graphVP.YOffset(), mm.graphTarget)
	}
	if !strings.Contains(ansi.Strip(mm.viewString()), "tab/enter") {
		t.Error("header hint scrolled out of view")
	}
}

func TestGraphLeafTypesAreNotExpandable(t *testing.T) {
	m := loadedFocused(largeGraph())
	mm := m.(Model)
	for _, b := range mm.graphBounds {
		if b.Type == "user" {
			mm.graphSel = "user"
		}
	}
	if mm.graphSel != "user" {
		t.Fatal("user card not found")
	}
	var cur tea.Model = mm
	cur, _ = cur.Update(key("enter"))
	if n := len(cur.(Model).graphExpanded); n != 0 {
		t.Fatalf("enter on leaf expanded %d types", n)
	}
	// Expand every non-leaf type one by one.
	for _, td := range mm.graph.Types {
		if len(td.Relations) == 0 {
			continue
		}
		g := cur.(Model)
		g.graphSel = td.Name
		cur = g
		cur, _ = cur.Update(key("enter"))
	}
	g := cur.(Model)
	if strings.Contains(ansi.Strip(g.graphVP.View()), "tab/enter") {
		t.Error("header still advertises expand with only leaf types collapsed")
	}
	cur, _ = cur.Update(key("E"))
	if n := len(cur.(Model).graphExpanded); n != 0 {
		t.Fatalf("E should collapse all, %d still expanded", n)
	}
}

// stressGraph parses the fga package's stress fixture: 22 types whose first
// card sits well below the top of the canvas.
func stressGraph(t *testing.T) fga.Graph {
	t.Helper()
	dsl, err := os.ReadFile("../../fga/testdata/stress_model.fga")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := transformer.TransformDSLToJSON(string(dsl))
	if err != nil {
		t.Fatal(err)
	}
	var model openfga.AuthorizationModel
	if err := json.Unmarshal([]byte(raw), &model); err != nil {
		t.Fatal(err)
	}
	return fga.ParseModel(&model)
}

// TestGraphLoadRevealsSelectedCard: a large model opens with its first card
// selected, so the viewport must start where that card is, not on the empty
// canvas above it.
func TestGraphLoadRevealsSelectedCard(t *testing.T) {
	m := newTestModel()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	m, _ = m.Update(modelLoadedMsg{modelID: "stress", graph: stressGraph(t)})
	mm := m.(Model)
	b, ok := mm.cardBounds(mm.graphSel)
	if !ok {
		t.Fatal("no selected card")
	}
	if b.Y < mm.graphVP.Height() {
		t.Fatalf("fixture no longer puts the first card (%s, y=%d) below the fold", b.Type, b.Y)
	}
	if title := b.Y + 1; title < mm.graphVP.YOffset() || title >= mm.graphVP.YOffset()+mm.graphVP.Height() {
		t.Fatalf("title row %d of %s outside viewport [%d,%d)", title, b.Type, mm.graphVP.YOffset(), mm.graphVP.YOffset()+mm.graphVP.Height())
	}
	if b.X < mm.graphVP.XOffset() || b.X >= mm.graphVP.XOffset()+mm.graphVP.Width() {
		t.Fatalf("card %s at x=%d outside viewport columns from %d", b.Type, b.X, mm.graphVP.XOffset())
	}
}

// TestGraphLargeModelFocusesSelectedCardsLines: a large model opens drawing
// only the selected card's lines; `a` brings every line back and again.
func TestGraphLargeModelFocusesSelectedCardsLines(t *testing.T) {
	m := loadedFocused(largeGraph())
	arrows := func(m tea.Model) int {
		mm := m.(Model)
		out, _ := mm.graph.RenderDiagramLayout(mm.graphOpts())
		return strings.Count(ansi.Strip(out), "▸")
	}
	if !m.(Model).graphFocusEdges {
		t.Fatal("large model should open showing only the selected card's lines")
	}
	focused := arrows(m)

	m, _ = m.Update(key("a"))
	if m.(Model).graphFocusEdges {
		t.Fatal("a should show every line")
	}
	if all := arrows(m); all <= focused {
		t.Fatalf("all-lines render has %d arrowheads, focused %d", all, focused)
	}
	if !strings.Contains(ansi.Strip(m.(Model).viewString()), "a all lines") {
		t.Error("footer should advertise the a key")
	}

	m, _ = m.Update(key("a"))
	if !m.(Model).graphFocusEdges {
		t.Fatal("a again should go back to the selected card's lines")
	}
}

func TestGraphSmallModelDrawsAllLines(t *testing.T) {
	m := loadedFocused(sampleGraph())
	if m.(Model).graphFocusEdges {
		t.Fatal("small models should draw every line")
	}
	m, _ = m.Update(key("a")) // nothing selected: no-op
	if m.(Model).graphFocusEdges {
		t.Fatal("a with no selection should do nothing")
	}
}

// TestGraphHorizontalWheelPans: a touchpad's sideways swipe arrives as wheel
// left/right and pans the diagram instead of scrolling it down.
func TestGraphHorizontalWheelPans(t *testing.T) {
	m := loadedFocused(stressGraph(t)).(Model)
	m.graphVP.SetXOffset(graphColStep)
	y := m.graphTarget

	got, _ := m.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelRight})
	mm := got.(Model)
	if mm.graphVP.XOffset() != 2*graphColStep {
		t.Fatalf("wheel right: x offset %d, want %d", mm.graphVP.XOffset(), 2*graphColStep)
	}
	got, _ = mm.handleWheel(tea.MouseWheelMsg{Button: tea.MouseWheelLeft})
	mm = got.(Model)
	if mm.graphVP.XOffset() != graphColStep {
		t.Fatalf("wheel left: x offset %d, want %d", mm.graphVP.XOffset(), graphColStep)
	}
	if mm.graphTarget != y {
		t.Fatalf("horizontal wheel scrolled vertically: target %v, want %v", mm.graphTarget, y)
	}
}

// TestGraphLegendStaysOnScreen: the colour legend is pinned above the
// diagram, so it is still there after scrolling all the way down.
func TestGraphLegendStaysOnScreen(t *testing.T) {
	m := loadedFocused(stressGraph(t)).(Model)
	m.graphVP.SetYOffset(m.graphMaxOffset())
	if m.graphVP.YOffset() == 0 {
		t.Fatal("fixture should be taller than the viewport")
	}
	view := ansi.Strip(m.viewString())
	for _, want := range []string{"inherited (tuple-to-userset)", "costly"} {
		if !strings.Contains(view, want) {
			t.Errorf("legend %q scrolled out of view", want)
		}
	}
}

// TestGraphExpandAllKeepsSelectedCardInColumns: E widens the columns left of
// the selected card, so it must pan to keep that card on screen.
func TestGraphExpandAllKeepsSelectedCardInColumns(t *testing.T) {
	m := loadedFocused(largeGraph())
	m, _ = m.Update(key("shift+tab")) // last card, rightmost column
	m, _ = m.Update(key("E"))
	mm := m.(Model)
	b, ok := mm.cardBounds(mm.graphSel)
	if !ok {
		t.Fatal("selected card has no bounds")
	}
	if x := mm.graphVP.XOffset(); b.X < x || b.X >= x+mm.graphVP.Width() {
		t.Fatalf("card %s at x=%d outside viewport columns [%d,%d)", b.Type, b.X, x, x+mm.graphVP.Width())
	}
}

// TestGraphTabBackToVisibleCardCancelsScroll: tab to an offscreen card starts
// a scroll; shift+tab back to the still-visible card must not keep scrolling
// toward the card it left.
func TestGraphTabBackToVisibleCardCancelsScroll(t *testing.T) {
	m := loadedFocused(stressGraph(t))
	for i := 0; i < 40 && !m.(Model).graphAnimating; i++ {
		m, _ = m.Update(key("tab"))
	}
	if !m.(Model).graphAnimating {
		t.Skip("no tab scrolled the viewport")
	}
	m, _ = m.Update(key("shift+tab"))
	mm := m.(Model)
	if mm.graphAnimating || mm.graphTarget != float64(mm.graphVP.YOffset()) {
		t.Fatalf("still scrolling: animating=%v target=%v offset=%d", mm.graphAnimating, mm.graphTarget, mm.graphVP.YOffset())
	}
}

// TestGraphLayoutUsesPinnedLegendHeight: when the legend wraps onto more
// lines, the layout must use the viewport height left below it, so no
// wrapped card is taller than the viewport.
func TestGraphLayoutUsesPinnedLegendHeight(t *testing.T) {
	m := loadedFocused(stressGraph(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 240, Height: 30})
	m, _ = m.Update(key("E"))
	mm := m.(Model)
	wide := mm.graphHeaderHeight()
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	mm = m.(Model)
	if mm.graphHeaderHeight() <= wide {
		t.Fatalf("legend should wrap onto more lines: %d at 240 cols, %d at 60", wide, mm.graphHeaderHeight())
	}
	for _, b := range mm.graphBounds {
		if b.H > mm.graphVP.Height() {
			t.Fatalf("card %s is %d lines, viewport %d", b.Type, b.H, mm.graphVP.Height())
		}
	}
}

// TestGraphToggleCardKeepsTitleRowWhenLegendChanges: collapsing a card when
// all are expanded brings back the expand hint, which can wrap the pinned
// legend onto another line; the selected title must stay on its screen row.
func TestGraphToggleCardKeepsTitleRowWhenLegendChanges(t *testing.T) {
	screenRow := func(mm Model) int {
		b, _ := mm.cardBounds(mm.graphSel)
		return mm.graphHeaderHeight() + b.Y - mm.graphVP.YOffset()
	}
	// Find a pane width where the returning hint wraps the legend.
	for width := 60; width <= 240; width++ {
		m := loadedFocused(largeGraph())
		m, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: 14})
		m, _ = m.Update(key("E"))
		for range 6 {
			m, _ = m.Update(key("tab"))
		}
		mm := m.(Model)
		// Keep the card away from the scroll limits so its row can be kept.
		b, _ := mm.cardBounds(mm.graphSel)
		if b.Y < 2 || mm.graphMaxOffset()-b.Y < 2 {
			continue
		}
		mm.snapGraphTo(b.Y - 1)
		before, legend := screenRow(mm), mm.graphHeaderHeight()
		got, _ := mm.Update(key("enter"))
		mm = got.(Model)
		if mm.graphHeaderHeight() == legend {
			continue
		}
		if after := screenRow(mm); after != before {
			t.Fatalf("width %d: title moved from screen row %d to %d", width, before, after)
		}
		return
	}
	t.Fatal("no width changes the legend height; fixture no longer exercises the case")
}

// TestGraphClickVisibleCardCancelsScroll: clicking a card on screen while a
// tab scroll is still running must stop the scroll toward the card left.
func TestGraphClickVisibleCardCancelsScroll(t *testing.T) {
	m := loadedFocused(stressGraph(t))
	for i := 0; i < 40 && !m.(Model).graphAnimating; i++ {
		m, _ = m.Update(key("tab"))
	}
	mm := m.(Model)
	if !mm.graphAnimating {
		t.Skip("no tab scrolled the viewport")
	}
	bx, by := mm.sh.MainBodyOrigin()
	top := mm.graphVP.YOffset()
	for _, b := range mm.graphBounds {
		title := b.Y + 1
		if b.Type == mm.graphSel || title < top || title >= top+mm.graphVP.Height() || b.X < mm.graphVP.XOffset() || b.X+1 >= mm.graphVP.XOffset()+mm.graphVP.Width() {
			continue
		}
		m, _ = m.Update(tea.MouseClickMsg{X: bx + b.X + 1 - mm.graphVP.XOffset(), Y: by + mm.graphHeaderHeight() + title - top, Button: tea.MouseLeft})
		mm = m.(Model)
		if mm.graphSel != b.Type {
			t.Fatalf("click should select %s, selected %s", b.Type, mm.graphSel)
		}
		if mm.graphAnimating || mm.graphTarget != float64(mm.graphVP.YOffset()) {
			t.Fatalf("still scrolling: animating=%v target=%v offset=%d", mm.graphAnimating, mm.graphTarget, mm.graphVP.YOffset())
		}
		return
	}
	t.Skip("no other card visible")
}

// TestGraphWeightedRoundTripRevealsSelectedCard: back from the weighted view,
// the diagram must open on its selected card, as it does on load.
func TestGraphWeightedRoundTripRevealsSelectedCard(t *testing.T) {
	m := loadedFocused(stressGraph(t))
	m, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	m, _ = m.Update(key("v"))
	m, _ = m.Update(key("v"))
	mm := m.(Model)
	b, ok := mm.cardBounds(mm.graphSel)
	if !ok {
		t.Fatal("selected card has no bounds")
	}
	if b.Y < mm.graphVP.Height() {
		t.Fatalf("fixture no longer puts the first card (%s, y=%d) below the fold", b.Type, b.Y)
	}
	if title := b.Y + 1; title < mm.graphVP.YOffset() || title >= mm.graphVP.YOffset()+mm.graphVP.Height() {
		t.Fatalf("title row %d of %s outside viewport [%d,%d)", title, b.Type, mm.graphVP.YOffset(), mm.graphVP.YOffset()+mm.graphVP.Height())
	}
}
