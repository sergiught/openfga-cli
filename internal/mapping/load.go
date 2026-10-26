package mapping

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"github.com/openfga/mapper"
	"github.com/openfga/mapper/language"
	"gopkg.in/yaml.v3"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// File is a mapping file read from disk: its bytes, the editor's Document, and
// the yaml.v3 tree Save merges into so comments and key order survive.
type File struct {
	Path string
	// Src is what was read with the BOM stripped and CRLF kept, so positions
	// mapper reports line up with Doc and with root.
	Src []byte
	Doc *Document
	// Editable is whether Doc represents the whole file, which is what makes
	// Save safe. Reason says why not.
	Editable bool
	Reason   string
	// Invalid is mapper's verdict on a draft: a file whose YAML parses into a
	// mapping but that mapper rejects. Only the draft loaders return such a
	// file; it is nil otherwise.
	Invalid mapper.Diagnostics

	root *yaml.Node
	crlf bool
	bom  bool
	// disk is the exact bytes loaded or last saved, so Save can tell when
	// someone else changed the file in between.
	disk []byte
}

// LoadError means the file is not a mapping: the YAML did not parse, or mapper
// rejected it. Diagnostics carry mapper's positions.
type LoadError struct {
	Path        string
	Diagnostics mapper.Diagnostics
}

func (e *LoadError) Error() string {
	if len(e.Diagnostics) == 0 {
		return e.Path + ": not a mapping file"
	}
	d := e.Diagnostics[0]
	msg := d.Message
	if d.Field != "" {
		msg = d.Field + ": " + msg
	}
	// mapper reports "is required" without a position; ":0:0" would point
	// at nothing.
	if d.Position.StartLine == 0 {
		return fmt.Sprintf("%s: %s", e.Path, msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s", e.Path, d.Position.StartLine, d.Position.StartColumn, msg)
}

// Load reads and parses the mapping file at path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadBytes(path, data)
}

// DefaultFile is the mapping file name used when the user names none.
const DefaultFile = "mapping.yaml"

// LoadBytes parses data as a mapping file; path is only used to name it.
func LoadBytes(path string, data []byte) (*File, error) { return load(path, data, false) }

// LoadDraft is Load for the editor: a file mapper rejects still loads, with
// mapper's verdict in Invalid, so it can be fixed in place. Load stays strict
// for validate, test and apply.
func LoadDraft(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadDraftBytes(path, data)
}

// LoadDraftBytes is LoadBytes with LoadDraft's leniency. YAML that does not
// parse is still a *LoadError.
func LoadDraftBytes(path string, data []byte) (*File, error) { return load(path, data, true) }

func load(path string, data []byte, draft bool) (*File, error) {
	f := &File{Path: path, disk: data}
	if bytes.HasPrefix(data, utf8BOM) {
		f.bom = true
		data = data[len(utf8BOM):]
	}
	f.Src = data
	f.crlf = bytes.Contains(data, []byte("\r\n"))

	// Validate normalizes actions in place (an empty tuple action becomes the
	// rule's, then write), so the Document is built from a second, unvalidated
	// parse: it must hold what the file says, not what mapper infers.
	checked, err := language.Parse(data)
	if err != nil {
		return nil, &LoadError{Path: path, Diagnostics: mapper.DiagnosticsFrom(err)}
	}
	if err := checked.Validate(); err != nil {
		if !draft {
			return nil, &LoadError{Path: path, Diagnostics: mapper.DiagnosticsFrom(err)}
		}
		f.Invalid = mapper.DiagnosticsFrom(err)
	}
	cfg, err := language.Parse(data)
	if err != nil {
		return nil, &LoadError{Path: path, Diagnostics: mapper.DiagnosticsFrom(err)}
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, &LoadError{Path: path, Diagnostics: mapper.DiagnosticsFrom(err)}
	}
	f.root = &root
	f.Doc = documentFrom(cfg)
	if usesAliases(&root) {
		f.Reason = "uses YAML anchors or aliases; edit it by hand"
	} else {
		f.Editable, f.Reason = editable(cfg, f.Doc)
	}
	return f, nil
}

// documentFrom converts mapper's parsed config into the editor's Document.
// Context maps come back sorted: Save keeps the file's own key order anyway,
// so order only matters for keys the user adds.
func documentFrom(cfg *language.MappingConfig) *Document {
	d := &Document{}
	for _, r := range cfg.Rules {
		rule := Rule{Name: r.Name, When: r.When, Action: string(r.Action)}
		for _, v := range r.Variables {
			rule.Variables = append(rule.Variables, Variable{Name: v.Name, Expr: v.Expression})
		}
		for _, fl := range r.TupleFilters {
			rule.Filters = append(rule.Filters, TupleFilter{User: fl.User, Relation: fl.Relation, Object: fl.Object, Action: string(fl.Action)})
		}
		if r.Iterator != nil {
			rule.Iterator = &Iterator{Source: r.Iterator.Source, As: r.Iterator.As, Tuples: tuplesFrom(r.Iterator.Tuples)}
		}
		rule.Tuples = tuplesFrom(r.Tuples)
		d.Rules = append(d.Rules, rule)
	}
	for _, tc := range cfg.Tests {
		d.Tests = append(d.Tests, TestCase{
			Name: tc.Name, Input: tc.Input, ExpectTuples: tc.ExpectTuples,
			ExpectTupleFilters:          tc.ExpectTupleFilters,
			AssertWritesCoveredByFilter: tc.AssertWritesCoveredByFilter,
		})
	}
	return d
}

func tuplesFrom(ps []language.ParsedTuple) []Tuple {
	var out []Tuple
	for _, p := range ps {
		t := Tuple{User: p.User, Relation: p.Relation, Object: p.Object, When: p.When, Action: string(p.Action), Condition: p.Condition}
		keys := make([]string, 0, len(p.Context))
		for k := range p.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			t.Context = append(t.Context, ContextEntry{Key: k, Template: p.Context[k]})
		}
		out = append(out, t)
	}
	return out
}

