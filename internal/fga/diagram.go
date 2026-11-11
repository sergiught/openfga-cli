package fga

import (
	"image/color"
	"maps"
	"slices"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// RenderDiagram draws the authorization model as a node-link diagram: one
// rounded card per object type (a colored header plus its relations) laid out
// left→right by dependency depth, with edges routed orthogonally around the
// cards (never through them) and colored by resolution kind. The result is a
// wide, multi-line string meant to be shown inside a scrollable/pannable
// viewport.
func (g Graph) RenderDiagram() string {
	return g.RenderDiagramWith(DiagramOptions{Expanded: ExpandAll(g)})
}

// DiagramOptions tunes how RenderDiagramWith draws the model.
type DiagramOptions struct {
	Expanded       map[string]bool // types drawn with full rows; nil/empty = all compact
	Selected       string          // type whose border is highlighted; "" none
	ViewportHeight int             // wrap cards taller than this; 0 = never wrap
	// FocusEdges draws only the edges into or out of Selected, so a large
	// model reads as one card's neighbourhood. Cards stay where they would be
	// with every edge drawn. Ignored when Selected is "".
	FocusEdges bool
}

// ExpandAll returns an Expanded set naming every type of g.
func ExpandAll(g Graph) map[string]bool {
	m := make(map[string]bool, len(g.Types))
	for _, t := range g.Types {
		m[t.Name] = true
	}
	return m
}

// RenderDiagramWith is RenderDiagram with per-type expansion, selection
// highlighting and wrapping of tall cards controlled by opts.
func (g Graph) RenderDiagramWith(opts DiagramOptions) string {
	out, _ := g.RenderDiagramLayout(opts)
	return out
}

// CardBounds locates one type's card in the string RenderDiagramLayout returns.
// X is a column and Y a line index, both from the start of that string, so Y
// includes the header lines.
type CardBounds struct {
	Type       string
	X, Y, W, H int
}

// RenderDiagramLayout is RenderDiagramWith that also reports where each type's
// card sits, sorted by X then Y (layout order).
func (g Graph) RenderDiagramLayout(opts DiagramOptions) (string, []CardBounds) {
	return g.RenderDiagramCached(nil, opts)
}

// DiagramCache keeps the most recent layout and edge routing so a render that
// changes only DiagramOptions.Selected skips both, the costly part of drawing
// a large model. The zero value is ready to use; it is not safe for
// concurrent use.
type DiagramCache struct {
	types    []TypeNode // identifies the graph the cached layout belongs to
	expanded map[string]bool
	height   int
	focus    string // Selected when the routing drew only its edges, else ""
	routed   *routedDiagram
}

// routedDiagram is a laid-out diagram with its edges drawn but no cards yet.
type routedDiagram struct {
	boxes  []*nodeBox
	shown  []DiagramEdge // the edges drawn
	edges  *canvas
	header string
	bounds []CardBounds
}

// RenderDiagramCached is RenderDiagramLayout reusing cache's layout when only
// the selection changed since the last call. A nil cache always re-routes.
func (g Graph) RenderDiagramCached(cache *DiagramCache, opts DiagramOptions) (string, []CardBounds) {
	if len(g.Types) == 0 {
		return style.Faint.Render("no authorization model in this store"), nil
	}

	var r *routedDiagram
	if cache != nil && cache.matches(g, opts) {
		r = cache.routed
	} else {
		r = g.routeDiagram(opts)
		if cache != nil {
			*cache = DiagramCache{types: g.Types, expanded: maps.Clone(opts.Expanded), height: opts.ViewportHeight, focus: focusOf(opts), routed: r}
		}
	}

	c := r.edges.clone()
	for _, b := range r.boxes {
		drawBox(c, b, b.typ == opts.Selected)
	}
	drawPorts(c, r.boxes, r.shown)
	return r.header + "\n\n" + c.String(), slices.Clone(r.bounds)
}

func (dc *DiagramCache) matches(g Graph, opts DiagramOptions) bool {
	if dc.routed == nil || len(dc.types) != len(g.Types) || &dc.types[0] != &g.Types[0] || dc.height != opts.ViewportHeight || dc.focus != focusOf(opts) {
		return false
	}
	for _, t := range g.Types {
		if dc.expanded[t.Name] != opts.Expanded[t.Name] {
			return false
		}
	}
	return true
}

// focusOf returns the type whose edges alone are drawn, or "" for all edges.
func focusOf(opts DiagramOptions) string {
	if opts.FocusEdges {
		return opts.Selected
	}
	return ""
}

// routeDiagram lays out the cards and routes the edges between them: every
// part of a render that does not depend on the selection (beyond which edges
// FocusEdges keeps).
func (g Graph) routeDiagram(opts DiagramOptions) *routedDiagram {
	boxes := g.buildBoxes(opts)
	// Layout always sees every edge, so cards keep their place as the
	// focused selection moves.
	edges := g.diagramEdges()
	layoutBoxes(boxes, edges)

	shown := edges
	if focus := focusOf(opts); focus != "" {
		shown = nil
		for _, e := range edges {
			if e.From == focus || e.To == focus {
				shown = append(shown, e)
			}
		}
	}

	w, h := canvasBounds(boxes)
	c := newCanvas(w, h)
	routeEdges(c, boxes, shown)

	allExpanded := true
	for _, t := range g.Types {
		if len(t.Relations) > 0 && !opts.Expanded[t.Name] {
			allExpanded = false
			break
		}
	}
	header := g.diagramHeader(allExpanded, focusOf(opts))
	headerLines := strings.Count(header, "\n") + 2
	bounds := make([]CardBounds, 0, len(boxes))
	for _, b := range boxes {
		bounds = append(bounds, CardBounds{Type: b.typ, X: b.x, Y: b.y + headerLines, W: b.w, H: b.h})
	}
	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].X != bounds[j].X {
			return bounds[i].X < bounds[j].X
		}
		return bounds[i].Y < bounds[j].Y
	})
	return &routedDiagram{boxes: boxes, shown: shown, edges: c, header: header, bounds: bounds}
}

