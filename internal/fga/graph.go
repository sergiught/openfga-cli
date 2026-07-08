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
	Kind string
	// Label is a human-readable description of the edge target.
	Label string
}

// Relation is a single relation on a type with its resolution edges.
type Relation struct {
	Name  string
	Edges []RelationEdge
}

// TypeNode is one object type in the model with its relations.
type TypeNode struct {
	Name      string
	Relations []Relation
}

// DiagramEdge is a directed dependency between two object types: type From has
// a relation that can be satisfied by users (or usersets) of type To. Kind is
// "direct" or "ttu" (tuple-to-userset / inherited). Via names the relation the
// dependency flows through (the relation on From for direct edges, the tupleset
// relation for ttu edges).
type DiagramEdge struct {
	From string
	To   string
	Kind string
	Via  string
}

// Graph is the parsed, render-ready view of an authorization model.
type Graph struct {
	SchemaVersion string
	Types         []TypeNode
	// Edges are the inter-type dependencies used to draw the node-link diagram.
	Edges []DiagramEdge
}

// ParseModel converts an authorization model into a Graph by interpreting the
// untyped relation rewrite rules and the directly-related-user-types metadata.
func ParseModel(m *openfga.AuthorizationModel) Graph {
	g := Graph{SchemaVersion: m.SchemaVersion}
	seen := map[string]bool{}
	for _, td := range m.TypeDefinitions {
		node := TypeNode{Name: td.Type}

		// Collect relation names and sort for stable output.
		names := make([]string, 0, len(td.Relations))
		for name := range td.Relations {
			names = append(names, name)
		}
		sort.Strings(names)

		direct := directTypesByRelation(td.Metadata)

		for _, name := range names {
			rel := Relation{Name: name}
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
					if _, via, ok := splitTTU(e.Label); ok {
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

// splitTTU parses a tuple-to-userset edge label of the form "target from via".
func splitTTU(label string) (target, via string, ok bool) {
	parts := strings.SplitN(label, " from ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// directTypesByRelation extracts metadata.relations[rel].directly_related_user_types.
func directTypesByRelation(metadata map[string]any) map[string][]string {
	out := map[string][]string{}
	relations, ok := mapField(metadata, "relations")
	if !ok {
		return out
	}
	for rel, raw := range relations {
		relMeta, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		list, ok := relMeta["directly_related_user_types"].([]any)
		if !ok {
			continue
		}
		for _, item := range list {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			t, _ := obj["type"].(string)
			if t == "" {
				continue
			}
			label := t
			if r, ok := obj["relation"].(string); ok && r != "" {
				label = t + "#" + r
			} else if _, ok := obj["wildcard"]; ok {
				label = t + ":*"
			}
			out[rel] = append(out[rel], label)
		}
	}
	return out
}

// rewriteEdges interprets a userset rewrite rule into computed/ttu edges,
// recursing through union/intersection/difference. Direct ("this") nodes are
// ignored here because they are already represented via metadata.
func rewriteEdges(rule any) []RelationEdge {
	m, ok := rule.(map[string]any)
	if !ok {
		return nil
	}
	var edges []RelationEdge

	if _, ok := m["this"]; ok {
		// direct assignment; covered by metadata
	}
	if cu, ok := mapField(m, "computedUserset"); ok {
		if r, _ := cu["relation"].(string); r != "" {
			edges = append(edges, RelationEdge{Kind: "computed", Label: r})
		}
	}
	if ttu, ok := mapField(m, "tupleToUserset"); ok {
		var via, target string
		if ts, ok := mapField(ttu, "tupleset"); ok {
			via, _ = ts["relation"].(string)
		}
		if cu, ok := mapField(ttu, "computedUserset"); ok {
			target, _ = cu["relation"].(string)
		}
		if target != "" {
			edges = append(edges, RelationEdge{Kind: "ttu", Label: fmt.Sprintf("%s from %s", target, via)})
		}
	}
	for _, op := range []string{"union", "intersection"} {
		if node, ok := mapField(m, op); ok {
			if children, ok := node["child"].([]any); ok {
				for _, child := range children {
					edges = append(edges, rewriteEdges(child)...)
				}
			}
		}
	}
	if diff, ok := mapField(m, "difference"); ok {
		if base, ok := diff["base"]; ok {
			edges = append(edges, rewriteEdges(base)...)
		}
		if sub, ok := diff["subtract"]; ok {
			for _, e := range rewriteEdges(sub) {
				e.Label = "not " + e.Label
				edges = append(edges, e)
			}
		}
	}
	return edges
}

func mapField(m map[string]any, key string) (map[string]any, bool) {
	if m == nil {
		return nil, false
	}
	v, ok := m[key].(map[string]any)
	return v, ok
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
	b.WriteString(style.Subtitle.Render("schema "+g.SchemaVersion) + "    " + legend + "\n\n")

	for ti, t := range g.Types {
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(style.Violet).Render(t.Name))
		b.WriteString("\n")
		for ri, r := range t.Relations {
			lastRel := ri == len(t.Relations)-1
			relBranch := "├─"
			relIndent := "│  "
			if lastRel {
				relBranch = "└─"
				relIndent = "   "
			}
			b.WriteString(style.Faint.Render(relBranch) + " " + style.Key.Render(r.Name) + "\n")
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
				b.WriteString(style.Faint.Render(relIndent+edgeBranch+" ") + edgeGlyph(e.Kind) + " " + style.Value.Render(e.Label) + "\n")
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
