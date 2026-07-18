package fga

import (
	"image/color"
	"maps"
	"sort"
	"strconv"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/style"
)

// This file renders an authorization model as a fully-expanded *weighted graph*,
// in the style of openfga/model-visualizer: relation nodes, operator nodes
// (union/intersection/exclusion), logical-direct-grouping nodes, tuple-to-userset
// targets and terminal type nodes, each annotated with per-terminal-type weights
// (1..∞). It reuses the low-level canvas and glyph helpers from diagram.go, but
// lays the graph out (Sugiyama-style, with virtual routing nodes) and paints the
// edges here so long edges travel in clean lanes rather than across the boxes.

// wgKind classifies a node, mirroring model-visualizer's NodeType.
type wgKind int

const (
	wgRelation wgKind = iota // SpecificTypeAndRelation, e.g. document#viewer
	wgType                   // SpecificType, e.g. user
	wgOperator               // OperatorNode, e.g. union
	wgGrouping               // LogicalDirectGrouping, e.g. document#direct:viewer
)

// wgEdgeKind mirrors model-visualizer's EdgeType.
type wgEdgeKind int

const (
	wgDirect wgEdgeKind = iota
	wgRewrite
	wgTTU
	wgLogical
)

type wgNode struct {
	id      string
	display string // shown label (operator nodes show "union", others show id)
	kind    wgKind
	weights map[string]int // terminal type -> cost, weightRecursive for ∞
}

type wgEdge struct {
	from, to string
	kind     wgEdgeKind
}

// weightedGraph is the expanded, weight-annotated view of an authorization model.
type weightedGraph struct {
	schema string
	nodes  []*wgNode
	edges  []wgEdge
	index  map[string]*wgNode
	opSeq  int
}

