package mapping

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/sergiught/openfga-cli/internal/style"
)

// wrapLine is the whole answer to the complaint that started this: a tuple's
// object — the part that says which thing the rule touched — sat at the end of
// the line and so was the part the ellipsis ate.
func TestWrapLineHangsContinuationsUnderTheirOwn(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		w    int
		want []string
	}{
		{"a line that fits is left alone", "user:acme", 20, []string{"user:acme"}},
		{
			"a flush line hangs two columns in",
			"write user:alice member organization:acme",
			20,
			[]string{"write user:alice", "  member", "  organization:acme"},
		},
		{
			"an indented line hangs past its own indent",
			"    object: organization:acme",
			26,
			[]string{"    object:", "      organization:acme"},
		},
		{
			"a word longer than the width is broken rather than dropped",
			"  id: aaaaaaaaaaaaaaaaaaaaaaaa",
			16,
			[]string{"  id:", "    aaaaaaaaaaaa", "    aaaaaaaaaaaa"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := wrapLine(tc.in, tc.w)
			if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("wrapLine(%q, %d) =\n%q\nwant\n%q", tc.in, tc.w, got, tc.want)
			}
			for _, l := range got {
				if lipgloss.Width(l) > tc.w {
					t.Fatalf("row %q is %d cells, over the %d it was given", l, lipgloss.Width(l), tc.w)
				}
			}
		})
	}
}

// The evaluated tuple is the line the user complained about by name. It is
// built from their own event payload, so the object is the one part of it they
// cannot reconstruct from the rule they wrote.
func TestTheEvaluatedTupleIsWrappedNotCut(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if len(m.preview.Tuples) == 0 {
		t.Fatal("the sample produced no tuples, so there is nothing to wrap")
	}
	pane := plain(previewOf(m))
	for _, want := range []string{m.preview.Tuples[0].User, m.preview.Tuples[0].Object} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the preview lost %q:\n%s", want, pane)
		}
	}
}

// Wrapping is only an improvement while it stays inside the column it was given:
// a row that overflows the pane lands in the editor beside it.
func TestNoPreviewRowOverflowsThePane(t *testing.T) {
	for _, w := range []int{100, 120, 160} {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: w, Height: 30})
		pw := m.previewWidth()
		for _, l := range strings.Split(plain(previewOf(m)), "\n") {
			if lipgloss.Width(l) > pw {
				t.Fatalf("at %d cols the row %q is %d cells, over the pane's %d",
					w, l, lipgloss.Width(l), pw)
			}
		}
	}
}

// The document reads as a document rather than as a wall: keys are picked out
// from their values, and the {{ }} templates — the only part of the file that
// runs — from the literal text around them.
func TestTheDocumentPreviewIsHighlighted(t *testing.T) {
	out := strings.Join(yamlRows("rules:\n  - user: \"user:{{ input.id }}\"\n", 60), "\n")
	for _, want := range []string{
		style.Key.Render("rules:"),
		style.Key.Render("user:"),
		lipgloss.NewStyle().Foreground(style.Keyword).Render("{{ input.id }}"),
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the document is not highlighted: %q missing from %q", want, out)
		}
	}
}

// Colour is decoration over the file, never a change to it — the same contract
// internal/dsl's highlighter keeps. A preview that quietly rewrote the YAML
// would be worse than one with no colour at all.
func TestHighlightingLeavesTheTextAlone(t *testing.T) {
	src := "version: \"1\"\nrules:\n  # a comment\n  - name: x\n    when: input.type == \"x\"\n"
	got := plain(strings.Join(yamlRows(src, 60), "\n"))
	if want := strings.TrimRight(src, "\n"); got != want {
		t.Fatalf("highlighting changed the text:\ngot  %q\nwant %q", got, want)
	}
}

// A template long enough to wrap is split across two rows, and both halves are
// still part of the same expression. Colouring only the half that carries the
// braces would read as the expression ending mid-line.
func TestASplitTemplateStaysHighlightedOnBothRows(t *testing.T) {
	rows := yamlRows("  object: \"organization:{{ input.data.object.organization.id }}\"", 30)
	if len(rows) < 2 {
		t.Fatalf("the line did not wrap, so there is nothing to carry over: %q", rows)
	}
	open, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(style.Keyword).Render("x"), "x")
	for i, r := range rows[:2] {
		if !strings.Contains(r, open) {
			t.Fatalf("row %d of the split template carries no colour: %q", i, r)
		}
	}
}

