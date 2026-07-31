package playground

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/ui/icons"
)

// ctrl+g is the recovery key for a UI that looks broken, so it cannot be
// discoverable only through the ? overlay — someone staring at boxes has no
// reason to believe help holds the answer. But the hint has to stay off the
// rungs that always render, or it nags every default launch for nothing.
func TestNeedsGlyphHint(t *testing.T) {
	tests := []struct {
		name             string
		parsed, resolved icons.Mode
		want             bool
	}{
		{"guessed nerdfont is the only rung that can show boxes", icons.ModeAuto, icons.ModeNerdFont, true},
		{"guessed unicode always renders", icons.ModeAuto, icons.ModeUnicode, false},
		{"guessed off always renders", icons.ModeAuto, icons.ModeOff, false},
		{"explicit nerdfont was the user's own choice", icons.ModeNerdFont, icons.ModeNerdFont, false},
		{"explicit unicode", icons.ModeUnicode, icons.ModeUnicode, false},
		{"explicit off", icons.ModeOff, icons.ModeOff, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := needsGlyphHint(tc.parsed, tc.resolved); got != tc.want {
				t.Fatalf("needsGlyphHint(%v, %v) = %v, want %v", tc.parsed, tc.resolved, got, tc.want)
			}
		})
	}
}

// collectMsgs drains a command (recursing into batches) into the messages it
// produces, so Init's deferred notices can be inspected without a running
// program.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	var out []tea.Msg
	collectCmd(cmd, &out)
	return out
}

func bootNoticeTexts(cmd tea.Cmd) []string {
	var texts []string
	for _, msg := range collectMsgs(cmd) {
		if n, ok := msg.(bootNoticeMsg); ok {
			texts = append(texts, n.text)
		}
	}
	return texts
}

func TestInitEmitsGlyphHintWhenSet(t *testing.T) {
	m := newTestModel().(Model)
	m.glyphHint = true

	texts := bootNoticeTexts(m.Init())
	var found string
	for _, tx := range texts {
		if strings.Contains(tx, "ctrl+g") {
			found = tx
		}
	}
	if found == "" {
		t.Fatalf("Init should advertise ctrl+g when glyphHint is set; got notices %q", texts)
	}
	// The hint has to name the symptom, not just the key — "press ctrl+g" alone
	// gives a confused user no reason to press it.
	if !strings.Contains(found, "boxes") {
		t.Fatalf("hint should name the symptom it fixes; got %q", found)
	}
}

func TestInitSilentWithoutGlyphHint(t *testing.T) {
	m := newTestModel().(Model)
	m.glyphHint = false

	for _, tx := range bootNoticeTexts(m.Init()) {
		if strings.Contains(tx, "ctrl+g") {
			t.Fatalf("no glyph hint should be emitted when glyphHint is false; got %q", tx)
		}
	}
}

// The pre-existing boot notice and the glyph hint are independent; setting both
// must surface both rather than one silently replacing the other.
func TestGlyphHintDoesNotDisplaceBootNotice(t *testing.T) {
	m := newTestModel().(Model)
	m.glyphHint = true
	m.bootNotice = "ignored invalid pinned model id"

	texts := bootNoticeTexts(m.Init())
	var sawBoot, sawHint bool
	for _, tx := range texts {
		if strings.Contains(tx, "pinned model id") {
			sawBoot = true
		}
		if strings.Contains(tx, "ctrl+g") {
			sawHint = true
		}
	}
	if !sawBoot || !sawHint {
		t.Fatalf("both notices should survive; boot=%v hint=%v, got %q", sawBoot, sawHint, texts)
	}
}
