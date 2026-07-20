package fga

import (
	"fmt"
	"sort"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/style"
)

// RelationEdge describes one resolution path for a relation.
type RelationEdge struct {
	// Kind is "direct", "computed", or "ttu" (tuple-to-userset).
	Kind string `json:"kind"`
	// Label is a human-readable description of the edge target.
	Label string `json:"label"`
}

// Relation is a single relation on a type with its resolution edges.
type Relation struct {
	Name  string         `json:"name"`
	Edges []RelationEdge `json:"edges"`
	// Weight is the worst-case resolution cost (>=1); -1 when Recursive.
	Weight    int  `json:"weight"`
	Recursive bool `json:"recursive"`
}

// TypeNode is one object type in the model with its relations.
type TypeNode struct {
	Name      string     `json:"name"`
	Relations []Relation `json:"relations"`
}

// DiagramEdge is a directed dependency between two object types: type From has
// a relation that can be satisfied by users (or usersets) of type To. Kind is
// "direct" or "ttu" (tuple-to-userset / inherited). Via names the relation the
// dependency flows through (the relation on From for direct edges, the tupleset
// relation for ttu edges).
type DiagramEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Via  string `json:"via"`
}

// Graph is the parsed, render-ready view of an authorization model.
type Graph struct {
	SchemaVersion string     `json:"schema_version"`
	Types         []TypeNode `json:"types"`
	// Edges are the inter-type dependencies used to draw the node-link diagram.
	Edges []DiagramEdge `json:"edges"`
	// src is retained so the weighted-graph view can be built on demand; it is
	// never serialized.
	src *openfga.AuthorizationModel
}

// RenderWeightedDiagram draws the fully-expanded weighted graph (relation,
// operator, direct-grouping and terminal-type nodes with per-terminal-type
// weights), in the style of openfga/model-visualizer. It is built lazily so the
// text and JSON model-graph outputs, which never use it, do not pay for it.
func (g Graph) RenderWeightedDiagram() string {
	if g.src == nil {
		return style.Faint.Render("no authorization model in this store")
	}
	return buildWeightedGraph(g.src).render()
}

// ParseModel converts an authorization model into a Graph by interpreting the
// relation rewrite rules and the directly-related-user-types metadata. Slices
// are initialized (never nil) so `--json` output serializes empty collections
// as [] rather than null.
func ParseModel(m *openfga.AuthorizationModel) Graph {
	g := Graph{SchemaVersion: m.SchemaVersion, Types: []TypeNode{}, Edges: []DiagramEdge{}}
	seen := map[string]bool{}
	for _, td := range m.TypeDefinitions {
		node := TypeNode{Name: td.Type, Relations: []Relation{}}

		// Collect relation names and sort for stable output.
		names := make([]string, 0, len(td.Relations))
		for name := range td.Relations {
			names = append(names, name)
		}
		sort.Strings(names)

		direct := directTypesByRelation(td.Metadata)

		for _, name := range names {
			rel := Relation{Name: name, Edges: []RelationEdge{}}
			// Directly assignable types from metadata.
			for _, dt := range direct[name] {
				rel.Edges = append(rel.Edges, RelationEdge{Kind: "direct", Label: dt})
				// Inter-type dependency: this type points at the target type.
				addEdge(&g, &seen, DiagramEdge{From: td.Type, To: typePart(dt), Kind: "direct", Via: name})
			}
			// Computed/TTU edges from the rewrite rule.
			for _, e := range rewriteEdges(td.Relations[name]) {
				rel.Edges = append(rel.Edges, e)
				// A tuple-to-userset edge "target from via" inherits through the
				// type(s) the tupleset relation `via` points to.
				if e.Kind == "ttu" {
					if _, via, ok := SplitTTU(e.Label); ok {
						for _, parent := range direct[via] {
							addEdge(&g, &seen, DiagramEdge{From: td.Type, To: typePart(parent), Kind: "ttu", Via: via})
						}
					}
				}
			}
			node.Relations = append(node.Relations, rel)
		}
		g.Types = append(g.Types, node)
	}

	weights := computeWeights(m)
	for ti := range g.Types {
		for ri := range g.Types[ti].Relations {
			w := weights[g.Types[ti].Name+"#"+g.Types[ti].Relations[ri].Name]
			if w == weightRecursive {
				g.Types[ti].Relations[ri].Weight = weightRecursive
				g.Types[ti].Relations[ri].Recursive = true
			} else {
				g.Types[ti].Relations[ri].Weight = w
			}
		}
	}
	g.src = m
	return g
}

// addEdge appends a deduplicated inter-type edge, skipping self-references and
// empty targets.
func addEdge(g *Graph, seen *map[string]bool, e DiagramEdge) {
	if e.To == "" || e.To == e.From {
		return
	}
	key := e.From + "\x00" + e.To + "\x00" + e.Kind
	if (*seen)[key] {
		return
	}
	if *seen == nil {
		*seen = map[string]bool{}
	}
	(*seen)[key] = true
	g.Edges = append(g.Edges, e)
}