func (g Graph) diagramHeader(allExpanded bool, focus string) string {
	parts := []string{
		colorRune('─', style.Green) + " " + style.Faint.Render("direct"),
		colorRune('─', style.Amber) + " " + style.Faint.Render("inherited (tuple-to-userset)"),
		style.Faint.Render("↑↓←→ pan"),
	}
	if !allExpanded {
		parts = append(parts, style.Faint.Render("tab/enter expand"))
	}
	if focus != "" {
		parts = append(parts, style.Faint.Render("lines of "+style.SanitizeTerminal(focus)+" only · a all"))
	}
	legend := strings.Join(parts, "    ")
	return style.Subtitle.Render("schema "+style.SanitizeTerminal(g.SchemaVersion)) + "    " + legend + "\n" + weightLegend()
}

// --- node boxes ---

type scell struct {
	r    rune
	fg   color.Color
	bg   color.Color
	bold bool
}

type nodeBox struct {
	typ    string
	title  []scell
	rows   [][]scell
	rels   []string    // relation names, parallel to rows
	cols   [][][]scell // rows arranged into interior columns
	colW   []int
	rowY   map[string]int // relation name -> absolute canvas row; title keyed by ""
	innerW int
	w, h   int
	x, y   int
	col    int
	order  int // position within its column
}

const (
	hgap   = 10 // horizontal space between columns
	vgap   = 3  // vertical space between stacked boxes (edge lanes)
	margin = 3  // free border around the whole canvas for detours
)

func (g Graph) buildBoxes(opts DiagramOptions) []*nodeBox {
	boxes := make([]*nodeBox, 0, len(g.Types))
	for _, t := range g.Types {
		b := &nodeBox{typ: t.Name}
		b.title = styledRunes(t.Name, style.OnAccent)
		for i := range b.title {
			b.title[i].bold = true
		}
		for _, r := range t.Relations {
			if opts.Expanded[t.Name] {
				b.rows = append(b.rows, relationRow(r))
			} else {
				b.rows = append(b.rows, compactRow(r))
			}
			b.rels = append(b.rels, r.Name)
		}

		// Arrange rows into interior columns so a tall card fits the viewport.
		ncols := 1
		if maxRows := opts.ViewportHeight - 4; maxRows >= 1 && len(b.rows) > maxRows {
			ncols = (len(b.rows) + maxRows - 1) / maxRows
		}
		perCol := len(b.rows)
		if ncols > 1 {
			perCol = (len(b.rows) + ncols - 1) / ncols
		}
		for i := 0; i < len(b.rows); i += max(perCol, 1) {
			b.cols = append(b.cols, b.rows[i:min(i+perCol, len(b.rows))])
		}
		b.colW = make([]int, len(b.cols))
		b.innerW = 0
		for i, col := range b.cols {
			for _, row := range col {
				b.colW[i] = max(b.colW[i], len(row))
			}
			b.innerW += b.colW[i]
		}
		if len(b.cols) > 1 {
			b.innerW += 2 * (len(b.cols) - 1)
		}
		// Cards fit their widest content; the pannable viewport absorbs the
		// extra width so nothing is truncated.
		b.innerW = max(b.innerW, len(b.title))
		b.w = b.innerW + 4 // borders + one space of padding each side
		b.h = 3            // top border + title + bottom border
		if len(b.rows) > 0 {
			b.h += 1 + perCol // separator + one line per relation row
		}
		boxes = append(boxes, b)
	}
	return boxes
}

// compactRow renders a relation as its heat glyph and name only.
func compactRow(r Relation) []scell {
	gr, gc := r.heatGlyph()
	cells := []scell{{r: gr, fg: gc}, {r: ' '}}
	return append(cells, styledRunes(r.Name, style.Cyan)...)
}

// fillRowIndex records the absolute canvas row of the title and of every
// relation; b.y must already be known.
func fillRowIndex(b *nodeBox) {
	b.rowY = map[string]int{"": b.y + 1}
	i := 0
	for _, col := range b.cols {
		for j := range col {
			b.rowY[b.rels[i]] = b.y + 3 + j
			i++
		}
	}
}