// buildWeightedGraph expands a model into its weighted graph.
func buildWeightedGraph(m *openfga.AuthorizationModel) weightedGraph {
	rules := map[string]map[string]openfga.Userset{}
	directs := map[string]map[string][]string{}
	for _, td := range m.TypeDefinitions {
		rules[td.Type] = td.Relations
		directs[td.Type] = directTypesByRelation(td.Metadata)
	}

	wc := &weightCalc{rules: rules, directs: directs, state: map[string]int{}, memo: map[string]map[string]int{}}

	g := weightedGraph{schema: m.SchemaVersion, index: map[string]*wgNode{}}

	for _, td := range m.TypeDefinitions {
		if len(td.Relations) == 0 {
			// A pure subject type (user, employee) is a terminal node; it is
			// added lazily when something points at it, so skip here.
			continue
		}
		names := make([]string, 0, len(td.Relations))
		for n := range td.Relations {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, rel := range names {
			relID := td.Type + "#" + rel
			g.ensure(relID, relID, wgRelation, wc.weightsOf(td.Type, rel))
			rule := td.Relations[rel]
			if isPureThis(rule) {
				// Direct-only relation: connect straight to its assignable
				// targets (no grouping node), matching model-visualizer.
				for _, lbl := range directs[td.Type][rel] {
					g.linkDirect(relID, lbl, wc)
				}
				continue
			}
			g.expand(relID, td.Type, rel, rule, wc)
		}
	}

	return g
}

// expand connects src to the resolution of rule, creating operator / grouping /
// terminal nodes as needed.
func (g *weightedGraph) expand(src, typ, rel string, rule openfga.Userset, wc *weightCalc) {
	switch {
	case rule.This != nil:
		gid := typ + "#direct:" + rel
		g.ensure(gid, gid, wgGrouping, wc.directListWeights(typ, rel))
		g.edge(src, gid, wgLogical)
		for _, lbl := range g.directsOf(typ, rel, wc) {
			g.linkDirect(gid, lbl, wc)
		}
	case rule.ComputedUserset != nil && rule.ComputedUserset.Relation != "":
		to := typ + "#" + rule.ComputedUserset.Relation
		g.ensure(to, to, wgRelation, wc.weightsOf(typ, rule.ComputedUserset.Relation))
		g.edge(src, to, wgRewrite)
	case rule.TupleToUserset != nil && rule.TupleToUserset.ComputedUserset.Relation != "":
		via := rule.TupleToUserset.Tupleset.Relation
		target := rule.TupleToUserset.ComputedUserset.Relation
		for _, plbl := range wc.directs[typ][via] {
			pt := typePart(plbl)
			if _, ok := wc.rules[pt][target]; !ok {
				continue
			}
			to := pt + "#" + target
			g.ensure(to, to, wgRelation, wc.weightsOf(pt, target))
			g.edge(src, to, wgTTU)
		}
	case rule.Union != nil, rule.Intersection != nil, rule.Difference != nil:
		opName, children := operatorParts(rule)
		g.opSeq++
		opID := "op:" + strconv.Itoa(g.opSeq) + ":" + src
		g.ensure(opID, opName, wgOperator, wc.weightsOf2(src))
		g.edge(src, opID, wgRewrite)
		for _, child := range children {
			g.expand(opID, typ, rel, child, wc)
		}
	}
}

// linkDirect adds a DirectEdge from src to the terminal type or userset relation
// named by a directly-related-user-type label ("user", "user:*", "group#member").
func (g *weightedGraph) linkDirect(src, label string, wc *weightCalc) {
	if i := strings.IndexAny(label, "#"); i >= 0 {
		t, r := label[:i], label[i+1:]
		to := t + "#" + r
		g.ensure(to, to, wgRelation, wc.weightsOf(t, r))
		g.edge(src, to, wgDirect)
		return
	}
	t := typePart(label) // strips ":*" wildcard
	g.ensure(t, t, wgType, nil)
	g.edge(src, t, wgDirect)
}

func (g *weightedGraph) directsOf(typ, rel string, wc *weightCalc) []string {
	return wc.directs[typ][rel]
}

func (g *weightedGraph) ensure(id, display string, kind wgKind, weights map[string]int) {
	if n, ok := g.index[id]; ok {
		if n.weights == nil && weights != nil {
			n.weights = weights
		}
		return
	}
	n := &wgNode{id: id, display: display, kind: kind, weights: weights}
	g.index[id] = n
	g.nodes = append(g.nodes, n)
}

func (g *weightedGraph) edge(from, to string, kind wgEdgeKind) {
	g.edges = append(g.edges, wgEdge{from: from, to: to, kind: kind})
}

// --- weight calculation (per terminal type) ---

const wgRecursiveKey = "" // sentinel key: recursion of an as-yet-unknown terminal type

type weightCalc struct {
	rules   map[string]map[string]openfga.Userset
	directs map[string]map[string][]string
	state   map[string]int // 0 unvisited, 1 in-progress, 2 done
	memo    map[string]map[string]int
}

// weightsOf returns the per-terminal-type cost map for a relation node.
func (wc *weightCalc) weightsOf(typ, rel string) map[string]int {
	key := typ + "#" + rel
	switch wc.state[key] {
	case 2:
		return cloneW(wc.memo[key])
	case 1:
		return map[string]int{wgRecursiveKey: weightRecursive} // back-edge
	}
	rule, ok := wc.rules[typ][rel]
	if !ok {
		return map[string]int{}
	}
	wc.state[key] = 1
	m := wc.ruleWeights(typ, rel, rule)
	resolveRecursion(m)
	wc.state[key] = 2
	wc.memo[key] = m
	return cloneW(m)
}

// weightsOf2 fetches an already-computed relation's weights by its "type#rel" id
// (used for the operator node, which shares its relation's result).
func (wc *weightCalc) weightsOf2(relID string) map[string]int {
	typ, rel, ok := strings.Cut(relID, "#")
	if !ok {
		return nil
	}
	return wc.weightsOf(typ, rel)
}

// directListWeights is the weight of a relation's [...] direct list alone.
func (wc *weightCalc) directListWeights(typ, rel string) map[string]int {
	res := map[string]int{}
	for _, lbl := range wc.directs[typ][rel] {
		if i := strings.IndexAny(lbl, "#"); i >= 0 {
			mergeMaxW(res, incW(wc.weightsOf(lbl[:i], lbl[i+1:])))
		} else {
			mergeMaxW(res, map[string]int{typePart(lbl): 1})
		}
	}
	resolveRecursion(res)
	return res
}

func (wc *weightCalc) ruleWeights(typ, rel string, rule openfga.Userset) map[string]int {
	res := map[string]int{}
	if rule.This != nil {
		mergeMaxW(res, wc.directListWeights(typ, rel))
	}
	if cu := rule.ComputedUserset; cu != nil && cu.Relation != "" {
		mergeMaxW(res, wc.weightsOf(typ, cu.Relation)) // rewrite: no extra hop
	}
	if ttu := rule.TupleToUserset; ttu != nil && ttu.ComputedUserset.Relation != "" {
		via, target := ttu.Tupleset.Relation, ttu.ComputedUserset.Relation
		for _, plbl := range wc.directs[typ][via] {
			pt := typePart(plbl)
			if _, ok := wc.rules[pt][target]; ok {
				mergeMaxW(res, incW(wc.weightsOf(pt, target)))
			}
		}
	}
	for _, set := range []*openfga.Usersets{rule.Union, rule.Intersection} {
		if set == nil {
			continue
		}
		for _, child := range set.Child {
			mergeMaxW(res, wc.ruleWeights(typ, rel, child))
		}
	}
	if diff := rule.Difference; diff != nil {
		mergeMaxW(res, wc.ruleWeights(typ, rel, diff.Base))
		mergeMaxW(res, wc.ruleWeights(typ, rel, diff.Subtract))
	}
	return res
}

// resolveRecursion turns the recursion sentinel into ∞ on the node's concrete
// terminal types (so team#member = user:∞, not a bare marker).
func resolveRecursion(m map[string]int) {
	if _, rec := m[wgRecursiveKey]; !rec {
		return
	}
	concrete := false
	for k := range m {
		if k != wgRecursiveKey {
			concrete = true
			break
		}
	}
	if concrete {
		for k := range m {
			if k != wgRecursiveKey {
				m[k] = weightRecursive
			}
		}
		delete(m, wgRecursiveKey)
	}
}

func mergeMaxW(dst, src map[string]int) {
	for k, v := range src {
		if cur, ok := dst[k]; !ok {
			dst[k] = v
		} else {
			dst[k] = maxW(cur, v)
		}
	}
}

func maxW(a, b int) int {
	if a == weightRecursive || b == weightRecursive {
		return weightRecursive
	}
	if a > b {
		return a
	}
	return b
}

func incW(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	for k, v := range m {
		if v == weightRecursive {
			out[k] = weightRecursive
		} else {
			out[k] = v + 1
		}
	}
	return out
}

func cloneW(m map[string]int) map[string]int {
	out := make(map[string]int, len(m))
	maps.Copy(out, m)
	return out
}

// --- rule shape helpers ---

func isPureThis(rule openfga.Userset) bool {
	return rule.This != nil && rule.ComputedUserset == nil && rule.TupleToUserset == nil &&
		rule.Union == nil && rule.Intersection == nil && rule.Difference == nil
}

func operatorParts(rule openfga.Userset) (name string, children []openfga.Userset) {
	switch {
	case rule.Union != nil:
		return "union", rule.Union.Child
	case rule.Intersection != nil:
		return "intersection", rule.Intersection.Child
	case rule.Difference != nil:
		return "exclusion", []openfga.Userset{rule.Difference.Base, rule.Difference.Subtract}
	}
	return "operator", nil
}

// --- rendering ---
//
// Reuses diagram.go's low-level canvas, layout, A* pathfinder and glyph helpers,
// but draws boxes and routes edges with per-node-kind and per-edge-kind colors
// (the primary diagram's drawBox/routeEdges hardcode a single palette).

func (g weightedGraph) render() string {
	if len(g.nodes) == 0 {
		return style.Faint.Render("no authorization model in this store")
	}
	boxes, styles, edges := g.toBoxes()
	lay := newLayered(boxes, edges)
	lay.order()
	w, h := lay.position()
	c := newCanvas(w, h)
	marks := lay.route(c)
	for _, b := range boxes {
		drawWeightedBox(c, b, styles[b.typ])
	}
	// Ports and arrowheads sit on box borders, so stamp them after the boxes.
	for _, mk := range marks {
		c.set(mk.x, mk.y, scell{r: mk.r, fg: mk.col})
	}
	return g.header() + "\n\n" + c.String()
}

func (g weightedGraph) header() string {
	nodes := strings.Join([]string{
		colorRune('●', style.Primary) + " " + style.Faint.Render("relation"),
		colorRune('●', style.Violet) + " " + style.Faint.Render("direct group"),
		colorRune('●', style.Green) + " " + style.Faint.Render("type"),
		colorRune('▢', style.Muted) + " " + style.Faint.Render("operator"),
	}, "  ")
	edges := strings.Join([]string{
		colorRune('─', style.Green) + " " + style.Faint.Render("direct"),
		colorRune('─', style.Cyan) + " " + style.Faint.Render("rewrite"),
		colorRune('─', style.Amber) + " " + style.Faint.Render("ttu"),
		colorRune('─', style.Violet) + " " + style.Faint.Render("logical"),
	}, "  ")
	l1 := style.Subtitle.Render("schema "+style.SanitizeTerminal(g.schema)) + "    " +
		style.Faint.Render("weighted graph") + "    " + nodes
	l2 := edges + "    " + style.Faint.Render("v diagram") + "    " + style.Faint.Render("↑↓←→ pan")
	return l1 + "\n" + l2
}

// wgBoxStyle is the per-node-kind box palette. A nil head means an unfilled
// header (plain label), used to de-emphasize operator nodes.
type wgBoxStyle struct {
	border color.Color
	headBG color.Color
	headFG color.Color
	bold   bool
}

func styleFor(kind wgKind) wgBoxStyle {
	switch kind {
	case wgOperator:
		return wgBoxStyle{border: style.Subtle, headFG: style.Muted}
	case wgGrouping:
		return wgBoxStyle{border: style.Subtle, headBG: style.Violet, headFG: style.OnAccent, bold: true}
	case wgType:
		return wgBoxStyle{border: style.Subtle, headBG: style.Green, headFG: style.OnAccent, bold: true}
	default: // wgRelation
		return wgBoxStyle{border: style.Subtle, headBG: style.Primary, headFG: style.OnAccent, bold: true}
	}
}

func wgEdgeColor(kind string) color.Color {
	switch kind {
	case "rewrite":
		return style.Cyan
	case "ttu":
		return style.Amber
	case "logical":
		return style.Violet
	default: // direct
		return style.Green
	}
}

func (g weightedGraph) toBoxes() ([]*nodeBox, map[string]wgBoxStyle, []DiagramEdge) {
	boxes := make([]*nodeBox, 0, len(g.nodes))
	styles := make(map[string]wgBoxStyle, len(g.nodes))
	for _, n := range g.nodes {
		b := &nodeBox{typ: n.id}
		b.title = styledRunes(n.display, style.Fg) // fg overridden by drawWeightedBox
		// The label + color carry the node kind, so the verbose type line is
		// dropped; only the weights remain in the body (terminals have none).
		if wtext := formatWeights(n.weights); wtext != "" {
			b.rows = append(b.rows, weightRow(wtext, n.weights))
		}
		b.innerW = len(b.title)
		for _, row := range b.rows {
			if len(row) > b.innerW {
				b.innerW = len(row)
			}
		}
		b.w = b.innerW + 4
		b.h = 3
		if len(b.rows) > 0 {
			b.h += 1 + len(b.rows)
		}
		boxes = append(boxes, b)
		styles[n.id] = styleFor(n.kind)
	}
	edges := make([]DiagramEdge, 0, len(g.edges))
	for _, e := range g.edges {
		edges = append(edges, DiagramEdge{From: e.from, To: e.to, Kind: e.kind.routeKind()})
	}
	return boxes, styles, edges
}

func (k wgEdgeKind) routeKind() string {
	switch k {
	case wgRewrite:
		return "rewrite"
	case wgTTU:
		return "ttu"
	case wgLogical:
		return "logical"
	default:
		return "direct"
	}
}

// drawWeightedBox mirrors diagram.go's drawBox but takes a per-kind palette and
// supports an unfilled (plain-label) header for operator nodes.
func drawWeightedBox(c *canvas, b *nodeBox, st wgBoxStyle) {
	bc := st.border
	left, right := b.x, b.x+b.w-1
	top := b.y
	innerStart := b.x + 2

	c.set(left, top, scell{r: '╭', fg: bc})
	c.set(right, top, scell{r: '╮', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, top, scell{r: '─', fg: bc})
	}

	ty := top + 1
	c.set(left, ty, scell{r: '│', fg: bc})
	c.set(right, ty, scell{r: '│', fg: bc})
	if st.headBG != nil {
		for x := left + 1; x < right; x++ {
			c.set(x, ty, scell{r: ' ', bg: st.headBG})
		}
	}
	for i := 0; i < b.innerW; i++ {
		s := scell{r: ' ', fg: st.headFG, bg: st.headBG, bold: st.bold}
		if i < len(b.title) {
			s.r = b.title[i].r
		}
		c.set(innerStart+i, ty, s)
	}

	rowY := ty + 1
	if len(b.rows) > 0 {
		c.set(left, rowY, scell{r: '├', fg: bc})
		c.set(right, rowY, scell{r: '┤', fg: bc})
		for x := left + 1; x < right; x++ {
			c.set(x, rowY, scell{r: '─', fg: bc})
		}
		rowY++
		for _, row := range b.rows {
			c.set(left, rowY, scell{r: '│', fg: bc})
			c.set(right, rowY, scell{r: '│', fg: bc})
			for i := 0; i < b.innerW; i++ {
				s := scell{r: ' '}
				if i < len(row) {
					s = row[i]
				}
				c.set(innerStart+i, rowY, s)
			}
			rowY++
		}
	}

	bottom := b.y + b.h - 1
	c.set(left, bottom, scell{r: '╰', fg: bc})
	c.set(right, bottom, scell{r: '╯', fg: bc})
	for x := left + 1; x < right; x++ {
		c.set(x, bottom, scell{r: '─', fg: bc})
	}
}

// portMark is a glyph stamped onto a box border after the boxes are drawn: a
// source port, a target port, or a single arrowhead per target.
type portMark struct {
	x, y int
	r    rune
	col  color.Color
}

// --- layered layout (Sugiyama-style with virtual routing nodes) ---
//
// The primary diagram's A* router lets long edges span many columns, so their
// trunks run alongside boxes and every box exit crosses them (the ├┼┼ mess).
// Here each edge that spans more than one column is broken into unit-length
// segments through virtual nodes, one per intermediate column. Virtual nodes
// reserve their own row slot, so a long edge travels in a clear lane and never
// runs adjacent to a box.

type lnode struct {
	id    string   // real node id, or "" for a virtual routing node
	box   *nodeBox // nil for virtual nodes
	col   int
	order int
	x, y  int
	w, h  int
}

type eroute struct {
	nodes []*lnode // real source, virtual nodes…, real target
	kind  string
}

type layered struct {
	real   map[string]*lnode
	byCol  map[int][]*lnode
	routes []eroute
	maxCol int
	colX   []int
	colW   []int
}

func newLayered(boxes []*nodeBox, edges []DiagramEdge) *layered {
	adj := map[string][]string{}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
	}
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

	l := &layered{real: map[string]*lnode{}, byCol: map[int][]*lnode{}}
	add := func(n *lnode) {
		n.order = len(l.byCol[n.col])
		l.byCol[n.col] = append(l.byCol[n.col], n)
		if n.col > l.maxCol {
			l.maxCol = n.col
		}
	}
	for _, b := range boxes {
		n := &lnode{id: b.typ, box: b, col: maxDepth - depthOf(b.typ, map[string]bool{}), w: b.w, h: b.h}
		l.real[b.typ] = n
		add(n)
	}
	for _, e := range edges {
		a, b := l.real[e.From], l.real[e.To]
		if a == nil || b == nil {
			continue
		}
		r := eroute{kind: e.Kind, nodes: []*lnode{a}}
		if a.col != b.col {
			step := 1
			if b.col < a.col {
				step = -1
			}
			for col := a.col + step; col != b.col; col += step {
				vn := &lnode{col: col, w: 1, h: 1}
				add(vn)
				r.nodes = append(r.nodes, vn)
			}
		}
		r.nodes = append(r.nodes, b)
		l.routes = append(l.routes, r)
	}
	return l
}

