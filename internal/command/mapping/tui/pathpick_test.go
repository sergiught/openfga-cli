package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/ui/field"
)

func openPicker(s *Session) component {
	c, _ := newPathPicker(s, Focus{})
	return c
}

func iterDoc(source string) *mapping.Document {
	d := sampleDoc(userCreatedEvent())
	d.Rules[0].Iterator = &mapping.Iterator{Source: source, As: "identity"}
	return d
}

func TestPathPickListsSamplePathsWithExamples(t *testing.T) {
	h := newHarness(t, sampleDoc(memberAddedEvent()), nil, openPicker)
	view := plain(h.view(80, 40))
	for _, want := range []string{"input.data.object.organization.id", "org_1234567890abcdef"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the picker does not show %q:\n%s", want, view)
		}
	}
}

func TestPathPickIsFilterable(t *testing.T) {
	h := newHarness(t, sampleDoc(memberAddedEvent()), nil, openPicker)
	h.press("/")
	h.typeText("user_id")
	h.press("enter", "enter")
	if !h.closed || h.result != (pathPick{Expr: "input.data.object.user.user_id"}) {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

// The filter is recomputed after every key, and the recompute must not move
// the caret the user left mid-word.
func TestPathPickFilterKeepsTheCaretWhereTheUserLeftIt(t *testing.T) {
	h := newHarness(t, sampleDoc(memberAddedEvent()), nil, openPicker)
	h.press("/")
	h.typeText("user_d")
	h.press("left")
	h.typeText("i")
	h.press("enter", "enter")
	if h.result != (pathPick{Expr: "input.data.object.user.user_id"}) {
		t.Fatalf("result = %#v, want the user_id path", h.result)
	}
}

func TestEscClosesThePickerWithNothing(t *testing.T) {
	h := newHarness(t, sampleDoc(memberAddedEvent()), nil, openPicker)
	h.press("esc")
	if !h.closed || h.result != nil {
		t.Fatalf("closed = %v, result = %#v; want closed with nothing", h.closed, h.result)
	}
}

func TestWithoutASampleThePickerExplainsItself(t *testing.T) {
	s := NewSession(context.Background(), &mapping.Document{Rules: []mapping.Rule{{Name: "r"}}}, nil)
	c, why := newPathPicker(s, Focus{})
	if c != nil || !strings.Contains(why, "pick a sample event first") {
		t.Fatalf("picker = %v, why = %q", c, why)
	}
}

func TestPathPickInIteratorScopeUsesTheAlias(t *testing.T) {
	s := NewSession(context.Background(), iterDoc("input.data.object.identities"), nil)
	items := pathItems(s, Focus{InIter: true})
	var exprs []string
	for _, it := range items {
		exprs = append(exprs, it.ID)
		if strings.HasPrefix(it.ID, "input.") {
			t.Fatalf("an event-level path inside the iterator: %q", it.ID)
		}
	}
	if !strings.Contains(strings.Join(exprs, " "), "identity.connection") {
		t.Fatalf("paths = %v, want identity.connection", exprs)
	}
}

// A13: Lookup missed json_path and ?? sources that mapper evaluates; the
// picker now asks mapper for the item.
func TestPathPickResolvesEverySourceMapperDoes(t *testing.T) {
	for _, src := range []string{
		`json_path(input, "data.object.identities")`,
		`input.data.object.identities ?? []`,
	} {
		s := NewSession(context.Background(), iterDoc(src), nil)
		items := pathItems(s, Focus{InIter: true})
		found := false
		for _, it := range items {
			found = found || it.ID == "identity.connection"
		}
		if !found {
			t.Fatalf("%s: no identity.connection among %d paths", src, len(items))
		}
	}
}

func TestPathPickInsideAnUnresolvableIteratorNamesTheSource(t *testing.T) {
	s := NewSession(context.Background(), iterDoc("input.nope"), nil)
	c, why := newPathPicker(s, Focus{InIter: true})
	if c != nil || !strings.Contains(why, "input.nope does not resolve to a list of objects") {
		t.Fatalf("picker = %v, why = %q", c, why)
	}
}

// Review focus: example values come straight out of the user's payload.
func TestPathRowsCannotDriveTheTerminal(t *testing.T) {
	const attack = "\x1b[2J\x1b[1;1H\x1b]0;pwned\a"
	h := newHarness(t, sampleDoc(map[string]any{"data": map[string]any{"evil": attack + "id"}, "日本語": "値"}), nil, openPicker)
	view := h.view(80, 40)
	for _, bad := range []string{"\x1b[2J", "\x1b]", "\a"} {
		if strings.Contains(view, bad) {
			t.Fatalf("a path row carries %q to the terminal", bad)
		}
	}
	if !strings.Contains(plain(view), "input.日本語") {
		t.Fatalf("the unicode key's path is missing:\n%s", plain(view))
	}
}

func TestInsertPathWrapsATemplateFieldAndLeavesAnExpressionBare(t *testing.T) {
	f := field.NewForm(field.New("Object", ""), field.New("When", ""))
	f.Init()
	f.SetValues([]string{"organization:", ""})
	f.SetCursor(0, len("organization:"))
	insertPath(f, 0, true, "input.data.object.organization.id")
	insertPath(f, 1, false, "input.type")
	got := f.Values()
	if got[0] != "organization:{{ input.data.object.organization.id }}" || got[1] != "input.type" {
		t.Fatalf("values = %q", got)
	}
}

func TestInsertPathGoesInAtTheCaretNotTheEnd(t *testing.T) {
	f := field.NewForm(field.New("User", ""))
	f.Init()
	f.SetValues([]string{"user:-suffix"})
	f.SetCursor(0, len("user:"))
	insertPath(f, 0, true, "input.id")
	if got := f.Values()[0]; got != "user:{{ input.id }}-suffix" {
		t.Fatalf("user = %q", got)
	}
}

// ctrl+o leaves the caret inside the template it just prefilled; a path
// inserted there must not be wrapped a second time.
func TestPathInsertedInsideAnExistingTemplateIsNotWrappedAgain(t *testing.T) {
	f := field.NewForm(field.New("Object", ""))
	f.Init()
	f.SetValues([]string{"organization:{{  }}"})
	f.SetCursor(0, len("organization:{{ "))
	insertPath(f, 0, true, "input.id")
	if got := f.Values()[0]; got != "organization:{{ input.id }}" {
		t.Fatalf("object = %q", got)
	}
}

func TestInsideTemplate(t *testing.T) {
	cases := []struct {
		name string
		s    string
		pos  int
		want bool
	}{
		{"empty field", "", 0, false},
		{"plain text", "organization:", 13, false},
		{"between braces", "organization:{{  }}", 16, true},
		{"inside fga_escape", "user:{{ fga_escape() }}", 19, true},
		{"after a closed template", "organization:{{ x }}", 20, false},
		{"before the braces", "organization:{{ x }}", 5, false},
		{"past the end clamps", "organization:{{  }}", 99, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := insideTemplate(c.s, c.pos); got != c.want {
				t.Fatalf("insideTemplate(%q, %d) = %v, want %v", c.s, c.pos, got, c.want)
			}
		})
	}
}

// The breadcrumb places the path pick: under the form it inserts into.
func TestThePathPickIsNamedInTheBreadcrumb(t *testing.T) {
	h, _ := formOn(t, tuplesDoc(mapping.Tuple{}), nil)
	h.press("ctrl+p")
	topAs[*pathPicker](t, h)
	if titles := h.st.Titles(); len(titles) < 2 || titles[len(titles)-2] != "Tuple" || titles[len(titles)-1] != "Insert a path" {
		t.Fatalf("titles = %q, want the tuple form and then the pick", titles)
	}
}