// typePart returns the object type from a directly-related-user-type label such
// as "user", "group#member", or "user:*".
func typePart(label string) string {
	if i := strings.IndexAny(label, "#:"); i >= 0 {
		return label[:i]
	}
	return label
}

// SplitTTU parses a tuple-to-userset edge label of the form "target from via".
func SplitTTU(label string) (target, via string, ok bool) {
	parts := strings.SplitN(label, " from ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// directTypesByRelation extracts each relation's directly-related user types from
// the type definition's metadata.
func directTypesByRelation(metadata *openfga.Metadata) map[string][]string {
	out := map[string][]string{}
	if metadata == nil {
		return out
	}
	for rel, relMeta := range metadata.Relations {
		for _, ref := range relMeta.DirectlyRelatedUserTypes {
			if ref.Type == "" {
				continue
			}
			label := ref.Type
			switch {
			case ref.Relation != "":
				label = ref.Type + "#" + ref.Relation
			case ref.Wildcard != nil:
				label = ref.Type + ":*"
			}
			out[rel] = append(out[rel], label)
		}
	}
	return out
}

// rewriteEdges interprets a userset rewrite rule into computed/ttu edges,
// recursing through union/intersection/difference. Direct ("this") nodes are
// ignored here because they are already represented via metadata.
func rewriteEdges(rule openfga.Userset) []RelationEdge {
	var edges []RelationEdge

	// rule.This is direct assignment; covered by metadata, ignored here.
	if cu := rule.ComputedUserset; cu != nil && cu.Relation != "" {
		edges = append(edges, RelationEdge{Kind: "computed", Label: cu.Relation})
	}
	if ttu := rule.TupleToUserset; ttu != nil {
		via := ttu.Tupleset.Relation
		target := ttu.ComputedUserset.Relation
		if target != "" {
			edges = append(edges, RelationEdge{Kind: "ttu", Label: fmt.Sprintf("%s from %s", target, via)})
		}
	}
	for _, set := range []*openfga.Usersets{rule.Union, rule.Intersection} {
		if set == nil {
			continue
		}
		for _, child := range set.Child {
			edges = append(edges, rewriteEdges(child)...)
		}
	}
	if diff := rule.Difference; diff != nil {
		edges = append(edges, rewriteEdges(diff.Base)...)
		for _, e := range rewriteEdges(diff.Subtract) {
			e.Label = "not " + e.Label
			edges = append(edges, e)
		}
	}
	return edges
}

// edgeGlyph returns a colored glyph and legend hint for an edge kind.
func edgeGlyph(kind string) string {
	switch kind {
	case "direct":
		return lipgloss.NewStyle().Foreground(style.Green).Render("←")
	case "computed":
		return lipgloss.NewStyle().Foreground(style.Cyan).Render("=")
	case "ttu":
		return lipgloss.NewStyle().Foreground(style.Amber).Render("⇡")
	default:
		return "·"
	}
}

// Render draws the graph as a colored tree with a legend. width is advisory.
func (g Graph) Render() string {
	var b strings.Builder

	legend := strings.Join([]string{
		edgeGlyph("direct") + " " + style.Faint.Render("directly assignable"),
		edgeGlyph("computed") + " " + style.Faint.Render("implied by relation"),
		edgeGlyph("ttu") + " " + style.Faint.Render("inherited (tuple-to-userset)"),
	}, "    ")
	b.WriteString(style.Subtitle.Render("schema "+style.SanitizeTerminal(g.SchemaVersion)) + "    " + legend + "\n")
	b.WriteString(weightLegend() + "\n\n")

	for ti, t := range g.Types {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Violet).Render(style.SanitizeTerminal(t.Name)))
		b.WriteString("\n")
		for ri, r := range t.Relations {
			lastRel := ri == len(t.Relations)-1
			relBranch := "├─"
			relIndent := "│  "
			if lastRel {
				relBranch = "└─"
				relIndent = "   "
			}
			glyph, color := r.heatGlyph()
			b.WriteString(style.Faint.Render(relBranch) + " " + lipgloss.NewStyle().Foreground(color).Render(string(glyph)) + " " + style.Key.Render(style.SanitizeTerminal(r.Name)) + "\n")
			if len(r.Edges) == 0 {
				b.WriteString(style.Faint.Render(relIndent+"└─ ") + style.Faint.Render("(no resolutions)") + "\n")
				continue
			}
			for ei, e := range r.Edges {
				lastEdge := ei == len(r.Edges)-1
				edgeBranch := "├─"
				if lastEdge {
					edgeBranch = "└─"
				}
				b.WriteString(style.Faint.Render(relIndent+edgeBranch+" ") + edgeGlyph(e.Kind) + " " + style.Value.Render(style.SanitizeTerminal(e.Label)) + "\n")
			}
		}
		if ti != len(g.Types)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Summary returns a one-line summary like "4 types, 11 relations".
func (g Graph) Summary() string {
	rels := 0
	for _, t := range g.Types {
		rels += len(t.Relations)
	}
	return fmt.Sprintf("%d types, %d relations", len(g.Types), rels)
}