// relationRow renders one relation line: the relation name followed by compact,
// colored markers for each of its resolution edges.
func relationRow(r Relation) []scell {
	gr, gc := r.heatGlyph()
	cells := []scell{{r: gr, fg: gc}, {r: ' '}}
	cells = append(cells, styledRunes(r.Name, style.Cyan)...)
	for _, e := range r.Edges {
		cells = append(cells, scell{r: ' '})
		switch e.Kind {
		case "direct":
			cells = append(cells, scell{r: '←', fg: style.Green})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		case "computed":
			cells = append(cells, scell{r: '=', fg: style.Cyan})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		case "ttu":
			cells = append(cells, scell{r: '⇡', fg: style.Amber})
			cells = append(cells, styledRunes(e.Label, style.Fg)...)
		}
	}
	return cells
}

func styledRunes(s string, fg color.Color) []scell {
	s = style.SanitizeTerminal(s)
	rs := []rune(s)
	out := make([]scell, len(rs))
	for i, r := range rs {
		out[i] = scell{r: r, fg: fg}
	}
	return out
}

// --- layout ---

func layoutBoxes(boxes []*nodeBox, edges []DiagramEdge) {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}

	// Dependency depth (longest path along From→To), cycle-guarded.
	memo := map[string]int{}
	var depthOf func(t string, stack map[string]bool) int
	depthOf = func(t string, stack map[string]bool) int {
		if d, ok := memo[t]; ok {
			return d
		}
		if stack[t] {
			return 0
		}
		stack[t] = true
		best := 0
		for _, to := range adj[t] {
			if d := depthOf(to, stack) + 1; d > best {
				best = d
			}
		}
		stack[t] = false
		memo[t] = best
		return best
	}

	maxDepth := 0
	for _, b := range boxes {
		if d := depthOf(b.typ, map[string]bool{}); d > maxDepth {
			maxDepth = d
		}
	}
	for _, b := range boxes {
		b.col = maxDepth - depthOf(b.typ, map[string]bool{})
	}

	cols := map[int][]*nodeBox{}
	maxCol := 0
	for _, b := range boxes {
		b.order = len(cols[b.col])
		cols[b.col] = append(cols[b.col], b)
		if b.col > maxCol {
			maxCol = b.col
		}
	}

	orderColumns(cols, maxCol, edges)

	// Column x positions.
	colX := make([]int, maxCol+1)
	x := margin
	for c := 0; c <= maxCol; c++ {
		colX[c] = x
		cw := 0
		for _, b := range cols[c] {
			if b.w > cw {
				cw = b.w
			}
		}
		x += cw + hgap
	}

	// Vertically center each column.
	canvasH := 0
	for c := 0; c <= maxCol; c++ {
		if ch := columnHeight(cols[c]); ch > canvasH {
			canvasH = ch
		}
	}
	for c := 0; c <= maxCol; c++ {
		y := margin + (canvasH-columnHeight(cols[c]))/2
		for _, b := range cols[c] {
			b.x = colX[c]
			b.y = y
			y += b.h + vgap
			fillRowIndex(b)
		}
	}
}

// orderColumns reduces edge crossings with a few barycenter sweeps, ordering
// each column by the average position of its neighbors in the prior column.
func orderColumns(cols map[int][]*nodeBox, maxCol int, edges []DiagramEdge) {
	pos := map[string]int{}
	refresh := func() {
		for c := 0; c <= maxCol; c++ {
			for i, b := range cols[c] {
				b.order = i
				pos[b.typ] = i
			}
		}
	}
	refresh()

	preds := map[string][]string{} // box -> neighbors in the column to its left
	succs := map[string][]string{}
	colOf := map[string]int{}
	for c := 0; c <= maxCol; c++ {
		for _, b := range cols[c] {
			colOf[b.typ] = c
		}
	}
	for _, e := range edges {
		// Edges run From (smaller col) → To (larger col) in the common case.
		if colOf[e.From] < colOf[e.To] {
			succs[e.From] = append(succs[e.From], e.To)
			preds[e.To] = append(preds[e.To], e.From)
		} else if colOf[e.To] < colOf[e.From] {
			succs[e.To] = append(succs[e.To], e.From)
			preds[e.From] = append(preds[e.From], e.To)
		}
	}

	bary := func(neighbors []string) float64 {
		if len(neighbors) == 0 {
			return -1
		}
		sum := 0
		for _, n := range neighbors {
			sum += pos[n]
		}
		return float64(sum) / float64(len(neighbors))
	}

	for sweep := 0; sweep < 4; sweep++ {
		forward := sweep%2 == 0
		for step := 0; step <= maxCol; step++ {
			c := step
			if !forward {
				c = maxCol - step
			}
			nbr := preds
			if !forward {
				nbr = succs
			}
			list := cols[c]
			sort.SliceStable(list, func(i, j int) bool {
				bi, bj := bary(nbr[list[i].typ]), bary(nbr[list[j].typ])
				if bi < 0 || bj < 0 {
					return false // keep relative order for unconstrained nodes
				}
				return bi < bj
			})
			refresh()
		}
	}
}

