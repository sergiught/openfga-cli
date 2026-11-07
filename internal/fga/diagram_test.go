package fga

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/style"
)

func githubModel() *openfga.AuthorizationModel {
	direct := func(types ...string) openfga.RelationMetadata {
		refs := make([]openfga.RelationReference, len(types))
		for i, t := range types {
			refs[i] = openfga.DirectType(t)
		}
		return openfga.RelationMetadata{DirectlyRelatedUserTypes: refs}
	}
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "organization", Relations: map[string]openfga.Userset{
				"member": openfga.This(),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"member": direct("user"),
			}}},
			{Type: "repo", Relations: map[string]openfga.Userset{
				"owner": openfga.This(),
				"admin": openfga.Union(openfga.This(), openfga.TupleTo("owner", "member")),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"owner": direct("organization"),
				"admin": direct("user"),
			}}},
		},
	}
}

func TestParseModelInterTypeEdges(t *testing.T) {
	g := ParseModel(githubModel())

	want := map[string]bool{
		"organization|direct|user": true, // organization#member: [user]
		"repo|direct|user":         true, // repo#admin: [user]
		"repo|direct|organization": true, // repo#owner: [organization]
		"repo|ttu|organization":    true, // repo#admin: member from owner -> organization
	}
	got := map[string]bool{}
	for _, e := range g.Edges {
		got[e.From+"|"+e.Kind+"|"+e.To] = true
		if e.From == e.To {
			t.Errorf("unexpected self-edge on %q", e.From)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("missing edge %q; got %v", k, got)
		}
	}
}

func TestRenderDiagramShowsTypesAndEdges(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagram())

	for _, typ := range []string{"user", "organization", "repo"} {
		if !strings.Contains(out, typ) {
			t.Errorf("diagram missing type %q", typ)
		}
	}
	if !strings.ContainsRune(out, '╭') {
		t.Error("diagram should draw rounded node cards")
	}
	if !strings.ContainsRune(out, '▸') {
		t.Error("diagram should draw directed edges")
	}
}

func TestRenderDiagramEmpty(t *testing.T) {
	g := Graph{}
	if got := g.RenderDiagram(); !strings.Contains(got, "no authorization model") {
		t.Errorf("empty diagram = %q", got)
	}
}

func TestRenderDiagramHeat(t *testing.T) {
	g := ParseModel(weightModel())
	out := ansi.Strip(g.RenderDiagram())

	for _, want := range []string{"cheap", "moderate", "costly", "recursive"} {
		if !strings.Contains(out, want) {
			t.Errorf("diagram missing heat legend %q", want)
		}
	}
	if !strings.ContainsRune(out, '∞') {
		t.Error("diagram should show ∞ for recursive relations")
	}
}

// cardLines returns the stripped rows of the card whose title row holds
// "│ <name>", from the "╭" row through the "╰" row, cropped to the card's own
// columns so neighbouring cards and edges do not leak in. top is the index of
// the "╭" row in out.
func cardLines(t *testing.T, out, name string) (lines []string, top int) {
	t.Helper()
	all := strings.Split(out, "\n")
	for i, l := range all {
		idx := strings.Index(l, "│ "+name)
		if p := strings.Index(l, "┤ "+name); idx < 0 && p >= 0 {
			idx = p
		}
		if idx < 0 {
			continue
		}
		top = i - 1
		x := len([]rune(l[:idx]))
		topRunes := []rune(all[top])
		end := x
		for end < len(topRunes) && topRunes[end] != '╮' {
			end++
		}
		for j := top; j < len(all); j++ {
			r := []rune(all[j])
			lines = append(lines, string(r[x:min(end+1, len(r))]))
			if strings.Contains(lines[len(lines)-1], "╰") {
				return lines, top
			}
		}
	}
	t.Fatalf("card %q not found", name)
	return nil, 0
}

func TestRenderDiagramWithCompactRowsKeepNames(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{}))
	for _, name := range []string{"member", "owner", "admin"} {
		if !strings.Contains(out, name) {
			t.Errorf("compact diagram missing relation %q", name)
		}
	}
	if strings.Contains(out, "←user") || strings.Contains(out, "from owner") {
		t.Errorf("compact diagram must not show resolution detail:\n%s", out)
	}
}

func TestRenderDiagramWithAllExpandedEqualsRenderDiagram(t *testing.T) {
	g := ParseModel(githubModel())
	if a, b := g.RenderDiagram(), g.RenderDiagramWith(DiagramOptions{Expanded: ExpandAll(g)}); a != b {
		t.Errorf("all-expanded render differs from RenderDiagram")
	}
}

