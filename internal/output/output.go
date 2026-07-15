// Package output renders command results either as a styled table/summary for
// humans or as indented JSON for machines (--json).
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	lipgloss "charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"

	"github.com/sergiught/openfga-cli/internal/style"
)

// Output mode toggles set from global flags before commands run.
var (
	// Quiet suppresses incidental success/info lines (-q/--quiet).
	Quiet bool
	// Plain renders tables as tab-separated, unstyled rows (--plain).
	Plain bool
	// Interactive is true when stdout is a terminal. When false (piped or
	// redirected), Table drops its box-drawing frame so the rows stay
	// grep/awk-friendly, mirroring how color is stripped for non-TTY output.
	Interactive bool
)

// JSON writes v as indented JSON to w. A typed nil slice (e.g. `var x []T` with
// zero rows) marshals to `null`, which breaks scripts doing `… --json | jq '.[]'`
// or length checks on empty result sets, so it is coerced to an empty slice and
// serialized as [].
func JSON(w io.Writer, v any) error {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// YAML writes v as YAML to w. v is round-tripped through JSON first so field
// names and shapes match --json exactly (Go struct field names, json tags,
// and the same nil-slice-to-[] coercion as JSON), rather than diverging to
// yaml.v3's own default field-naming rules.
func YAML(w io.Writer, v any) error {
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return err
	}
	node, err := yamlNode(generic)
	if err != nil {
		return err
	}
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer func() { _ = enc.Close() }()
	return enc.Encode(node)
}

func yamlNode(v any) (*yaml.Node, error) {
	switch value := v.(type) {
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value)}, nil
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(value.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value.String()}, nil
	case []any:
		node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range value {
			child, err := yamlNode(item)
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		return node, nil
	case map[string]any:
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			child, err := yamlNode(value[key])
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				child,
			)
		}
		return node, nil
	default:
		return nil, fmt.Errorf("convert JSON value %T to YAML", value)
	}
}

// Emit writes v as YAML when asYAML is set, otherwise as JSON. Commands that
// support --json also support the parallel --output yaml (or -o yaml) via
// this helper, so the two structured formats never need separate branches.
func Emit(w io.Writer, asYAML bool, v any) error {
	if asYAML {
		return YAML(w, v)
	}
	return JSON(w, v)
}

// Table renders a simple, aligned table with a styled header. Columns are
// sized to their widest cell. It is intentionally dependency-light so it can be
// used from any command. In Plain mode it emits tab-separated, unstyled rows
// for grep/awk pipelines.
func Table(w io.Writer, headers []string, rows [][]string) error {
	if Plain {
		for _, row := range rows {
			if _, err := fmt.Fprintln(w, strings.Join(row, "\t")); err != nil {
				return err
			}
		}
		return nil
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) {
				if cw := lipgloss.Width(cell); cw > widths[i] {
					widths[i] = cw
				}
			}
		}
	}

	cell := func(s string, i int) string {
		pad := widths[i] - lipgloss.Width(s)
		if pad < 0 {
			pad = 0
		}
		return s + strings.Repeat(" ", pad)
	}

	var buf strings.Builder

	// Header.
	var hb strings.Builder
	for i, h := range headers {
		if i > 0 {
			hb.WriteString("   ")
		}
		hb.WriteString(style.TableHeader.Render(cell(h, i)))
	}
	fmt.Fprintln(&buf, hb.String())

	// Keep redirected output free of box-drawing. --plain remains the
	// headerless, tab-separated mode for machine pipelines.
	if Interactive {
		var rb strings.Builder
		for i := range headers {
			if i > 0 {
				rb.WriteString("   ")
			}
			rb.WriteString(style.Faint.Render(strings.Repeat("─", widths[i])))
		}
		fmt.Fprintln(&buf, rb.String())
	}

	// Rows.
	for _, row := range rows {
		var b strings.Builder
		for i := range headers {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			if i > 0 {
				b.WriteString("   ")
			}
			b.WriteString(cell(val, i))
		}
		fmt.Fprintln(&buf, b.String())
	}

	if style.Active.Name == "mono" || !Interactive {
		_, err := io.WriteString(w, buf.String())
		return err
	}

	_, err := fmt.Fprintln(w, lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(style.Faintc).
		Padding(0, 1).
		Render(strings.TrimRight(buf.String(), "\n")))
	return err
}

// KeyValues renders an aligned key/value block (used for "get" style output).
func KeyValues(w io.Writer, pairs [][2]string) error {
	width := 0
	for _, p := range pairs {
		if lipgloss.Width(p[0]) > width {
			width = lipgloss.Width(p[0])
		}
	}
	for _, p := range pairs {
		pad := strings.Repeat(" ", width-lipgloss.Width(p[0]))
		if _, err := fmt.Fprintf(w, "%s%s  %s\n",
			style.Key.Render(SanitizeField(p[0])), pad, style.Value.Render(SanitizeField(p[1]))); err != nil {
			return err
		}
	}
	return nil
}

// Successf prints a success line with a green dot (suppressed in Quiet/Plain).
func Successf(w io.Writer, format string, a ...any) {
	if Quiet || Plain {
		return
	}
	dot := lipgloss.NewStyle().Foreground(style.Green).Render(style.IconDot)
	fmt.Fprintf(w, "%s %s\n", dot, fmt.Sprintf(format, a...))
}

// Infof prints a muted informational line with a primary-colored dot
// (suppressed in Quiet/Plain).
func Infof(w io.Writer, format string, a ...any) {
	if Quiet || Plain {
		return
	}
	dot := lipgloss.NewStyle().Foreground(style.Primary).Render(style.IconDot)
	fmt.Fprintf(w, "%s %s\n", dot, fmt.Sprintf(format, a...))
}

// Progressf prints transient progress only for an interactive human session,
// keeping redirected and machine-readable command output quiet.
func Progressf(w io.Writer, format string, a ...any) {
	if Quiet || Plain || !Interactive {
		return
	}
	Infof(w, format, a...)
}

// Errorf prints an error line with a red dot. Unlike Successf/Infof it is
// never suppressed by Quiet — errors must always reach the user.
func Errorf(w io.Writer, format string, a ...any) {
	dot := lipgloss.NewStyle().Foreground(style.Red).Render(style.IconDot)
	fmt.Fprintf(w, "%s %s\n", dot, sanitizeText(fmt.Sprintf(format, a...)))
}

// Hintf writes a faint, indented follow-up line (e.g. a "try this next" hint
// after an error). Rendered on stderr by callers; not suppressed by --quiet so
// remediation guidance always shows.
func Hintf(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "  %s\n", style.Faint.Render(sanitizeText(fmt.Sprintf(format, a...))))
}

// Title prints a bold violet title line.
func Title(w io.Writer, s string) {
	fmt.Fprintln(w, style.Title.Render(SanitizeField(s)))
}

// SanitizeField removes terminal control characters from untrusted values
// before they are embedded in human-readable output. Structured output must
// keep the original data, so callers apply this only at terminal render sites.
func SanitizeField(s string) string {
	return style.SanitizeTerminal(s)
}

func sanitizeText(s string) string {
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = style.SanitizeTerminal(lines[i])
	}
	return strings.Join(lines, "\n")
}