func columnHeight(boxes []*nodeBox) int {
	h := 0
	for i, b := range boxes {
		h += b.h
		if i < len(boxes)-1 {
			h += vgap
		}
	}
	return h
}

func canvasBounds(boxes []*nodeBox) (w, h int) {
	for _, b := range boxes {
		if r := b.x + b.w; r > w {
			w = r
		}
		if bottom := b.y + b.h; bottom > h {
			h = bottom
		}
	}
	return w + margin, h + margin
}

// --- canvas ---

type canvas struct {
	w, h  int
	cells [][]scell
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, cells: make([][]scell, h)}
	for y := range c.cells {
		row := make([]scell, w)
		for x := range row {
			row[x] = scell{r: ' '}
		}
		c.cells[y] = row
	}
	return c
}

func (c *canvas) clone() *canvas {
	out := &canvas{w: c.w, h: c.h, cells: make([][]scell, c.h)}
	for y, row := range c.cells {
		out.cells[y] = slices.Clone(row)
	}
	return out
}

func (c *canvas) set(x, y int, s scell) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	c.cells[y][x] = s
}

func (c *canvas) at(x, y int) rune {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return ' '
	}
	return c.cells[y][x].r
}

func (c *canvas) String() string {
	var b strings.Builder
	for y := 0; y < c.h; y++ {
		row := c.cells[y]
		end := c.w
		for end > 0 && row[end-1].r == ' ' && row[end-1].bg == nil {
			end--
		}
		x := 0
		for x < end {
			s := row[x]
			j := x
			var seg []rune
			for j < end && row[j].fg == s.fg && row[j].bg == s.bg && row[j].bold == s.bold {
				seg = append(seg, row[j].r)
				j++
			}
			st := lipgloss.NewStyle()
			if s.fg != nil {
				st = st.Foreground(s.fg)
			}
			if s.bg != nil {
				st = st.Background(s.bg)
			}
			if s.bold {
				st = st.Bold(true)
			}
			if s.fg == nil && s.bg == nil && !s.bold {
				b.WriteString(string(seg))
			} else {
				b.WriteString(st.Render(string(seg)))
			}
			x = j
		}
		if y < c.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// --- box drawing ---

func drawBox(c *canvas, b *nodeBox, selected bool) {
	bc := style.Subtle
	if selected {
		bc = style.Accent
	}
	left, right := b.x, b.x+b.w-1
	top := b.y
	innerStart := b.x + 2

	c.set(left, top, scell{r: '╭', fg: bc})
	c.set(right, top, scell{r: '╮', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, top, scell{r: '─', fg: bc})
	}

	// Title row with a filled accent header bar.
	ty := top + 1
	c.set(left, ty, scell{r: '│', fg: bc})
	c.set(right, ty, scell{r: '│', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, ty, scell{r: ' ', bg: style.Primary})
	}
	for i := 0; i < b.innerW; i++ {
		s := scell{r: ' ', fg: style.OnAccent, bg: style.Primary, bold: true}
		if i < len(b.title) {
			s.r = b.title[i].r
		}
		c.set(innerStart+i, ty, s)
	}

	if len(b.rows) > 0 {
		sep := ty + 1
		c.set(left, sep, scell{r: '├', fg: bc})
		c.set(right, sep, scell{r: '┤', fg: bc})
		for x := left + 1; x < right; x++ {
			c.set(x, sep, scell{r: '─', fg: bc})
		}
		for y := sep + 1; y < b.y+b.h-1; y++ {
			c.set(left, y, scell{r: '│', fg: bc})
			c.set(right, y, scell{r: '│', fg: bc})
			for i := 0; i < b.innerW; i++ {
				c.set(innerStart+i, y, scell{r: ' '})
			}
		}
		off := 0
		for ci, col := range b.cols {
			for j, row := range col {
				for i, s := range row {
					c.set(innerStart+off+i, sep+1+j, s)
				}
			}
			off += b.colW[ci] + 2
		}
	}

	bottom := b.y + b.h - 1
	c.set(left, bottom, scell{r: '╰', fg: bc})
	c.set(right, bottom, scell{r: '╯', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, bottom, scell{r: '─', fg: bc})
	}
}

// drawPorts marks the box borders where edges connect, so connection points are
// obvious after the boxes are painted over the routed lines.
func drawPorts(c *canvas, boxes []*nodeBox, edges []DiagramEdge) {
	byType := indexBoxes(boxes)
	for _, e := range routableEdges(edges, byType) {
		src, dst := byType[e.From], byType[e.To]
		if src == nil || dst == nil {
			continue
		}
		col := edgeColor(e.Kind)
		sy := sourcePortRow(src, e)
		ty := targetPortRow(dst, e)
		c.set(src.x+src.w-1, sy, scell{r: '├', fg: col})
		c.set(dst.x, ty, scell{r: '┤', fg: col})
	}
}

// --- edge routing (A* around the boxes) ---

func routeEdges(c *canvas, boxes []*nodeBox, edges []DiagramEdge) {
	grid := newRouteGrid(c, boxes)
	search := newAStarScratch(c.w * c.h)
	byType := indexBoxes(boxes)
	ids := make(map[string]int32, len(boxes))
	for i, b := range boxes {
		ids[b.typ] = int32(i + 1)
	}

	// Stable order so port assignment is deterministic. Grouping by target
	// (To) first lets edges that converge on the same node bundle onto a shared
	// trunk instead of fanning into parallel lanes.
	sorted := routableEdges(edges, byType)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].To != sorted[j].To {
			return sorted[i].To < sorted[j].To
		}
		return sorted[i].From < sorted[j].From
	})

	// The two gutter columns left of every card are reserved: the arrow column
	// (x-1) holds nothing but arrowheads, and the trunk lane (x-2) only carries
	// edges bound for that card. Through traffic to other cards routes further
	// out (hgap leaves eight free columns), so a card's bundle reads as one
	// trunk with arrows branching off and never crosses another bundle.
	for _, b := range boxes {
		for y := b.y; y < b.y+b.h && y < c.h; y++ {
			if b.x-1 >= 0 {
				grid.blocked[y*c.w+b.x-1] = true
			}
			if b.x-2 >= 0 {
				grid.lane[y*c.w+b.x-2] = ids[b.typ]
			}
		}
	}

	for _, e := range sorted {
		src, dst := byType[e.From], byType[e.To]
		if src == nil || dst == nil || dst.x < 2 {
			continue
		}
		start := [2]int{src.x + src.w, sourcePortRow(src, e)} // gutter cell right of source
		ty := targetPortRow(dst, e)
		if oob(c, dst.x-1, ty) {
			continue
		}
		// Phase one: find the cheapest way onto the target's trunk lane.
		// Phase two: ride the lane to the port row and step into the arrow
		// cell. Every edge into a card therefore shares the one trunk.
		path := routeAStar(grid, search, ids[dst.typ], start, dst.x-2, dst.y, dst.y+dst.h-1)
		if path == nil {
			continue
		}
		for y := path[len(path)-1][1]; y != ty; {
			y += sign(ty - y)
			path = append(path, [2]int{dst.x - 2, y})
		}
		path = append(path, [2]int{dst.x - 1, ty})
		paintPath(c, path, grid.usedBy, ids[dst.typ], edgeColor(e.Kind))
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// routeGrid is the routing state over the canvas, flattened row-major
// (cell = y*w + x). Types are identified by a nonzero id; 0 means none.
type routeGrid struct {
	w, h    int
	blocked []bool  // card cells and arrow columns
	lane    []int32 // trunk-lane owner: only edges into that type may enter
	usedBy  []int32 // destination type of the edge already drawn there
}

func newRouteGrid(c *canvas, boxes []*nodeBox) *routeGrid {
	g := &routeGrid{
		w: c.w, h: c.h,
		blocked: make([]bool, c.w*c.h),
		lane:    make([]int32, c.w*c.h),
		usedBy:  make([]int32, c.w*c.h),
	}
	for _, b := range boxes {
		for y := max(0, b.y); y < b.y+b.h && y < c.h; y++ {
			for x := max(0, b.x); x < b.x+b.w && x < c.w; x++ {
				g.blocked[y*c.w+x] = true
			}
		}
	}
	return g
}

// passable reports whether an edge into dst may enter (x, y).
func (g *routeGrid) passable(x, y int, dst int32) bool {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return false
	}
	i := y*g.w + x
	return !g.blocked[i] && (g.lane[i] == 0 || g.lane[i] == dst)
}