func TestSelectedCardBorderUsesAccent(t *testing.T) {
	g := ParseModel(githubModel())
	plain := g.RenderDiagramWith(DiagramOptions{})
	sel := g.RenderDiagramWith(DiagramOptions{Selected: "repo"})
	if plain == sel {
		t.Fatal("selecting repo must change the render")
	}
	if ansi.Strip(plain) != ansi.Strip(sel) {
		t.Fatal("selection must only change colors, not glyphs")
	}
	// Locate the repo card's top-left corner in the stripped text, then
	// compare the colored renders from that line on.
	stripped := strings.Split(ansi.Strip(sel), "\n")
	_, top := cardLines(t, ansi.Strip(sel), "repo")
	idx := strings.Index(stripped[top], "╭")
	if idx < 0 {
		t.Fatal("no corner above repo title")
	}
	col := len([]rune(stripped[top][:idx]))
	cellOf := func(out string) string {
		line := strings.Split(out, "\n")[top]
		return ansi.Cut(line, col, col+1)
	}
	if cellOf(plain) == cellOf(sel) {
		t.Errorf("repo corner cell identical in plain and selected: %q", cellOf(sel))
	}
}

func TestRenderDiagramWithMixedForms(t *testing.T) {
	g := ParseModel(githubModel())
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{Expanded: map[string]bool{"repo": true}}))
	if !strings.Contains(out, "from owner") {
		t.Error("expanded repo should show ttu detail")
	}
	card, _ := cardLines(t, out, "organization")
	found := false
	for _, l := range card {
		if strings.Contains(l, "member") {
			found = true
			if strings.Contains(l, "←") {
				t.Errorf("compact organization member row carries detail: %q", l)
			}
		}
	}
	if !found {
		t.Error("organization member row not found")
	}
}

func TestCompactCardAtLeastTitleWide(t *testing.T) {
	g := ParseModel(&openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{{
			Type:      "a_very_long_type_name",
			Relations: map[string]openfga.Userset{"r": openfga.This()},
			Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"r": {DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("a_very_long_type_name")}},
			}},
		}},
	})
	card, _ := cardLines(t, ansi.Strip(g.RenderDiagramWith(DiagramOptions{})), "a_very_long_type_name")
	if w := len([]rune(card[0])); w != len("a_very_long_type_name")+4 {
		t.Errorf("card width %d, want %d", w, len("a_very_long_type_name")+4)
	}
}

func TestRenderDiagramWithWrapsTallCards(t *testing.T) {
	g := ParseModel(largeModel(t))
	out := ansi.Strip(g.RenderDiagramWith(DiagramOptions{ViewportHeight: 20}))
	lines := strings.Split(out, "\n")
	if len(lines) > 3+2*margin+2*20+vgap {
		t.Errorf("wrapped canvas too tall: %d lines", len(lines))
	}
	card, _ := cardLines(t, out, "res0 ")
	if len(card) > 20 {
		t.Errorf("wrapped res0 card is %d lines tall, want <= 20", len(card))
	}
	for r := 0; r < 25; r++ {
		if !strings.Contains(out, fmt.Sprintf("rel%d", r)) {
			t.Errorf("missing rel%d", r)
		}
	}
}

// Guards against pathological blowups only; BenchmarkRenderDiagramWithLargeModel
// is where real numbers live.
func TestRenderDiagramWithLargeModelIsFast(t *testing.T) {
	g := ParseModel(largeModel(t))
	start := time.Now()
	g.RenderDiagramWith(DiagramOptions{ViewportHeight: 40})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("compact render took %v", d)
	}
	start = time.Now()
	g.RenderDiagram()
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("expanded render took %v", d)
	}
}

func BenchmarkRenderDiagramWithLargeModel(b *testing.B) {
	g := ParseModel(largeModel(b))
	b.Run("compact", func(b *testing.B) {
		for range b.N {
			g.RenderDiagramWith(DiagramOptions{ViewportHeight: 40})
		}
	})
	b.Run("expanded", func(b *testing.B) {
		for range b.N {
			g.RenderDiagram()
		}
	})
}

// rowOf returns the line index (in the stripped render) of the named relation
// inside the named card.
func rowOf(t *testing.T, out, card, relation string) int {
	t.Helper()
	lines, top := cardLines(t, out, card)
	for i := 2; i < len(lines); i++ {
		if strings.Contains(lines[i], relation) && !strings.Contains(lines[i], "╰") {
			return top + i
		}
	}
	t.Fatalf("relation %q not found in card %q", relation, card)
	return -1
}