// order reduces edge crossings with a few barycenter sweeps over the columns.
func (l *layered) order() {
	succ := map[*lnode][]*lnode{}
	pred := map[*lnode][]*lnode{}
	for _, r := range l.routes {
		for i := 0; i+1 < len(r.nodes); i++ {
			u, v := r.nodes[i], r.nodes[i+1]
			if u.col < v.col {
				succ[u] = append(succ[u], v)
				pred[v] = append(pred[v], u)
			} else {
				succ[v] = append(succ[v], u)
				pred[u] = append(pred[u], v)
			}
		}
	}
	pos := func() {
		for c := 0; c <= l.maxCol; c++ {
			for i, n := range l.byCol[c] {
				n.order = i
			}
		}
	}
	pos()
	bary := func(ns []*lnode) float64 {
		if len(ns) == 0 {
			return -1
		}
		sum := 0
		for _, n := range ns {
			sum += n.order
		}
		return float64(sum) / float64(len(ns))
	}
	for sweep := range 6 {
		forward := sweep%2 == 0
		for step := 0; step <= l.maxCol; step++ {
			c := step
			if !forward {
				c = l.maxCol - step
			}
			nbr := pred
			if !forward {
				nbr = succ
			}
			list := l.byCol[c]
			sort.SliceStable(list, func(i, j int) bool {
				bi, bj := bary(nbr[list[i]]), bary(nbr[list[j]])
				if bi < 0 || bj < 0 {
					return false
				}
				return bi < bj
			})
			pos()
		}
	}
}