// sourcePortRow is the row an edge leaves its source box from: the source
// relation's row, else the title row.
func sourcePortRow(b *nodeBox, e DiagramEdge) int {
	if y, ok := b.rowY[e.Via]; ok && e.Via != "" {
		return y
	}
	return b.rowY[""]
}

// targetPortRow is the row an edge arrives on: the target relation's row when
// the edge names one, else the title row.
func targetPortRow(b *nodeBox, e DiagramEdge) int {
	if y, ok := b.rowY[e.ToRelation]; ok && e.ToRelation != "" {
		return y
	}
	return b.rowY[""]
}

// diagramEdges is g.Edges with each direct edge that routableEdges would drop
// (its relation is the tupleset of a ttu edge to the same type) moved to
// another relation of From that assigns the same type directly. ParseModel
// keeps one direct edge per target, from the first relation that assigns it,
// so a tupleset sorting first would otherwise hide every other relation's
// direct assignment of that type. g.Edges, the CLI's JSON, stays aggregated.
func (g Graph) diagramEdges() []DiagramEdge {
	type key struct{ from, to, via string }
	ttu := map[key]bool{}
	for _, e := range g.Edges {
		if e.Kind == "ttu" {
			ttu[key{e.From, e.To, e.Via}] = true
		}
	}
	out := slices.Clone(g.Edges)
	for i, e := range out {
		if e.Kind != "direct" || !ttu[key{e.From, e.To, e.Via}] {
			continue
		}
		if via, ok := g.directAssigner(e, func(rel string) bool { return ttu[key{e.From, e.To, rel}] }); ok {
			out[i].Via = via
		}
	}
	return out
}