// cardLeft returns the rune column of the card's left border.
func cardLeft(t *testing.T, out, card string) int {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		idx := strings.Index(l, "│ "+card)
		if p := strings.Index(l, "┤ "+card); idx < 0 && p >= 0 {
			idx = p
		}
		if idx >= 0 {
			return len([]rune(l[:idx]))
		}
	}
	t.Fatalf("card %q not found", card)
	return -1
}

// cardRight returns the rune column of the card's right border.
func cardRight(t *testing.T, out, card string) int {
	t.Helper()
	lines, _ := cardLines(t, out, card)
	return cardLeft(t, out, card) + len([]rune(lines[0])) - 1
}

func runeAt(lines []string, y, x int) rune {
	r := []rune(lines[y])
	if x >= len(r) {
		return 0
	}
	return r[x]
}

func portModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "group", Relations: map[string]openfga.Userset{"member": openfga.This()},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"member": {DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("user")}},
				}}},
			{Type: "doc", Relations: map[string]openfga.Userset{"viewer": openfga.This()},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"viewer": {DirectlyRelatedUserTypes: []openfga.RelationReference{
						openfga.DirectType("user"), {Type: "group", Relation: "member"},
					}},
				}}},
		},
	}
}

func TestPortsAttachToViaAndTargetRelation(t *testing.T) {
	g := ParseModel(portModel())
	out := ansi.Strip(g.RenderDiagram())
	lines := strings.Split(out, "\n")
	vy := rowOf(t, out, "doc", "viewer")
	if r := runeAt(lines, vy, cardRight(t, out, "doc")); r != '├' {
		t.Errorf("source port should be on viewer row, got %q", r)
	}
	my := rowOf(t, out, "group", "member")
	if r := runeAt(lines, my, cardLeft(t, out, "group")); r != '┤' {
		t.Errorf("target port should be on member row, got %q", r)
	}
}

func TestLeafEdgesLandOnTitleRow(t *testing.T) {
	g := ParseModel(portModel())
	out := ansi.Strip(g.RenderDiagram())
	lines := strings.Split(out, "\n")
	x := cardLeft(t, out, "user")
	ul, top := cardLines(t, out, "user")
	if r := runeAt(lines, top+1, x); r != '┤' {
		t.Errorf("user title row should carry a target port, got %q", r)
	}
	for y := top + 2; y < top+len(ul); y++ {
		if r := runeAt(lines, y, x); r != '│' && r != '╰' {
			t.Errorf("user left border row %d holds %q", y, r)
		}
	}
}

func ttuModel() *openfga.AuthorizationModel {
	return &openfga.AuthorizationModel{
		SchemaVersion: "1.1",
		TypeDefinitions: []openfga.TypeDefinition{
			{Type: "user"},
			{Type: "folder", Relations: map[string]openfga.Userset{"viewer": openfga.This()},
				Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
					"viewer": {DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("user")}},
				}}},
			{Type: "doc", Relations: map[string]openfga.Userset{
				"parent": openfga.This(),
				"viewer": openfga.TupleTo("parent", "viewer"),
			}, Metadata: &openfga.Metadata{Relations: map[string]openfga.RelationMetadata{
				"parent": {DirectlyRelatedUserTypes: []openfga.RelationReference{openfga.DirectType("folder")}},
			}}},
		},
	}
}

func TestTTUEdgeLandsOnTitleAndSuppressesDirect(t *testing.T) {
	g := ParseModel(ttuModel())
	plain := ansi.Strip(g.RenderDiagram())
	lines := strings.Split(plain, "\n")

	rx := cardRight(t, plain, "doc")
	_, dtop := cardLines(t, plain, "doc")
	dl, _ := cardLines(t, plain, "doc")
	var ports []int
	for y := dtop; y < dtop+len(dl); y++ {
		if runeAt(lines, y, rx) == '├' {
			ports = append(ports, y)
		}
	}
	if len(ports) != 1 || ports[0] != rowOf(t, plain, "doc", "parent") {
		t.Errorf("want exactly one source port on parent row, got %v", ports)
	}

	lx := cardLeft(t, plain, "folder")
	fl, ftop := cardLines(t, plain, "folder")
	for y := ftop; y < ftop+len(fl); y++ {
		r := runeAt(lines, y, lx)
		if y == ftop+1 && r != '┤' {
			t.Errorf("folder title row should hold target port, got %q", r)
		}
		if y != ftop+1 && r == '┤' {
			t.Errorf("unexpected target port on folder row %d", y)
		}
	}

	// Colour: the doc source port must be amber (ttu), not green. Cells of the
	// same colour are merged into one escape run, so match the run prefix. Cut
	// the line at doc's right border so folder's own green edge (to user) on
	// the same canvas row is not mistaken for a doc port.
	cell := ansi.Cut(strings.Split(g.RenderDiagram(), "\n")[ports[0]], 0, rx+2)
	amber := strings.TrimSuffix(colorRune('x', style.Amber), "x\x1b[m")
	green := strings.TrimSuffix(colorRune('x', style.Green), "x\x1b[m")
	if !strings.Contains(cell, amber+"├") {
		t.Errorf("source port should be amber: %q", cell)
	}
	if strings.Contains(cell, green+"├") {
		t.Errorf("no green port expected on doc row: %q", cell)
	}
}

