package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
	"github.com/sergiught/openfga-cli/internal/style"
)

func previewOn(doc *mapping.Document, at Focus) *Preview {
	c := NewPreview(NewSession(context.Background(), doc, nil), "mapping.yaml")
	c.Refresh(at)
	return c
}

// A7: mapper stops before appending the overrunning rule's tuples, so the
// count read 0 and the line stayed faint exactly when the budget was blown.
func TestAnOverBudgetSampleReadsRed(t *testing.T) {
	ids := make([]any, mapping.MaxTuples+1)
	for i := range ids {
		ids[i] = map[string]any{"id": fmt.Sprintf("u%d", i)}
	}
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "b", When: "true",
		Sample: &mapping.Sample{Label: "b", Event: map[string]any{"type": "b", "ids": ids}},
		Iterator: &mapping.Iterator{Source: "input.ids", As: "it", Tuples: []mapping.Tuple{
			{User: "user:{{ it.id }}", Relation: "member", Object: "organization:1"},
		}},
	}}}
	view := previewOn(doc, Focus{}).View(80, 40)
	if strings.Contains(plain(view), "0 of 40") {
		t.Fatalf("the budget line reads 0 of 40 when it is blown:\n%s", plain(view))
	}
	red := lipgloss.NewStyle().Foreground(style.Red).Render("more than 40 tuples — mapper fails the whole event")
	if !strings.Contains(view, red) {
		t.Fatalf("no red over-budget line:\n%s", plain(view))
	}
}

// A4: a tuple whose object has no type evaluated cleanly and was shown with a
// tick, so nothing on screen said FGA would refuse it.
func TestAMalformedTupleIsShownAsAFault(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "a", When: "true",
		Sample: &mapping.Sample{Label: "a", Event: map[string]any{"type": "a"}},
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "member", Object: "acme"}},
	}}}
	view := plain(previewOn(doc, Focus{}).View(80, 40))
	if !strings.Contains(view, `object "acme" is not type:id`) {
		t.Fatalf("the malformed tuple is not flagged:\n%s", view)
	}
}

// A13: the payload inside an iterator is the item mapper's own evaluation of
// the source yields, so a json_path source shows its item.
func TestAJSONPathIteratorShowsItsItem(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "u", When: "true",
		Sample:   &mapping.Sample{Label: "user.created", Event: userCreatedEvent()},
		Iterator: &mapping.Iterator{Source: `json_path(input, "data.object.identities")`, As: "identity"},
	}}}
	c := previewOn(doc, Focus{Rule: 0, InIter: true})
	label, ok := c.Switch()
	if !ok || label != "identity payload" {
		t.Fatalf("switch = %q, %v; want the item's payload on offer", label, ok)
	}
	c.Update(key("ctrl+t"))
	view := plain(c.View(80, 40))
	if !strings.Contains(view, `"connection"`) || strings.Contains(view, "auth0|507f") {
		t.Fatalf("the payload is not the iterator's item:\n%s", view)
	}
}

// Review focus: a sample value is the user's own payload. Control bytes in it
// never reach the terminal, and a unicode key shows as itself. encoding/json
// escapes the C0 controls on its own, so the needles that matter are the ones
// it lets through: C1 controls (8-bit CSI, an OSC that sets the window title),
// DEL and a bidi override. The iterator item is a second way onto the pane.
func TestThePayloadCannotDriveTheTerminal(t *testing.T) {
	const hostile = "\x1b[2J\x1b]0;pwned\a \u009b2J \u009d0;pwned\u009c a\x7fb \u202eevil"
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "a", When: "true",
		Sample: &mapping.Sample{Label: "a", Event: map[string]any{
			"日本語":   hostile,
			"items": []any{map[string]any{"v": hostile}},
		}},
		Iterator: &mapping.Iterator{Source: "input.items", As: "it", Tuples: []mapping.Tuple{
			{User: "user:1", Relation: "member", Object: "organization:1"},
		}},
	}}}
	for at, title := range map[Focus]string{{}: "input payload", {InIter: true}: "it payload"} {
		c := previewOn(doc, at)
		c.Update(key("ctrl+t"))
		view := c.View(80, 40)
		if !strings.Contains(plain(view), title) || !strings.Contains(plain(view), "pwned") {
			t.Fatalf("the %s is not on the pane:\n%s", title, plain(view))
		}
		if hasTerminalControls(view) {
			t.Fatalf("the %s carries a terminal control to the pane: %q", title, view)
		}
	}
	c := previewOn(doc, Focus{})
	c.Update(key("ctrl+t"))
	if view := c.View(80, 40); !strings.Contains(view, "日本語") {
		t.Fatalf("the unicode key is missing:\n%s", plain(view))
	}
}