// position assigns pixel coordinates: columns left-to-right, nodes stacked and
// vertically centred within each column, virtual nodes taking a 1-row slot.
func (l *layered) position() (int, int) {
	l.colX = make([]int, l.maxCol+1)
	l.colW = make([]int, l.maxCol+1)
	x := margin
	for c := 0; c <= l.maxCol; c++ {
		cw := 3 // minimum for a virtual-only column
		for _, n := range l.byCol[c] {
			if n.box != nil && n.w > cw {
				cw = n.w
			}
		}
		l.colX[c], l.colW[c] = x, cw
		x += cw + hgap
	}
	canvasH := 0
	colH := make([]int, l.maxCol+1)
	for c := 0; c <= l.maxCol; c++ {
		h := 0
		for i, n := range l.byCol[c] {
			h += n.h
			if i < len(l.byCol[c])-1 {
				h += vgap
			}
		}
		colH[c] = h
		if h > canvasH {
			canvasH = h
		}
	}
	maxX := 0
	for c := 0; c <= l.maxCol; c++ {
		y := margin + (canvasH-colH[c])/2
		for _, n := range l.byCol[c] {
			n.x, n.y = l.colX[c], y
			if n.box != nil {
				n.box.x, n.box.y = n.x, n.y
			} else {
				n.w = l.colW[c] // virtual node spans its column for a clean pass-through
			}
			if r := n.x + n.w; r > maxX {
				maxX = r
			}
			y += n.h + vgap
		}
	}
	return maxX + margin, canvasH + 2*margin
}