func TestMissingViaFallsBackToTitle(t *testing.T) {
	g := Graph{
		Types: []TypeNode{
			{Name: "a", Relations: []Relation{{Name: "x"}}},
			{Name: "b", Relations: []Relation{{Name: "y"}}},
		},
		Edges: []DiagramEdge{{From: "a", To: "b", Kind: "ttu", Via: "nope"}},
	}
	out := ansi.Strip(g.RenderDiagram())
	lines := strings.Split(out, "\n")
	_, top := cardLines(t, out, "a")
	if r := runeAt(lines, top+1, cardRight(t, out, "a")); r != '├' {
		t.Errorf("port should sit on a's title row, got %q", r)
	}
}

func TestUsersetAndTypeEdgesLandSeparately(t *testing.T) {
	m := portModel()
	m.TypeDefinitions[2].Metadata.Relations["viewer"] = openfga.RelationMetadata{
		DirectlyRelatedUserTypes: []openfga.RelationReference{
			openfga.DirectType("group"), {Type: "group", Relation: "member"},
		},
	}
	out := ansi.Strip(ParseModel(m).RenderDiagram())
	lines := strings.Split(out, "\n")
	x := cardLeft(t, out, "group")
	_, top := cardLines(t, out, "group")
	if r := runeAt(lines, top+1, x); r != '┤' {
		t.Errorf("title row should hold a port, got %q", r)
	}
	if r := runeAt(lines, rowOf(t, out, "group", "member"), x); r != '┤' {
		t.Errorf("member row should hold a port, got %q", r)
	}
}

func TestRenderDiagramLayoutBoundsEncloseTitles(t *testing.T) {
	g := ParseModel(githubModel())
	out, bounds := g.RenderDiagramLayout(DiagramOptions{})
	lines := strings.Split(ansi.Strip(out), "\n")
	if len(bounds) != len(g.Types) {
		t.Fatalf("want %d bounds, got %d", len(g.Types), len(bounds))
	}
	for _, b := range bounds {
		title := string([]rune(lines[b.Y+1])[b.X : b.X+b.W])
		if !strings.Contains(title, b.Type) {
			t.Errorf("bounds for %s do not enclose its title: %q", b.Type, title)
		}
		if r := []rune(lines[b.Y])[b.X]; r != '╭' {
			t.Errorf("%s top-left is %q", b.Type, r)
		}
		if r := []rune(lines[b.Y+b.H-1])[b.X+b.W-1]; r != '╯' {
			t.Errorf("%s bottom-right is %q", b.Type, r)
		}
	}
}

func TestRenderDiagramLayoutOrder(t *testing.T) {
	g := ParseModel(githubModel())
	_, bounds := g.RenderDiagramLayout(DiagramOptions{})
	for i := 1; i < len(bounds); i++ {
		p, c := bounds[i-1], bounds[i]
		if p.X > c.X || (p.X == c.X && p.Y > c.Y) {
			t.Errorf("bounds out of order: %+v before %+v", p, c)
		}
	}
}

func TestDiagramHeaderHintOnlyWhenCompact(t *testing.T) {
	g := ParseModel(githubModel())
	if !strings.Contains(ansi.Strip(g.RenderDiagramWith(DiagramOptions{})), "tab/enter expand") {
		t.Error("compact header lacks hint")
	}
	if strings.Contains(ansi.Strip(g.RenderDiagram()), "tab/enter expand") {
		t.Error("expanded header must not show hint")
	}
}

