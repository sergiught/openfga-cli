package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/openfga/mapper/language"
)

// Plural is "1 write" / "2 writes". Moved from tui.Plural; tui callers use this.
func Plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// ChangeLine is one planned change as the CLI prints it:
// "+ user:1 member org:acme" or "- …", then " (cond: k=json, k2=json)" with
// sorted context keys. field sanitizes each part (output.SanitizeField in the
// CLI, style.SanitizeTerminal in the TUI).
func ChangeLine(t language.Tuple, field func(string) string) string {
	sign := "+"
	if t.Action == language.ActionDelete {
		sign = "-"
	}
	line := fmt.Sprintf("%s %s %s %s", sign, field(t.User), field(t.Relation), field(t.Object))
	if t.Condition == "" {
		return line
	}
	cond := field(t.Condition)
	if len(t.Context) > 0 {
		var pairs []string
		for _, k := range slices.Sorted(maps.Keys(t.Context)) {
			pairs = append(pairs, field(k)+"="+field(compactJSON(t.Context[k])))
		}
		cond += ": " + strings.Join(pairs, ", ")
	}
	return line + " (" + cond + ")"
}

func compactJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// PlanText is every line `ofga mappings apply` prints for p, uncapped: for
// each event with changes, "event N", " · <type>" when set, and
// " (split into N requests)" when not atomic; then its changes indented two
// spaces. The CLI caps it at maxPlanLines and adds its own "… N more" hint.
func PlanText(p *Plan, field func(string) string) []string {
	var lines []string
	for _, ep := range p.Events {
		if len(ep.Changes) == 0 {
			continue
		}
		header := fmt.Sprintf("event %d", ep.Index)
		if ep.Type != "" {
			header += " · " + field(ep.Type)
		}
		if !ep.Atomic {
			header += fmt.Sprintf(" (split into %d requests)", ep.Requests())
		}
		lines = append(lines, header)
		for _, t := range ep.Changes {
			lines = append(lines, "  "+ChangeLine(t, field))
		}
	}
	return lines
}

// PlanSummary is "3 events: 2 writes, 1 delete, 1 matched no rule".
func PlanSummary(p *Plan) string {
	return fmt.Sprintf("%s: %s, %s, %d matched no rule", Plural(len(p.Events), "event"),
		Plural(p.Writes, "write"), Plural(p.Deletes, "delete"), p.Unmatched)
}

// ApplyQuestion is "Apply 2 writes and 1 delete to store acme (01ARZ…)?";
// a zero half is dropped, and with no name only the id is shown.
func ApplyQuestion(writes, deletes int, name, id string) string {
	var parts []string
	if writes > 0 {
		parts = append(parts, Plural(writes, "write"))
	}
	if deletes > 0 {
		parts = append(parts, Plural(deletes, "delete"))
	}
	store := id
	if name != "" {
		store = fmt.Sprintf("%s (%s)", name, store)
	}
	return fmt.Sprintf("Apply %s to store %s?", strings.Join(parts, " and "), store)
}

// SentLine is "sent 2 writes and 1 delete across 3 events to store acme".
func SentLine(p *Plan, label string) string {
	return fmt.Sprintf("sent %s and %s across %s to store %s", Plural(p.Writes, "write"),
		Plural(p.Deletes, "delete"), Plural(len(p.Events), "event"), label)
}

// AppliedSoFar is "no events applied", "applied event 1" or "applied events
// 1-2" for the events before p.Events[applied], numbered as in the file. It
// needs 0 <= applied < len(p.Events). The count Execute returns with an error
// keeps to that: p.Events[applied] is then the event that did not finish.
func AppliedSoFar(p *Plan, applied int) string {
	first := p.Events[0].Index
	switch applied {
	case 0:
		return "no events applied"
	case 1:
		return fmt.Sprintf("applied event %d", first)
	default:
		return fmt.Sprintf("applied events %d-%d", first, p.Events[applied].Index-1)
	}
}
