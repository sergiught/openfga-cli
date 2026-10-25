package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const anchored = "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - &t {user: \"user:1\", relation: m, object: \"team:1\"}\n"

const brokenYAML = "version: \"1\"\nrules: [\n"

func brokenWs(t *testing.T) *wsHarness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	writeFile(t, path, brokenYAML)
	o, err := openPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return newWsHarness(t, o, Options{})
}

func TestAReadOnlyFileShowsTheBannerAndRefusesEdits(t *testing.T) {
	h := loadedWs(t, anchored)
	if !h.w.sess.ReadOnly {
		t.Fatal("the session is editable")
	}
	for i, key := range []string{"1", "2", "3"} {
		h.press(key)
		v := h.view()
		if !strings.Contains(v, "Read-only: uses YAML anchors or aliases") {
			t.Fatalf("pane %d lacks the banner:\n%s", i, v)
		}
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), "read-only") {
		t.Fatalf("toast = %q", h.toastText())
	}
	h.w.file.Reason = "bad\x1b]0;pwned\x07reason"
	if got := plain(h.w.readOnlyBanner(80)); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("banner keeps a control: %q", got)
	}
}

func TestReadOnlyBannerKeepsPanesInTheirHeight(t *testing.T) {
	h := loadedWs(t, anchored)
	h.resize(80, 24)
	for _, key := range []string{"1", "2", "3"} {
		h.press(key)
		if n := strings.Count(h.w.View(), "\n") + 1; n > 24 {
			t.Fatalf("pane %s is %d rows at 24", key, n)
		}
	}
}

func TestReadOnlyEmptyStatesOfferNoKey(t *testing.T) {
	h := loadedWs(t, "version: \"1\"\n")
	h.w.sess.ReadOnly = true
	h.w.build()
	for _, p := range []pane{paneRules, paneTests} {
		got := plain(h.w.stacks[p].View(80, 10))
		if strings.Contains(got, "Press") {
			t.Fatalf("pane %d offers a refused key:\n%s", p, got)
		}
	}
	h.w.sess.ReadOnly = false
	h.w.build()
	if got := plain(h.w.stacks[paneRules].View(80, 10)); !strings.Contains(got, "Press") {
		t.Fatalf("an editable empty hub lost its hint:\n%s", got)
	}
}