// route paints every edge as a clean orthogonal polyline through its virtual
// nodes and returns the border marks (source ports + one arrowhead per target).
func (l *layered) route(c *canvas) []portMark {
	// Assign each real source's outgoing edges a distinct exit row; bundle each
	// target's incoming edges to its title row.
	outBy := map[*lnode][]*eroute{}
	for i := range l.routes {
		r := &l.routes[i]
		src := r.nodes[0]
		outBy[src] = append(outBy[src], r)
	}
	exitRow := map[*eroute]int{}
	for src, rs := range outBy {
		if src.box == nil {
			for _, r := range rs {
				exitRow[r] = src.y
			}
			continue
		}
		// Exit on the body rows (below the title + separator); terminals with no
		// body exit from their single title row.
		lo, hi := src.y+3, src.y+src.h-2
		if src.h <= 3 {
			lo, hi = src.y+1, src.y+1
		}
		if hi < lo {
			hi = lo
		}
		sort.SliceStable(rs, func(i, j int) bool { return last(*rs[i]).order < last(*rs[j]).order })
		for i, r := range rs {
			if len(rs) == 1 {
				exitRow[r] = (lo + hi) / 2
			} else {
				exitRow[r] = lo + (i+1)*(hi-lo)/(len(rs)+1)
			}
		}
	}

	var marks []portMark
	seenArrow := map[*lnode]int{} // bitmask: 1 = right entry drawn, 2 = left entry drawn
	for i := range l.routes {
		r := &l.routes[i]
		col := wgEdgeColor(r.kind)
		src, dst := r.nodes[0], last(*r)
		entryRow := dst.y + 1

		pts := l.polyline(r, exitRow[r], entryRow)
		paintPolyline(c, pts, col)

		if dst.col >= src.col { // rightward: exit right, enter left
			marks = append(marks, portMark{src.x + src.w - 1, exitRow[r], '├', col})
			if seenArrow[dst]&1 == 0 {
				seenArrow[dst] |= 1
				marks = append(marks,
					portMark{dst.x - 1, entryRow, '▸', col},
					portMark{dst.x, entryRow, '┤', col},
				)
			}
		} else { // leftward back-edge: exit left, enter right
			marks = append(marks, portMark{src.x, exitRow[r], '┤', col})
			if seenArrow[dst]&2 == 0 {
				seenArrow[dst] |= 2
				marks = append(marks,
					portMark{dst.x + dst.w, entryRow, '◂', col},
					portMark{dst.x + dst.w - 1, entryRow, '├', col},
				)
			}
		}
	}
	return marks
}