// Review focus: a rule with no sample has nothing to evaluate and says so.
func TestARuleWithNoSampleSaysSo(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "a", When: "true",
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "member", Object: "organization:1"}},
	}}}
	c := previewOn(doc, Focus{})
	if _, ok := c.Switch(); ok {
		t.Fatal("ctrl+t is offered with no payload to switch to")
	}
	if view := plain(c.View(80, 40)); !strings.Contains(view, "no sample event yet") {
		t.Fatalf("the pane does not say there is no sample:\n%s", view)
	}
}

func TestInsideAnIteratorThePayloadIsTheItem(t *testing.T) {
	c := previewOn(recipeDoc(t, "user.updated"), Focus{Rule: 0, InIter: true})
	label, payload := c.label, c.payload
	if label != "identity payload" {
		t.Fatalf("the payload is headed %q, want the iterator's alias", label)
	}
	if !strings.Contains(payload, `"connection"`) {
		t.Fatalf("the item's own fields are missing:\n%s", payload)
	}
	if strings.Contains(payload, "a0tenant") {
		t.Fatalf("the whole event is shown, and its paths do not resolve here:\n%s", payload)
	}
}

// paneFor is a Preview over typ's recipe rule and its catalog sample.
func paneFor(t *testing.T, typ string, at Focus) *Preview {
	t.Helper()
	p := NewPreview(NewSession(t.Context(), recipeDoc(t, typ), nil), "mapping.yaml")
	p.Refresh(at)
	return p
}

// Scrolling is the whole answer to a payload that does not fit, so the claim
// to check is that moving the section reaches every row.
func TestPagingReachesEveryRowOfThePayload(t *testing.T) {
	const w, rows = 60, 50
	p := paneFor(t, "user.updated", Focus{})
	p.Update(key("ctrl+t"))
	all := jsonRows(p.payload, w)

	first := plain(p.View(w, rows))
	if p.maxOff == 0 {
		t.Fatalf("this payload fits in %d rows, so paging it proves nothing", len(all))
	}
	if !strings.Contains(first, "pgup/pgdn") {
		t.Fatalf("the section never offers the keys that move it:\n%s", first)
	}
	// A page overlaps the one before it by a row, so nothing can fall between
	// two jumps.
	seen := map[string]bool{}
	for {
		for _, row := range strings.Split(plain(p.View(w, rows)), "\n") {
			seen[strings.TrimRight(row, " ")] = true
		}
		if p.payloadOff >= p.maxOff {
			break
		}
		before, tail := p.payloadOff, p.payloadOff+p.page-1
		p.Update(key("pgdown"))
		if p.payloadOff == before {
			t.Fatalf("pgdn stopped moving at %d of %d", before, p.maxOff)
		}
		if p.payloadOff > tail {
			t.Fatalf("paging jumped from row %d to row %d, past the %d on screen", before+1, p.payloadOff+1, p.page)
		}
	}
	for i, row := range all {
		if want := strings.TrimRight(plain(row), " "); !seen[want] {
			t.Fatalf("row %d of %d is unreachable at every scroll position: %q", i+1, len(all), want)
		}
	}
	for range len(all) + 10 {
		p.Update(key("pgup"))
	}
	if p.payloadOff != 0 {
		t.Fatalf("paging back left the offset at %d", p.payloadOff)
	}
}

// The offset belongs to the payload it was taken on: carried across, it would
// open the next one somewhere in its middle.
func TestPagingOnePayloadDoesNotMoveTheNext(t *testing.T) {
	const w, rows = 60, 17
	p := paneFor(t, "user.updated", Focus{})
	p.Update(key("ctrl+t"))
	p.View(w, rows)
	for range 3 {
		p.Update(key("pgdown"))
	}
	if p.payloadOff < 20 {
		t.Fatalf("the event only scrolled to %d, too little to carry", p.payloadOff)
	}

	// The iterator's item is a different payload on the same rule.
	p.Refresh(Focus{InIter: true})
	pane := plain(p.View(w, rows))
	if p.maxOff == 0 {
		t.Fatal("the item fits, so clamping would hide a carried offset")
	}
	if p.payloadOff != 0 {
		t.Fatalf("the item opened at row %d, where the event had been left", p.payloadOff)
	}
	if first := jsonRows(p.payload, w)[0]; !strings.Contains(pane, plain(first)) {
		t.Fatalf("the item does not start at its first row %q:\n%s", plain(first), pane)
	}
}