func TestABrokenFileOpensOnCheck(t *testing.T) {
	h := brokenWs(t)
	if h.w.active != paneCheck || len(h.w.findings) == 0 {
		t.Fatalf("active = %d, findings = %d", h.w.active, len(h.w.findings))
	}
	h.press("enter")
	if h.w.active != paneCheck || h.w.loadErr == nil {
		t.Fatal("enter left load-error mode")
	}
	for _, key := range []string{"1", "3"} {
		h.press(key)
		if v := h.view(); !strings.Contains(flat(v), flat("The file must load before it can be edited: fix it in your editor (e).")) {
			t.Fatalf("pane %s:\n%s", key, v)
		}
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), "fixthefileinyoureditor") {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestBackingOutOfTheModelDialogKeepsItDueUntilTheFileLoads(t *testing.T) {
	h := brokenWs(t)
	h.w.modelDue = true
	h.press("m")
	if h.w.overlay == nil {
		t.Fatal("m did not open the model dialog")
	}
	h.press("esc")
	if h.w.overlay != nil || !h.w.modelDue {
		t.Fatalf("overlay = %v, modelDue = %v after esc; want it closed and still due", h.w.overlay != nil, h.w.modelDue)
	}
	writeFile(t, h.w.path, twoRules)
	h.send(editorDoneMsg{})
	if h.w.overlay == nil || h.w.modelDue {
		t.Fatal("the postponed model dialog did not open")
	}
}

func TestEditorExitReloadsAFixedFile(t *testing.T) {
	h := brokenWs(t)
	h.w.modelDue = true
	stacks, gen, busy := h.w.stacks, h.w.testsGen, h.w.busy
	writeFile(t, h.w.path, twoRules)
	h.send(editorDoneMsg{})
	if h.w.loadErr != nil || h.w.sess.ReadOnly || len(h.w.doc.Rules) != 2 {
		t.Fatalf("loadErr = %v, readonly = %v, rules = %d", h.w.loadErr != nil, h.w.sess.ReadOnly, len(h.w.doc.Rules))
	}
	if h.w.testsGen <= gen || h.w.busy <= busy {
		t.Fatalf("no Tests run started: testsGen %d -> %d, busy %d -> %d", gen, h.w.testsGen, busy, h.w.busy)
	}
	for p := range stacks {
		if h.w.stacks[p] == stacks[p] {
			t.Fatalf("pane %d keeps its old stack", p)
		}
	}
	if h.w.overlay == nil || h.w.modelDue {
		t.Fatal("the postponed model dialog did not open")
	}
	// The dialog is answered; a second exit must not ask again.
	h.w.closeOverlay()
	h.send(editorDoneMsg{})
	if h.w.overlay != nil {
		t.Fatal("a second editor exit opened the model dialog again")
	}
}

func TestEditorExitOnAStillBrokenFileRefreshes(t *testing.T) {
	h := brokenWs(t)
	before := h.w.findings
	writeFile(t, h.w.path, "version: \"1\"\nrules:\n  - name: [\n")
	h.send(editorDoneMsg{})
	if h.w.loadErr == nil || h.w.active != paneCheck {
		t.Fatal("left load-error mode on a broken file")
	}
	if len(before) == 0 || len(h.w.findings) == 0 {
		t.Fatal("no findings")
	}
	if h.w.sess.ReadOnly != true {
		t.Fatal("session editable while broken")
	}
}

func TestNoEditorToasts(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	t.Setenv("PATH", t.TempDir())
	h := brokenWs(t)
	h.press("e")
	if !strings.Contains(h.toastText(), flat("no editor found: set $EDITOR")) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestEOpensTheEditorOnEveryPaneOfABrokenFile(t *testing.T) {
	t.Setenv("VISUAL", "true")
	h := brokenWs(t)
	for _, key := range []string{"1", "2", "3"} {
		h.press(key)
		cmd := h.w.key(wsKey("e"))
		if cmd == nil {
			t.Fatalf("pane %s: e returned no command", key)
		}
		// The editor takes over the terminal, so the runtime must see its exec
		// message itself: wrapped in a paneMsg it would be handed to a pane and
		// the editor would never start.
		msg := cmd()
		if _, tagged := msg.(paneMsg); tagged {
			t.Fatalf("pane %s: the editor command is paneMsg-tagged", key)
		}
		if got := fmt.Sprintf("%T", msg); !strings.Contains(got, "exec") {
			t.Fatalf("pane %s: e produced %s, want the runtime's exec message", key, got)
		}
	}
}

func TestLoadErrorPanesGetNoKeysButTheWorkspaces(t *testing.T) {
	t.Setenv("VISUAL", "true")
	h := brokenWs(t)
	h.press("1")
	h.press("/")
	if h.w.stacks[paneRules].Typing() {
		t.Fatal("/ opened a filter on the hidden Rules pane")
	}
	if cmd := h.w.key(wsKey("e")); cmd == nil {
		t.Fatal("e did not open the editor after /")
	}
	h.press("a", "m", "d")
	if got := h.toastText(); got != "" {
		t.Fatalf("a hidden pane toasted %q", got)
	}
	h.press("3")
	h.press("/")
	if h.w.stacks[paneTests].Typing() {
		t.Fatal("/ opened a filter on the hidden Tests pane")
	}
	for _, key := range []string{"1", "2"} {
		h.press(key)
		for _, k := range h.w.keys() {
			if k == "/ filter" && h.w.active != paneCheck {
				t.Fatalf("pane %s advertises / filter", key)
			}
		}
	}
	h.press("2")
	if !slices.Contains(h.w.keys(), "/ filter") {
		t.Fatalf("Check lost / filter: %q", h.w.keys())
	}
}