// polyline builds the orthogonal cell path for one edge. For a rightward edge it
// exits the source's right side and enters each node from the left; for a
// leftward (back) edge — e.g. the recursion union → group#member — it mirrors
// onto the opposite sides so the line never runs back through the boxes.
func (l *layered) polyline(r *eroute, startRow, entryRow int) [][2]int {
	src, dst := r.nodes[0], last(*r)
	dir := 1
	if dst.col < src.col {
		dir = -1
	}
	pts := [][2]int{}
	x := src.x + src.w // exit right…
	if dir < 0 {
		x = src.x - 1 // …or left for a back-edge
	}
	y := startRow
	pts = append(pts, [2]int{x, y})
	push := func(nx, ny int) {
		for x != nx {
			if nx > x {
				x++
			} else {
				x--
			}
			pts = append(pts, [2]int{x, y})
		}
		for y != ny {
			if ny > y {
				y++
			} else {
				y--
			}
			pts = append(pts, [2]int{x, y})
		}
	}
	for i := 1; i < len(r.nodes); i++ {
		n := r.nodes[i]
		lastNode := i == len(r.nodes)-1
		row := n.y
		if lastNode {
			row = entryRow
		}
		var bendX, endX int
		if dir > 0 {
			bendX = n.x - 2
			if bendX <= x {
				bendX = x + 1
			}
			endX = n.x + n.w
			if lastNode {
				endX = n.x - 1
			}
		} else {
			bendX = n.x + n.w + 1
			if bendX >= x {
				bendX = x - 1
			}
			endX = n.x - 1
			if lastNode {
				endX = n.x + n.w
			}
		}
		push(bendX, y)
		push(bendX, row)
		push(endX, row)
	}
	return pts
}