func TestBothTupleRelationsCarryPorts(t *testing.T) {
	plain := ansi.Strip(ParseModel(twoTupleModel()).RenderDiagram())
	lines := strings.Split(plain, "\n")
	rx := cardRight(t, plain, "doc")
	for _, rel := range []string{"parent", "owner_folder"} {
		if r := runeAt(lines, rowOf(t, plain, "doc", "● "+rel), rx); r != '├' {
			t.Errorf("%s row should carry a source port, got %q", rel, r)
		}
	}
}

// lineArms returns the connection arms of a canvas glyph, treating arrowheads
// as a line continuing in their direction.
func lineArms(r rune) (int, bool) {
	switch r {
	case '▸', '◂':
		return armE | armW, true
	case '▾', '▴':
		return armN | armS, true
	}
	return armsOf(r)
}

// brokenJoins lists every line glyph whose arm points at a neighbouring line
// glyph that has no matching arm: a trunk cut by a crossing, a corner painted
// over a junction, a port whose path leaves without a bend. Free ends (a
// neighbour that is not a line glyph) are not reported, so card borders and
// the legend do not count.
func brokenJoins(plain string) []string {
	rows := strings.Split(plain, "\n")
	grid := make([][]rune, len(rows))
	for i, r := range rows {
		grid[i] = []rune(r)
	}
	at := func(x, y int) rune {
		if y < 0 || y >= len(grid) || x < 0 || x >= len(grid[y]) {
			return ' '
		}
		return grid[y][x]
	}
	var out []string
	for y := range grid {
		for x, r := range grid[y] {
			a, ok := lineArms(r)
			if !ok {
				continue
			}
			check := func(arm, dx, dy, need int) {
				if a&arm == 0 {
					return
				}
				n := at(x+dx, y+dy)
				na, ok := lineArms(n)
				if !ok || na&need != 0 {
					return
				}
				out = append(out, fmt.Sprintf("(%d,%d) %q arm->%q", x, y, r, n))
			}
			check(armE, 1, 0, armW)
			check(armW, -1, 0, armE)
			check(armS, 0, 1, armN)
			check(armN, 0, -1, armS)
		}
	}
	return out
}

func TestDiagramLinesAreContinuous(t *testing.T) {
	g := ParseModel(largeModel(t))
	for name, opt := range map[string]DiagramOptions{
		"compact":  {ViewportHeight: 47},
		"expanded": {Expanded: ExpandAll(g), ViewportHeight: 47},
	} {
		out, _ := g.RenderDiagramLayout(opt)
		if bad := brokenJoins(ansi.Strip(out)); len(bad) > 0 {
			t.Errorf("%s: %d cut lines, first %v", name, len(bad), bad[:min(5, len(bad))])
		}
	}
}

// Every edge into a card rides one trunk in the lane two cells left of it and
// branches into the arrow column: the arrow column holds only arrowheads, the
// lane cell feeds each arrowhead, and no second trunk one cell further out
// branches into an arrow row (a ├ there is a parallel trunk; a ┼ is just
// through traffic crossing an entry).
func TestEdgesIntoCardShareOneTrunk(t *testing.T) {
	large, stacked := ParseModel(largeModel(t)), ParseModel(stackedModel(t))
	for name, tc := range map[string]struct {
		g   Graph
		opt DiagramOptions
	}{
		"large/compact":    {large, DiagramOptions{ViewportHeight: 47}},
		"large/expanded":   {large, DiagramOptions{Expanded: ExpandAll(large), ViewportHeight: 47}},
		"stacked/compact":  {stacked, DiagramOptions{ViewportHeight: 47}},
		"stacked/expanded": {stacked, DiagramOptions{Expanded: ExpandAll(stacked), ViewportHeight: 47}},
	} {
		out, cards := tc.g.RenderDiagramLayout(tc.opt)
		lines := strings.Split(ansi.Strip(out), "\n")
		arrows := 0
		for _, b := range cards {
			for y := b.Y; y < b.Y+b.H; y++ {
				switch r := runeAt(lines, y, b.X-1); r {
				case ' ', 0:
				case '▸':
					arrows++
					if arms, _ := armsOf(runeAt(lines, y, b.X-2)); arms&armE == 0 {
						t.Errorf("%s: %s row %d arrow not fed from the trunk lane", name, b.Type, y)
					}
					if arms, _ := armsOf(runeAt(lines, y, b.X-3)); arms == armN|armS|armE {
						t.Errorf("%s: %s row %d fed by a second trunk outside the lane", name, b.Type, y)
					}
				default:
					t.Errorf("%s: %s row %d arrow column holds %q", name, b.Type, y, r)
				}
			}
		}
		if arrows == 0 {
			t.Fatalf("%s: no arrowheads found", name)
		}
	}
}