// The two sections keep their places separately: a look at the payload and
// back does not cost the user their place in the file.
func TestSwitchingKeepsEachSectionsPlace(t *testing.T) {
	const w, rows = 60, 15
	p := paneFor(t, "user.updated", Focus{})
	p.View(w, rows)
	p.Update(key("pgdown"))
	doc := p.docOff
	if doc == 0 {
		t.Fatal("the file fits, so there is no place to keep")
	}
	p.Update(key("ctrl+t"))
	p.View(w, rows)
	p.Update(key("pgdown"))
	if p.docOff != doc {
		t.Fatalf("paging the payload moved the file from %d to %d", doc, p.docOff)
	}
	p.Update(key("ctrl+t"))
	p.View(w, rows)
	if p.docOff != doc {
		t.Fatalf("the file came back at row %d, not the %d it left at", p.docOff, doc)
	}
}

// A rule with no sample offers no switch, and pressing ctrl+t anyway leaves
// the file where it is rather than heading an empty section.
func TestSwitchingDoesNothingWithNoPayload(t *testing.T) {
	d := recipeDoc(t, "user.updated")
	d.Rules[0].Sample = nil
	p := NewPreview(NewSession(t.Context(), d, nil), "mapping.yaml")
	p.Refresh(Focus{})
	if pane := plain(p.View(60, 33)); strings.Contains(pane, "ctrl+t") {
		t.Fatalf("the header offers a switch to nothing:\n%s", pane)
	}
	p.Update(key("ctrl+t"))
	if p.mode != previewDoc {
		t.Fatal("ctrl+t switched the pane to a payload that does not exist")
	}
	if pane := plain(p.View(60, 33)); !strings.Contains(pane, "mapping.yaml") {
		t.Fatalf("the file left the pane:\n%s", pane)
	}
}

// hasTerminalControls reports whether s carries anything that drives a
// terminal rather than printing: an escape other than the SGR colouring the
// pane does itself, any other C0 control but a newline, DEL, a C1 control or a
// bidi override.
func hasTerminalControls(s string) bool {
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			if i+1 < len(s) && s[i+1] == '[' {
				j := i + 2
				for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';' || s[j] == ':') {
					j++
				}
				if j < len(s) && s[j] == 'm' {
					i = j + 1
					continue
				}
			}
			return true
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		i += size
		switch {
		case r == '\n':
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			return true
		case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069, r == 0x200e, r == 0x200f:
			return true
		}
	}
	return false
}

// TestSanitizingADiagnosticKeepsItsLayout guards the seam between stripping
// escapes and preserving shape: mapper points a caret at the offending column
// on its own line, and style.SanitizeTerminal deletes newlines along with the
// escapes, so sanitizing a diagnostic whole collapses the caret into nonsense.
func TestSanitizingADiagnosticKeepsItsLayout(t *testing.T) {
	const msg = "unexpected token EOF (1:13)\n | input.type ==\x1b[31m\n | ............^"

	got := sanitizeKeepingLines(msg)
	if hasTerminalControls(got) {
		t.Fatalf("escapes survived: %q", got)
	}
	if lines := strings.Count(got, "\n"); lines != 2 {
		t.Fatalf("the caret art lost its lines: %q", got)
	}
	if !strings.HasSuffix(got, "| ............^") {
		t.Fatalf("the caret should still end the message: %q", got)
	}
}

// A sample mapper fails for any reason other than the budget produces no tuples
// at all, so a count beside the error would read as a measurement of tuples
// that never reach the store: mapper hands back what the earlier rules
// produced before the failure.
func TestAFailedSampleShowsNoTupleCount(t *testing.T) {
	tup := func(obj string) mapping.Tuple { return mapping.Tuple{User: "user:1", Relation: "member", Object: obj} }
	sample := &mapping.Sample{Label: "x", Event: map[string]any{"type": "x"}}
	doc := &mapping.Document{Rules: []mapping.Rule{
		{Name: "a", Sample: sample, Tuples: []mapping.Tuple{tup("group:1"), tup("group:2")}},
		{Name: "b", Tuples: []mapping.Tuple{tup("group:{{ input.missing.x }}")}},
	}}
	if view := plain(previewOn(doc, Focus{}).View(80, 40)); strings.Contains(view, "of 40 tuples") {
		t.Fatalf("a failed sample shows a tuple count:\n%s", view)
	}
}

// The header offers the payload while the file shows and the file while the
// payload shows, and the pane holds one of the two at a time.
func TestTheHeaderOffersTheSwitchBothWays(t *testing.T) {
	p := paneFor(t, "organization.member.added", Focus{})
	file := plain(p.View(80, 60))
	if !strings.Contains(file, "ctrl+t input payload") || !strings.Contains(file, `version: "1"`) {
		t.Fatalf("the file's header does not offer the payload:\n%s", file)
	}
	p.Update(key("ctrl+t"))
	payload := plain(p.View(80, 60))
	if header, _, _ := strings.Cut(payload, "\n"); !strings.HasPrefix(header, "input payload") || !strings.Contains(header, "ctrl+t file") {
		t.Fatalf("the payload's header does not offer the way back: %q", header)
	}
	if strings.Contains(payload, `version: "1"`) || !strings.Contains(payload, "preview") {
		t.Fatalf("the pane shows the file and the payload at once, or lost the evaluation:\n%s", payload)
	}
}