// The file used to be cut to the rows it was given, and it was cut from the
// bottom — which is where the rule the user had just written had landed, and
// watching that appear is the whole promise of a live preview. It scrolls now,
// so what used to be lost is merely further down.
func TestTheDocumentReachesItsLastRowByPaging(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: minRows})

	all := yamlRows(string(m.preview.YAML), m.previewWidth())
	last := strings.TrimRight(plain(all[len(all)-1]), " ")
	if strings.Contains(plain(previewOf(m)), last) {
		t.Fatalf("the file already fits in %d rows, so paging it proves nothing", len(all))
	}
	for range len(all) {
		if strings.Contains(plain(previewOf(m)), last) {
			return
		}
		send(m, key("pgdown"))
	}
	t.Fatalf("the file's last row %q is out of reach:\n%s", last, plain(previewOf(m)))
}

// The payload is the other half of the pane. Writing
// `{{ input.data.object.organization.id }}` means knowing what the event holds,
// and ctrl+p inserts one path without ever showing the shape they come from.
func TestThePayloadIsOneKeyAway(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if pane := plain(previewOf(m)); !strings.Contains(pane, "ctrl+t input payload") {
		t.Fatalf("the file's header never offers the way to the payload:\n%s", pane)
	}
	send(m, key("ctrl+t"))
	pane := plain(previewOf(m))
	for _, want := range []string{"input payload", `"a0tenant"`, `"org_1234567890abcdef"`} {
		if !strings.Contains(pane, want) {
			t.Fatalf("the payload section is missing %q:\n%s", want, pane)
		}
	}

	// And back, by the key its own header offers.
	if !strings.Contains(pane, "ctrl+t file") {
		t.Fatalf("the payload's header never offers the way back:\n%s", pane)
	}
	send(m, key("ctrl+t"))
	if pane := plain(previewOf(m)); !strings.Contains(pane, m.path) {
		t.Fatalf("ctrl+t did not bring the file back:\n%s", pane)
	}
}

// The header note alone meant a user had to be reading the preview already to
// learn there was a second half of it. The hint row is where a user looks for
// keys, so the switch is named there too, in the same words.
func TestTheHintRowOffersTheSwitch(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	if bar := plain(m.statusBar()); !strings.Contains(bar, "ctrl+t input payload") {
		t.Fatalf("the hint row never offers the switch:\n%s", bar)
	}
	send(m, key("ctrl+t"))
	if bar := plain(m.statusBar()); !strings.Contains(bar, "ctrl+t file") {
		t.Fatalf("the hint row never offers the way back:\n%s", bar)
	}
}

// A hint for a key that does nothing is worse than no hint: the user presses
// it, nothing moves, and they are left doubting the rest of the row.
func TestTheHintRowKeepsTheSwitchToItself(t *testing.T) {
	m := atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})

	m.rule().Sample = nil
	if bar := plain(m.statusBar()); strings.Contains(bar, "ctrl+t") {
		t.Fatalf("a rule with no sample still offers the switch:\n%s", bar)
	}

	m = atRuleFor(t, "organization.member.added")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	send(m, key("?"))
	if bar := plain(m.statusBar()); strings.Contains(bar, "ctrl+t") {
		t.Fatalf("a card screen with no preview still offers the switch:\n%s", bar)
	}
}

// Inside an iterator the expressions address the item, not the event:
// `identity.connection`, never `input.data.object.identities[0].connection`.
// Showing the whole event there would point the user at paths that do not
// resolve — the same rewrite the path picker already does.
func TestInsideAnIteratorThePayloadIsTheItem(t *testing.T) {
	m := atRuleFor(t, "user.created")
	m.inIter = true

	label, payload := m.samplePayload()
	if label != "identity payload" {
		t.Fatalf("the payload is headed %q, want the iterator's alias", label)
	}
	if !strings.Contains(payload, `"connection"`) {
		t.Fatalf("the item's own fields are missing:\n%s", payload)
	}
	if strings.Contains(payload, "a0tenant") {
		t.Fatalf("the whole event is shown, whose paths do not resolve here:\n%s", payload)
	}
}

// The pane holds one long section and the evaluation, never both long sections
// at once. Splitting fixed rows between two things that each grow without limit
// is what this replaced: the file lost its tail to a payload that was itself
// down to three rows of a hundred-odd.
func TestThePaneShowsOneSectionAtATime(t *testing.T) {
	for _, sz := range layoutSizes {
		m := atRuleFor(t, "user.updated")
		m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})

		if !m.sideBySide() {
			// Stacked, the pane is whatever rows the editor leaves, which can be
			// none at all — previewOf has no meaningful width to ask for.
			continue
		}
		for _, mode := range []previewMode{previewDoc, previewPayload} {
			m.previewMode = mode
			pane := plain(previewOf(m))
			// The bodies, not the headers: the file's header names the payload,
			// that being where its ctrl+t leads.
			if strings.Contains(pane, `version: "1"`) && strings.Contains(pane, `"a0stream"`) {
				t.Errorf("at %dx%d the pane shows both sections:\n%s", sz.w, sz.h, pane)
			}
			if !strings.Contains(pane, "preview") {
				t.Errorf("at %dx%d the section cost the evaluation its place:\n%s", sz.w, sz.h, pane)
			}
		}
	}
}