func last(r eroute) *lnode { return r.nodes[len(r.nodes)-1] }

// paintPolyline stamps a cell path with line/corner glyphs. Wherever a cell
// already holds a line, the two are unioned by their arms (diagram.go's
// mergeGlyph), so trunks, T-branches, corners and crossings all resolve to the
// correct box-drawing glyph instead of one line overwriting another.
func paintPolyline(c *canvas, path [][2]int, col color.Color) {
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
		case len(path) == 1:
			r = '─'
		case i == len(path)-1:
			in := dirOf(path[i-1], p)
			r = glyphFor(in, in)
		case i == 0:
			r = lineFor(dirOf(p, path[i+1]))
		default:
			r = glyphFor(dirOf(path[i-1], p), dirOf(p, path[i+1]))
		}
		c.set(p[0], p[1], scell{r: mergeGlyph(c.at(p[0], p[1]), r), fg: col})
	}
}

// formatWeights renders "user: 2, employee: ∞" with terminal types sorted.
func formatWeights(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		name := k
		if name == wgRecursiveKey {
			name = "*"
		}
		v := "∞"
		if m[k] != weightRecursive {
			v = strconv.Itoa(m[k])
		}
		parts = append(parts, name+": "+v)
	}
	return strings.Join(parts, ", ")
}

// weightIcon marks the resolution-weight row. A scales glyph ("weight"); it is
// width-1 (like the heat dots) so it keeps the boxes aligned — a true anchor
// emoji (⚓) is double-width and would shift the borders.
const weightIcon = '⚖'

// weightRow renders the per-terminal-type weights behind a scales icon, colored
// amber when any weight is ∞ (a hot/recursive resolution) else cyan.
func weightRow(text string, m map[string]int) []scell {
	fg := style.Cyan
	for _, v := range m {
		if v == weightRecursive {
			fg = style.Amber
			break
		}
	}
	cells := []scell{{r: weightIcon, fg: fg}, {r: ' '}}
	return append(cells, styledRunes(text, fg)...)
}
