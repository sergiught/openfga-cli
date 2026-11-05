package optimize

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var diffHunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func TestUnifiedDiff_LineLevel(t *testing.T) {
	before := "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n" +
		"    define a: [user]\n    define b: [user]\n    define viewer: a or a or b\n"
	after := "model\n  schema 1.1\n\ntype user\n\ntype doc\n  relations\n" +
		"    define b: [user]\n    define a: [user]\n    define viewer: a or b\n"
	d := UnifiedDiff("m", before, after)

	var minus, plus []string
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			minus = append(minus, l[1:])
		case strings.HasPrefix(l, "+"):
			plus = append(plus, l[1:])
		}
	}
	// Swapping two relations legitimately moves one line (shown once as -
	// and once as +); a character-level diff would also touch the lines
	// that stayed put and split the changed line into fragments.
	if len(minus) > 2 || len(plus) > 2 {
		t.Errorf("want at most the moved line and the changed line on each side, got -%v +%v:\n%s", minus, plus, d)
	}
	for _, l := range append(append([]string{}, minus...), plus...) {
		if l == "    define a: [user]" {
			t.Errorf("unmoved line %q shown as changed:\n%s", l, d)
		}
	}
	count := func(ss []string, want string) (n int) {
		for _, s := range ss {
			if s == want {
				n++
			}
		}
		return n
	}
	if count(minus, "    define viewer: a or a or b") != 1 || count(plus, "    define viewer: a or b") != 1 {
		t.Errorf("changed line not shown once as - and once as +:\n%s", d)
	}

	// Each hunk's +start must match where its lines land in the new text:
	// apply the hunks to before and compare with after.
	src := strings.SplitAfter(before, "\n")
	var out []string
	pos := 0
	for _, l := range strings.SplitAfter(d, "\n") {
		switch {
		case l == "", strings.HasPrefix(l, "--- "), strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "\\"):
		case strings.HasPrefix(l, "@@"):
			h := diffHunkHeader.FindStringSubmatch(l)
			oldStart, _ := strconv.Atoi(h[1])
			newStart, _ := strconv.Atoi(h[2])
			for ; pos < oldStart-1; pos++ {
				out = append(out, src[pos])
			}
			if len(out)+1 != newStart {
				t.Errorf("hunk %q: +start %d, but the new text reaches it at line %d", strings.TrimSpace(l), newStart, len(out)+1)
			}
		case l[0] == '+':
			out = append(out, l[1:])
		case l[0] == '-':
			pos++
		default:
			out = append(out, src[pos])
			pos++
		}
	}
	out = append(out, src[pos:]...)
	if got := strings.Join(out, ""); got != after {
		t.Errorf("diff does not apply to before:\n%s", d)
	}
}