// A header with room for one note keeps the switch: the rows show where the
// section is, and nothing else on screen leads to the payload.
func TestANarrowHeaderKeepsTheSwitch(t *testing.T) {
	const switchNote = "ctrl+t input payload"
	header := func(w int) string {
		h, _, _ := strings.Cut(plain(paneFor(t, "user.updated", Focus{}).View(w, 18)), "\n")
		return h
	}
	if h := header(80); !strings.Contains(h, "pgup/pgdn") || !strings.Contains(h, switchNote) {
		t.Fatalf("at 80 columns the header should hold both notes, or there is nothing to choose: %q", h)
	}
	// The narrowest header the switch fits: the title, the note, and the rule
	// between them.
	fits := lipgloss.Width("mapping.yaml") + lipgloss.Width(switchNote) + 5
	narrowed := false
	for w := fits; w < 80; w++ {
		h := header(w)
		if !strings.Contains(h, switchNote) {
			t.Fatalf("at %d columns the header kept the position over the switch: %q", w, h)
		}
		narrowed = narrowed || !strings.Contains(h, "pgup/pgdn")
	}
	if !narrowed {
		t.Fatal("every width had room for both notes, so nothing was tested")
	}
}

// No row of the pane is wider than the pane, whichever section shows, for
// every event in the catalog.
func TestNoPreviewRowOverflowsThePane(t *testing.T) {
	for _, e := range auth0.Catalog() {
		for _, w := range []int{30, 44, 60} {
			for _, payload := range []bool{false, true} {
				p := paneFor(t, e.Type, Focus{})
				if payload {
					p.Update(key("ctrl+t"))
				}
				for i, row := range strings.Split(p.View(w, 30), "\n") {
					if got := lipgloss.Width(row); got > w {
						t.Errorf("%s at %d columns, payload %v: row %d is %d cells: %q", e.Type, w, payload, i, got, plain(row))
					}
				}
			}
		}
	}
}

// An evaluated tuple too long for the pane wraps onto more rows rather than
// losing its end.
func TestTheEvaluatedTupleIsWrappedNotCut(t *testing.T) {
	p := paneFor(t, "organization.member.added", Focus{})
	tuples := p.Result().Tuples
	if len(tuples) == 0 {
		t.Fatal("the sample produced no tuple, so there is nothing to wrap")
	}
	pane := plain(p.View(44, 60))
	for _, want := range []string{tuples[0].User, tuples[0].Object} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the pane lost %q:\n%s", want, pane)
		}
	}
}

// A document with no rules yet is not a fault beside the list inviting the
// first one: the pane says what will appear, not what mapper says is missing.
func TestTheEmptyDocumentsPreviewIsNoFault(t *testing.T) {
	view := plain(previewOn(&mapping.Document{}, Focus{}).View(80, 30))
	if strings.Contains(view, "at least one rule") || !strings.Contains(view, "tuples appear here as you add rules") {
		t.Fatalf("the empty document's preview reads as a fault:\n%s", view)
	}
}

// The budget line counts tuples only when the sample ran: a document that does
// not compile has no count to report, and one that does reports its own.
func TestTheTupleBudgetCountsOnlyWhatActuallyRan(t *testing.T) {
	doc := tuplesDoc(mapping.Tuple{User: "user:alice", Relation: "member", Object: "organization:acme"})
	doc.Rules[0].Name = "" // a nameless rule does not compile
	if view := plain(previewOn(doc, Focus{}).View(80, 40)); strings.Contains(view, "of 40 tuples") {
		t.Fatalf("a document that never compiled reports a budget:\n%s", view)
	}
	doc.Rules[0].Name = "member_added"
	if view := plain(previewOn(doc, Focus{}).View(80, 40)); !strings.Contains(view, "1 of 40 tuples") {
		t.Fatalf("the sample's count against the cap is missing:\n%s", view)
	}
}

func TestPreviewPrintsUserRelationObject(t *testing.T) {
	doc := &mapping.Document{Rules: []mapping.Rule{{
		Name: "a", When: "true",
		Sample: &mapping.Sample{Label: "a", Event: map[string]any{"type": "a"}},
		Tuples: []mapping.Tuple{{User: "user:1", Relation: "member", Object: "organization:1"}},
	}}}
	view := plain(previewOn(doc, Focus{}).View(80, 40))
	if !strings.Contains(view, "user:1  member  organization:1") {
		t.Fatalf("the preview does not read user, relation, object:\n%s", view)
	}
}