// editable reports whether rendering doc and parsing it back yields the config
// the file parsed to. If not, the editor would silently drop something on
// Save, so it must refuse to save instead.
func editable(orig *language.MappingConfig, doc *Document) (bool, string) {
	src, err := Marshal(doc)
	if err != nil {
		return false, err.Error()
	}
	back, err := language.Parse(src)
	if err != nil {
		return false, "the editor cannot represent this file: " + err.Error()
	}
	if canonical(orig) != canonical(back) {
		return false, "the editor cannot represent everything in this file; validate, test and apply still work"
	}
	return true, ""
}

// canonical renders the parts of a config that carry meaning, without the
// positions the goccy AST stamps. yaml.v3 sorts map keys, so equal configs give
// equal text.
func canonical(c *language.MappingConfig) string {
	type tuple struct {
		User, Relation, Object, When, Action, Condition string
		Context                                         map[string]string
	}
	conv := func(ps []language.ParsedTuple) []tuple {
		var out []tuple
		for _, p := range ps {
			out = append(out, tuple{p.User, p.Relation, p.Object, p.When, string(p.Action), p.Condition, p.Context})
		}
		return out
	}
	type rule struct {
		Name, When, Action string
		Vars               [][2]string
		Filters            []language.ParsedTupleFilter
		IterSource, IterAs string
		IterTuples, Tuples []tuple
	}
	var view struct {
		Version string
		Rules   []rule
		Tests   []language.TestCase
	}
	view.Version = c.Version
	for _, r := range c.Rules {
		rr := rule{Name: r.Name, When: r.When, Action: string(r.Action), Tuples: conv(r.Tuples)}
		for _, v := range r.Variables {
			rr.Vars = append(rr.Vars, [2]string{v.Name, v.Expression})
		}
		for _, f := range r.TupleFilters {
			f.UserPos, f.RelationPos, f.ObjectPos = language.Position{}, language.Position{}, language.Position{}
			rr.Filters = append(rr.Filters, f)
		}
		if r.Iterator != nil {
			rr.IterSource, rr.IterAs, rr.IterTuples = r.Iterator.Source, r.Iterator.As, conv(r.Iterator.Tuples)
		}
		view.Rules = append(view.Rules, rr)
	}
	view.Tests = c.Tests
	out, err := yaml.Marshal(view)
	if err != nil {
		return fmt.Sprintf("%+v", view)
	}
	return string(out)
}

// RuleLine is the 1-based position of rules[i] in Src, or 0, 0 if there is none.
func (f *File) RuleLine(i int) (line, col int) { return f.itemPos("rules", i) }

// TestLine is the 1-based position of tests[i] in Src, or 0, 0 if there is none.
func (f *File) TestLine(i int) (line, col int) { return f.itemPos("tests", i) }

func (f *File) itemPos(key string, i int) (int, int) {
	seq := mappingValue(f.root, key)
	if seq == nil || seq.Kind != yaml.SequenceNode || i < 0 || i >= len(seq.Content) {
		return 0, 0
	}
	return seq.Content[i].Line, seq.Content[i].Column
}

// ProblemPos is the 1-based position of what p is about: the field of the tuple
// or filter it names, or as much of that as the file has, down to its rule.
// It is 0, 0 for a problem about no rule.
func (f *File) ProblemPos(p Problem) (line, col int) {
	rule := seqItem(mappingValue(f.root, "rules"), p.Rule)
	if rule == nil {
		return 0, 0
	}
	at := rule
	if item := sectionItem(rule, p.Section, p.Item-1); item != nil {
		at = item
		if v := mapValue(item, p.Field); v != nil {
			at = v
		}
	}
	return at.Line, at.Column
}

// sectionItem is the i-th tuple or filter of a rule's section, the iterator's
// tuples counting after the rule's own, or nil.
func sectionItem(rule *yaml.Node, section string, i int) *yaml.Node {
	switch section {
	case "filters":
		return seqItem(mapValue(rule, "tuple_filters"), i)
	case "tuple":
		own := mapValue(rule, "tuples")
		if own != nil && i < len(own.Content) {
			return seqItem(own, i)
		}
		if own != nil {
			i -= len(own.Content)
		}
		return seqItem(mapValue(mapValue(rule, "iterator"), "tuples"), i)
	}
	return nil
}

func seqItem(seq *yaml.Node, i int) *yaml.Node {
	seq = deref(seq)
	if seq == nil || seq.Kind != yaml.SequenceNode || i < 0 || i >= len(seq.Content) {
		return nil
	}
	return seq.Content[i]
}

// mappingValue finds key in the document's top-level mapping.
func mappingValue(root *yaml.Node, key string) *yaml.Node {
	if root == nil || root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil
	}
	return mapValue(root.Content[0], key)
}

// mapValue is key's value in the mapping m, or nil.
func mapValue(m *yaml.Node, key string) *yaml.Node {
	m = deref(m)
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == key {
			return deref(m.Content[j+1])
		}
	}
	return nil
}

// deref is the node an alias stands for, or n itself: an alias has no content
// of its own, so counting or walking it would find none.
func deref(n *yaml.Node) *yaml.Node {
	if n != nil && n.Kind == yaml.AliasNode {
		return n.Alias
	}
	return n
}
