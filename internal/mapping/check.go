package mapping

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/openfga/mapper"
	"gopkg.in/yaml.v3"
)

// Finding is one reason a mapping file is not valid. Line and Column are 1-based
// and zero when the problem has no position in the file.
type Finding struct {
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`

	// Rule and Section place the finding for an editor that jumps to it; they are
	// not part of validate's JSON. Rule is the index of the rule it belongs to,
	// -1 for the document. Section is trigger, action, variables, iterator,
	// filters or tuple, and "" when unknown.
	Rule    int    `json:"-"`
	Section string `json:"-"`
}

// Check returns every reason the file is not valid. ix may be nil (no model check).
// A file that does not compile returns only the compile findings: the filter and
// model checks need a file mapper accepts.
func Check(f *File, ix *ModelIndex) []Finding {
	var out []Finding
	if _, diags, err := CompileSource(f.Src); err != nil {
		for _, fd := range FindingsFrom(diags) {
			fd.Rule, fd.Section = f.attribute(fd)
			out = append(out, fd)
		}
		if len(diags) == 0 {
			out = append(out, Finding{Rule: -1, Message: err.Error()})
		}
		return out
	}
	for i, r := range f.Doc.Rules {
		for _, fl := range r.Filters {
			msg := ""
			switch typ, bare := strings.CutSuffix(fl.Object, ":"); {
			case strings.Contains(fl.Object, "{{"): // known only at evaluation time
			case bare && typ != "" && fl.User == "":
				msg = fmt.Sprintf("the filter on %q needs a user: OpenFGA cannot read a whole type without one", fl.Object)
			case fl.Object != "" && !strings.Contains(fl.Object, ":"):
				// Neither a type nor an object: the filter covers no tuple, so a
				// patch fails at apply and a delete changes nothing.
				msg = fmt.Sprintf("the filter object %q needs a type: use %q for every %s, or %q",
					fl.Object, fl.Object+":", fl.Object, fl.Object+":<id>")
			}
			if msg != "" {
				line, col := f.RuleLine(i)
				out = append(out, Finding{Line: line, Column: col, Field: ruleRef(i, r) + ".tuple_filters",
					Message: msg, Rule: i, Section: "filters"})
			}
		}
	}
	if ix != nil {
		for _, p := range Warnings(Lint(f.Doc, ix)) {
			line, col := f.RuleLine(p.Rule)
			field := ""
			if p.Rule >= 0 && p.Rule < len(f.Doc.Rules) {
				field = ruleRef(p.Rule, f.Doc.Rules[p.Rule])
				if p.Section == "filters" {
					field += ".tuple_filters"
				}
			}
			out = append(out, Finding{Line: line, Column: col, Field: field, Message: p.Message,
				Rule: p.Rule, Section: p.Section})
		}
	}
	return out
}

// FindingsFrom converts mapper diagnostics to findings, keeping each message's
// first line. mapper's YAML syntax errors begin with a "[line:col] " echo of the
// position; it is trimmed because callers print the position themselves.
func FindingsFrom(diags mapper.Diagnostics) []Finding {
	out := make([]Finding, 0, len(diags))
	for _, d := range diags {
		msg := firstLine(d.Message)
		if echo := fmt.Sprintf("[%d:%d] ", d.Position.StartLine, d.Position.StartColumn); strings.HasPrefix(msg, echo) {
			msg = msg[len(echo):]
		}
		out = append(out, Finding{Line: d.Position.StartLine, Column: d.Position.StartColumn, Field: d.Field, Message: msg, Rule: -1})
	}
	return out
}

// ruleField is a diagnostic's path into a rule: `rules[N]` or `rules["name"]`,
// then the rest. mapper's validation errors spell it so; its evaluation errors
// leave the rule out and name just the part (`when`, `variables.x`, `user`).
var ruleField = regexp.MustCompile(`^rules\[(?:(\d+)|("(?:[^"\\]|\\.)*"))\]\.?(.*)$`)

// FieldInRule is field without its `rules[N]` / `rules["name"]` prefix, the
// part that names a place inside the rule; a field with no such prefix comes
// back as it is.
func FieldInRule(field string) string {
	if m := ruleField.FindStringSubmatch(field); m != nil {
		return m[3]
	}
	return field
}

// attribute finds the rule and section a compile finding belongs to: the rule
// its field names, by index or by name (the first so named), else the last rule
// starting at or before its line, unless that line is in `tests:`.
func (f *File) attribute(fd Finding) (rule int, section string) {
	rule, rest := -1, fd.Field
	if m := ruleField.FindStringSubmatch(fd.Field); m != nil {
		rest = m[3]
		if m[1] != "" {
			if n, err := strconv.Atoi(m[1]); err == nil && n < len(f.Doc.Rules) {
				rule = n
			}
		} else if name, err := strconv.Unquote(m[2]); err == nil {
			for i, r := range f.Doc.Rules {
				if r.Name == name {
					rule = i
					break
				}
			}
		}
	}
	if rule < 0 && fd.Line > 0 {
		if testsAt := f.testsKeyLine(); testsAt == 0 || fd.Line < testsAt {
			for i := range f.Doc.Rules {
				if line, _ := f.RuleLine(i); line > 0 && line <= fd.Line {
					rule = i
				}
			}
		}
	}
	return rule, sectionOf(rest)
}

// testsKeyLine is the 1-based line of the top-level `tests:` key, or 0.
func (f *File) testsKeyLine() int {
	if f.root == nil || f.root.Kind != yaml.DocumentNode || len(f.root.Content) == 0 {
		return 0
	}
	m := f.root.Content[0]
	for j := 0; j+1 < len(m.Content); j += 2 {
		if m.Content[j].Value == "tests" {
			return m.Content[j].Line
		}
	}
	return 0
}

// sectionOf is the editor section for the part of a rule a field path names.
func sectionOf(rest string) string {
	head, _, _ := strings.Cut(rest, ".")
	head, _, _ = strings.Cut(head, "[")
	switch head {
	case "when", "name":
		return "trigger"
	case "tuples", "tuple":
		return "tuple"
	case "tuple_filters":
		return "filters"
	case "variables", "iterator", "action":
		return head
	}
	return ""
}

// ruleRef names a rule the way mapper's diagnostics do, by name when it has one.
func ruleRef(i int, r Rule) string {
	if r.Name == "" {
		return fmt.Sprintf("rules[%d]", i)
	}
	return fmt.Sprintf("rules[%q]", r.Name)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