// directAssigner finds a relation of e.From, other than one skip rejects,
// that directly assigns e.To (as e.ToRelation's userset when set).
func (g Graph) directAssigner(e DiagramEdge, skip func(rel string) bool) (string, bool) {
	for _, t := range g.Types {
		if t.Name != e.From {
			continue
		}
		for _, r := range t.Relations {
			if skip(r.Name) {
				continue
			}
			for _, re := range r.Edges {
				toRel := ""
				if i := strings.Index(re.Label, "#"); i >= 0 {
					toRel = re.Label[i+1:]
				}
				if re.Kind == "direct" && typePart(re.Label) == e.To && toRel == e.ToRelation {
					return r.Name, true
				}
			}
		}
	}
	return "", false
}

// routableEdges drops edges to types absent from the layout and direct edges
// made redundant by a ttu edge over the same tupleset. Edges into relation-less
// leaf types (such as user) are kept and land on the leaf card's title row.
func routableEdges(edges []DiagramEdge, byType map[string]*nodeBox) []DiagramEdge {
	type key struct{ from, to, via string }
	ttu := map[key]bool{}
	for _, e := range edges {
		if e.Kind == "ttu" {
			ttu[key{e.From, e.To, e.Via}] = true
		}
	}
	out := make([]DiagramEdge, 0, len(edges))
	for _, e := range edges {
		if byType[e.To] == nil {
			continue
		}
		if e.Kind == "direct" && ttu[key{e.From, e.To, e.Via}] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// dirs: 0=E 1=W 2=S 3=N
var dirDX = [4]int{1, -1, 0, 0}
var dirDY = [4]int{0, 0, 1, -1}

func opposite(d int) int {
	switch d {
	case 0:
		return 1
	case 1:
		return 0
	case 2:
		return 3
	default:
		return 2
	}
}

type aStarNode struct {
	x, y, dir int
	g, f      int
}

// bucketQueue is a monotone priority queue for A*: costs are small integers
// and the heuristic is consistent (every step costs at least 1 and moves the
// Manhattan estimate by at most 1), so popped f values never decrease and a
// bucket per f gives O(1) push and pop. Within a bucket the newest state pops
// first, which favours the deeper of equally promising states.
type bucketQueue struct {
	buckets [][]aStarNode
	cur, n  int
}

func (q *bucketQueue) reset() {
	for i := range q.buckets {
		q.buckets[i] = q.buckets[i][:0]
	}
	q.cur, q.n = 0, 0
}

func (q *bucketQueue) push(n aStarNode) {
	for n.f >= len(q.buckets) {
		q.buckets = append(q.buckets, nil)
	}
	if n.f < q.cur { // cannot happen with a consistent heuristic; stay safe
		q.cur = n.f
	}
	q.buckets[n.f] = append(q.buckets[n.f], n)
	q.n++
}

func (q *bucketQueue) pop() aStarNode {
	for len(q.buckets[q.cur]) == 0 {
		q.cur++
	}
	b := q.buckets[q.cur]
	n := b[len(b)-1]
	q.buckets[q.cur] = b[:len(b)-1]
	q.n--
	return n
}

// aStarScratch holds the per-search state, indexed by (cell, direction) and
// reused across every edge's search. A state belongs to the current search
// only when its stamp matches gen, so starting a search costs no clearing.
type aStarScratch struct {
	gen    uint32
	stamp  []uint32
	closed []uint32
	best   []int
	parent []int // -1 for the start state
	open   bucketQueue
	// dist[cell] is the fewest steps from cell to the current target's trunk
	// lane through free cells, the obstacle-aware part of the A* estimate.
	// It depends only on static obstacles, so it is computed once per target.
	dist     []int32
	distFor  int32
	frontier []int
}

func newAStarScratch(cells int) *aStarScratch {
	return &aStarScratch{
		stamp:  make([]uint32, cells*4),
		closed: make([]uint32, cells*4),
		best:   make([]int, cells*4),
		parent: make([]int, cells*4),
		dist:   make([]int32, cells),
	}
}

const (
	stepCost     = 2
	siblingCost  = 1 // cell already carrying an edge into the same card
	turnPenalty  = 10
	reusePenalty = 6
)

// fillDist runs a breadth-first search back from dstType's trunk lane over the
// cells an edge into dstType may use, recording each cell's step count.
func (s *aStarScratch) fillDist(g *routeGrid, dst int32, laneX, y0, y1 int) {
	s.distFor = dst
	for i := range s.dist {
		s.dist[i] = -1
	}
	q := s.frontier[:0]
	for y := max(0, y0); y <= y1 && y < g.h; y++ {
		if laneX >= 0 && laneX < g.w {
			s.dist[y*g.w+laneX] = 0
			q = append(q, y*g.w+laneX)
		}
	}
	for i := 0; i < len(q); i++ {
		cell := q[i]
		x, y := cell%g.w, cell/g.w
		for d := 0; d < 4; d++ {
			nx, ny := x+dirDX[d], y+dirDY[d]
			if !g.passable(nx, ny, dst) {
				continue
			}
			if n := ny*g.w + nx; s.dist[n] < 0 {
				s.dist[n] = s.dist[cell] + 1
				q = append(q, n)
			}
		}
	}
	s.frontier = q
}

// minTurns is a lower bound on the turns a path heading dir from (x, y) needs
// to reach column laneX between rows y0 and y1, so the A* estimate can charge
// turnPenalty for them and stay admissible.
func minTurns(x, y, dir, laneX, y0, y1 int) int {
	h, v := -1, -1 // direction still required on each axis, -1 when none
	switch {
	case laneX > x:
		h = 0
	case laneX < x:
		h = 1
	}
	switch {
	case y < y0:
		v = 2
	case y > y1:
		v = 3
	}
	switch {
	case h < 0 && v < 0:
		return 0
	case h >= 0 && v >= 0:
		if dir == h || dir == v {
			return 1
		}
		return 2
	}
	need := max(h, v)
	switch {
	case dir == need:
		return 0
	case dir == opposite(need):
		return 2
	default:
		return 1
	}
}

// routeAStar finds an orthogonal path from start onto the trunk lane at column
// laneX between rows laneY0 and laneY1, avoiding blocked cells and lanes
// reserved for other destination types. It prefers straight runs (turn
// penalty) and avoids already-drawn edge cells (reuse penalty) so parallel
// edges fan out into separate channels.
func routeAStar(g *routeGrid, s *aStarScratch, dst int32, start [2]int, laneX, laneY0, laneY1 int) [][2]int {
	if start[0] < 0 || start[1] < 0 || start[0] >= g.w || start[1] >= g.h || laneX < 0 || laneX >= g.w || laneY0 < 0 || laneY0 >= g.h {
		return nil
	}
	if s.distFor != dst {
		s.fillDist(g, dst, laneX, laneY0, laneY1)
	}
	if s.dist[start[1]*g.w+start[0]] < 0 {
		return nil // walled off from the lane
	}
	// Admissible and consistent: every remaining step costs at least
	// siblingCost and every remaining turn turnPenalty.
	h := func(x, y, dir int) int {
		return siblingCost*int(s.dist[y*g.w+x]) + turnPenalty*minTurns(x, y, dir, laneX, laneY0, laneY1)
	}
	key := func(x, y, dir int) int { return (y*g.w+x)*4 + dir }
	s.gen++
	s.open.reset()

	startDir := 0 // bias toward exiting east
	sk := key(start[0], start[1], startDir)
	s.stamp[sk], s.best[sk], s.parent[sk] = s.gen, 0, -1
	s.open.push(aStarNode{x: start[0], y: start[1], dir: startDir, g: 0, f: h(start[0], start[1], startDir)})

	goal := -1
	for s.open.n > 0 {
		cur := s.open.pop()
		ck := key(cur.x, cur.y, cur.dir)
		if cur.g > s.best[ck] || s.closed[ck] == s.gen {
			continue
		}
		s.closed[ck] = s.gen
		if cur.x == laneX && cur.y >= laneY0 && cur.y <= laneY1 {
			goal = ck
			break
		}
		for d := 0; d < 4; d++ {
			if d == opposite(cur.dir) {
				continue
			}
			nx, ny := cur.x+dirDX[d], cur.y+dirDY[d]
			// dist < 0 also covers every impassable cell: the BFS that
			// filled it uses the same passability rule.
			if !g.passable(nx, ny, dst) || s.dist[ny*g.w+nx] < 0 {
				continue
			}
			step := stepCost
			if by := g.usedBy[ny*g.w+nx]; by != 0 {
				if by == dst {
					// A sibling edge heading to the same card already passes
					// here; riding its feeder is cheaper than a fresh cell so
					// siblings merge, but not free, so an edge joins the trunk
					// lane where the feeder first meets it instead of riding
					// on and branching into the lane a second time.
					step = siblingCost
				} else {
					step += reusePenalty
				}
			}
			ng := cur.g + step
			if d != cur.dir {
				ng += turnPenalty
			}
			nk := key(nx, ny, d)
			if s.stamp[nk] != s.gen || (ng < s.best[nk] && s.closed[nk] != s.gen) {
				s.stamp[nk], s.best[nk], s.parent[nk] = s.gen, ng, ck
				s.open.push(aStarNode{x: nx, y: ny, dir: d, g: ng, f: ng + h(nx, ny, d)})
			}
		}
	}
	if goal < 0 {
		return nil
	}
	var path [][2]int
	for k := goal; k >= 0; k = s.parent[k] {
		cell := k / 4
		path = append(path, [2]int{cell % g.w, cell / g.w})
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// paintPath stamps a routed path with rounded corners, crossing glyphs, and a
// terminal arrowhead. Where the path shares a cell with an edge already drawn,
// it unions the two glyphs so a trunk ridden by a sibling edge reads as one
// line branching and an unrelated crossing keeps both lines intact.
func paintPath(c *canvas, path [][2]int, usedBy []int32, dst int32, col color.Color) {
	dirOf := func(a, b [2]int) int {
		switch {
		case b[0] > a[0]:
			return 0
		case b[0] < a[0]:
			return 1
		case b[1] > a[1]:
			return 2
		default:
			return 3
		}
	}
	for i, p := range path {
		var r rune
		switch {
		case i == len(path)-1:
			in := dirOf(path[i-1], p)
			r = arrowFor(in)
		case i == 0:
			// The first gutter cell continues the source port to its left, so
			// a path that leaves vertically starts with a corner, not a bar.
			out := dirOf(p, path[i+1])
			r = glyphOfArms(armW | armOfDir(out))
		default:
			in := dirOf(path[i-1], p)
			out := dirOf(p, path[i+1])
			r = glyphFor(in, out)
		}
		// Overlapping edges, bundled or merely crossing, union their arms so
		// neither line is cut: a sibling trunk becomes a T-junction, a plain
		// crossing becomes ┼, and a corner landing on a trunk keeps the trunk.
		// An arrowhead already on the cell is kept; a line never erases it.
		if cur := c.at(p[0], p[1]); isArrowhead(cur) && i != len(path)-1 {
			r = cur
		} else {
			r = mergeGlyph(cur, r)
		}
		usedBy[p[1]*c.w+p[0]] = dst
		c.set(p[0], p[1], scell{r: r, fg: col})
	}
}

// Line-glyph arms, used to union overlapping edge segments into junctions.
const (
	armN = 1 << iota
	armS
	armE
	armW
)

func armsOf(r rune) (int, bool) {
	switch r {
	case '─':
		return armE | armW, true
	case '│':
		return armN | armS, true
	case '╭':
		return armS | armE, true
	case '╮':
		return armS | armW, true
	case '╰':
		return armN | armE, true
	case '╯':
		return armN | armW, true
	case '├':
		return armN | armS | armE, true
	case '┤':
		return armN | armS | armW, true
	case '┬':
		return armS | armE | armW, true
	case '┴':
		return armN | armE | armW, true
	case '┼':
		return armN | armS | armE | armW, true
	}
	return 0, false
}

func glyphOfArms(a int) rune {
	switch a {
	case armE | armW:
		return '─'
	case armN | armS:
		return '│'
	case armS | armE:
		return '╭'
	case armS | armW:
		return '╮'
	case armN | armE:
		return '╰'
	case armN | armW:
		return '╯'
	case armN | armS | armE:
		return '├'
	case armN | armS | armW:
		return '┤'
	case armS | armE | armW:
		return '┬'
	case armN | armE | armW:
		return '┴'
	case armN | armS | armE | armW:
		return '┼'
	}
	return 0
}

// mergeGlyph unions the arms of two line glyphs into a single junction, falling
// back to the incoming glyph when either side is not a plain line (e.g. an
// arrowhead terminal keeps its shape).
func mergeGlyph(cur, next rune) rune {
	ca, ok1 := armsOf(cur)
	na, ok2 := armsOf(next)
	if !ok1 || !ok2 {
		return next
	}
	if g := glyphOfArms(ca | na); g != 0 {
		return g
	}
	return next
}

func lineFor(dir int) rune {
	if dir == 0 || dir == 1 {
		return '─'
	}
	return '│'
}

func glyphFor(in, out int) rune {
	if in == out {
		return lineFor(out)
	}
	switch {
	case (in == 0 && out == 2) || (in == 3 && out == 1):
		return '╮'
	case (in == 0 && out == 3) || (in == 2 && out == 1):
		return '╯'
	case (in == 1 && out == 2) || (in == 3 && out == 0):
		return '╭'
	case (in == 1 && out == 3) || (in == 2 && out == 0):
		return '╰'
	}
	return lineFor(out)
}

// armOfDir maps a routing direction (0=E 1=W 2=S 3=N) to its glyph arm.
func armOfDir(d int) int {
	return [4]int{armE, armW, armS, armN}[d]
}

func isArrowhead(r rune) bool {
	return r == '▸' || r == '◂' || r == '▾' || r == '▴'
}

func arrowFor(dir int) rune {
	switch dir {
	case 0:
		return '▸'
	case 1:
		return '◂'
	case 2:
		return '▾'
	default:
		return '▴'
	}
}

// --- helpers ---

func indexBoxes(boxes []*nodeBox) map[string]*nodeBox {
	m := map[string]*nodeBox{}
	for _, b := range boxes {
		m[b.typ] = b
	}
	return m
}

func edgeColor(kind string) color.Color {
	if kind == "ttu" {
		return style.Amber
	}
	return style.Green
}

func colorRune(r rune, fg color.Color) string {
	return lipgloss.NewStyle().Foreground(fg).Render(string(r))
}

func oob(c *canvas, x, y int) bool { return x < 0 || y < 0 || x >= c.w || y >= c.h }