// The pane is drawn into a MaxHeight container, so a section that promises more
// rows than the pane has is not an overflow to catch — it is the evaluation
// silently clipped off the bottom. The check is on the rendered view for that
// reason, and it is the evaluation that has to survive: it is what the user is
// reacting to.
func TestThePaneNeverSpendsMoreRowsThanItHas(t *testing.T) {
	for _, sz := range layoutSizes {
		for _, mode := range []previewMode{previewDoc, previewPayload} {
			m := atRuleFor(t, "user.updated")
			m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
			m.previewMode = mode

			if why := doesNotFit(m.viewString(), sz.w, sz.h); why != "" {
				t.Errorf("at %dx%d mode=%d: %s:\n%s", sz.w, sz.h, mode, why, m.viewString())
			}
			// Stacked, the pane takes what the editor leaves and a short terminal
			// leaves nothing — which predates the sections taking turns.
			if view := plain(m.viewString()); m.sideBySide() && !strings.Contains(view, "preview") {
				t.Errorf("at %dx%d mode=%d the evaluation was clipped away:\n%s",
					sz.w, sz.h, mode, view)
			}
		}
	}
}

// Three sections sharing the rows two had is only safe while the screen still
// fits the terminal. The general overflow sweep drives screens by stack alone,
// so it never has a sample and never renders a payload section at all.
func TestTheScreenWithAPayloadStaysInsideTheTerminal(t *testing.T) {
	for _, sz := range layoutSizes {
		m := atRuleFor(t, "organization.member.added")
		m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		if _, payload := m.samplePayload(); payload == "" {
			t.Fatal("this rule has no sample, so the test exercises nothing")
		}
		if why := doesNotFit(m.viewString(), sz.w, sz.h); why != "" {
			t.Errorf("at %dx%d: %s:\n%s", sz.w, sz.h, why, m.viewString())
		}
	}
}

// Colour over the payload is decoration only, the same contract the document
// half keeps: stripping the escapes gives back the JSON that went in.
func TestHighlightingLeavesThePayloadAlone(t *testing.T) {
	src := "{\n  \"type\": \"user.created\",\n  \"data\": {\n    \"id\": 7\n  }\n}"
	rows := strings.Join(jsonRows(src, 60), "\n")
	if got := plain(rows); got != src {
		t.Fatalf("highlighting changed the payload:\ngot  %q\nwant %q", got, src)
	}
	if !strings.Contains(rows, style.Key.Render(`"type"`)) {
		t.Fatal("the field names are not picked out from their values")
	}
}

// Scrolling is the whole answer to a payload that does not fit. user.updated is
// 114 lines of JSON against a section that can offer around twenty, and eliding
// long values would have saved eight rows of that — so the section has to move
// instead, and the claim to check is that moving it reaches everything.
func TestPagingReachesEveryRowOfThePayload(t *testing.T) {
	m := atRuleFor(t, "user.updated")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
	send(m, key("ctrl+t"))

	_, payload := m.samplePayload()
	all := jsonRows(payload, m.previewWidth())

	first := plain(previewOf(m))
	if m.previewMaxOff == 0 {
		t.Fatalf("this payload fits in %d rows, so paging it proves nothing", len(all))
	}
	if !strings.Contains(first, "pgup/pgdn") {
		t.Fatalf("the section never offers the keys that move it:\n%s", first)
	}

	// Walk it from top to bottom, ticking off every row that comes into view. A
	// page overlaps the one before it by a row, so nothing can fall between two
	// jumps.
	seen := map[string]bool{}
	for {
		for _, row := range strings.Split(plain(previewOf(m)), "\n") {
			seen[strings.TrimRight(row, " ")] = true
		}
		if m.payloadOff >= m.previewMaxOff {
			break
		}
		before, tail := m.payloadOff, m.payloadOff+m.previewPage-1
		send(m, key("pgdown"))
		if m.payloadOff == before {
			t.Fatalf("pgdn stopped moving at %d of %d", before, m.previewMaxOff)
		}
		// A page is one row short of the window, so the row the eye stopped on
		// is still on screen to land on. Checked by index rather than by the
		// rows seen, because a payload repeats itself — `},` is most of a
		// closing brace's line — and a skipped row would be ticked off by its
		// twin somewhere else.
		if m.payloadOff > tail {
			t.Fatalf("paging jumped from row %d to row %d, past the %d on screen",
				before+1, m.payloadOff+1, m.previewPage)
		}
	}
	for i, row := range all {
		if want := strings.TrimRight(plain(row), " "); !seen[want] {
			t.Fatalf("row %d of %d is unreachable at every scroll position: %q",
				i+1, len(all), want)
		}
	}

	// And back, without the offset running past the top into negative rows.
	for range len(all) + 10 {
		send(m, key("pgup"))
	}
	if m.payloadOff != 0 {
		t.Fatalf("paging back left the offset at %d", m.payloadOff)
	}
}

// The offset belongs to the payload it was taken on. Carried across, it would
// open the next one somewhere in its middle, on a row that means nothing there.
//
// The sizes are chosen so that clamping alone cannot pass this: the iterator's
// item is long enough at 120x40 to hold an offset of its own, so an offset that
// survives the switch survives visibly rather than being trimmed to zero.
func TestPagingOnePayloadDoesNotMoveTheNext(t *testing.T) {
	m := atRuleFor(t, "user.updated")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	send(m, key("ctrl+t"))
	previewOf(m)

	for range 3 {
		send(m, key("pgdown"))
	}
	if m.payloadOff < 20 {
		t.Fatalf("the event only scrolled to %d, too little to carry", m.payloadOff)
	}

	// The iterator's item is a different payload on the same rule.
	m.inIter = true
	pane := plain(previewOf(m))
	if m.previewMaxOff == 0 {
		t.Fatal("the item fits, so clamping would hide a carried offset")
	}
	if m.payloadOff != 0 {
		t.Fatalf("the item opened at row %d, where the event had been left", m.payloadOff)
	}
	_, payload := m.samplePayload()
	if first := jsonRows(payload, m.previewWidth())[0]; !strings.Contains(pane, plain(first)) {
		t.Fatalf("the item does not start at its first row %q:\n%s", plain(first), pane)
	}
}

// The two sections keep their places separately. A look at the payload and back
// should not cost the user the part of the file they were watching — which is
// the whole reason to look at the payload in the first place.
func TestSwitchingKeepsEachSectionsPlace(t *testing.T) {
	m := atRuleFor(t, "user.updated")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: minRows + 6})
	previewOf(m)

	send(m, key("pgdown"))
	doc := m.docOff
	if doc == 0 {
		t.Fatal("the file fits, so there is no place to keep")
	}

	send(m, key("ctrl+t"))
	previewOf(m)
	send(m, key("pgdown"))
	if m.docOff != doc {
		t.Fatalf("paging the payload moved the file from %d to %d", doc, m.docOff)
	}

	send(m, key("ctrl+t"))
	previewOf(m)
	if m.docOff != doc {
		t.Fatalf("the file came back at row %d, not the %d it was left at", m.docOff, doc)
	}
}

// A rule too narrow for both keeps the switch and drops the position. Which
// part of a long file you are looking at is visible in it; the key that reaches
// the payload is not visible anywhere else.
func TestANarrowHeaderKeepsTheSwitch(t *testing.T) {
	m := atRuleFor(t, "user.updated")
	m.Update(tea.WindowSizeMsg{Width: sideBySideMin + 4, Height: 30})

	pane := plain(previewOf(m))
	if strings.Contains(pane, "pgup/pgdn 1-") && strings.Contains(pane, "ctrl+t input payload") {
		t.Skipf("the rule still holds both, so nothing was dropped:\n%s", pane)
	}
	if !strings.Contains(pane, "ctrl+t input payload") {
		t.Fatalf("the narrow header dropped the switch and kept the position:\n%s", pane)
	}
}

// ctrl+t is offered only where there is a payload behind it. On a rule that has no
// sample the header says nothing about the key, and pressing it anyway leaves
// the file where it is rather than heading an empty section.
func TestSwitchingDoesNothingWithNoPayload(t *testing.T) {
	m := atRuleFor(t, "user.updated")
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.rule().Sample = nil

	if pane := plain(previewOf(m)); strings.Contains(pane, "ctrl+t") {
		t.Fatalf("the header offers a switch to nothing:\n%s", pane)
	}
	send(m, key("ctrl+t"))
	if m.previewMode != previewDoc {
		t.Fatal("ctrl+t switched the pane to a payload that does not exist")
	}
	if pane := plain(previewOf(m)); !strings.Contains(pane, m.path) {
		t.Fatalf("the file left the pane:\n%s", pane)
	}
}
