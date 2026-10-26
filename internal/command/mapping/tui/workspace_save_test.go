package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rename edits rule i the way an editor would.
func (h *wsHarness) rename(i int, name string) {
	h.t.Helper()
	h.w.doc.Rules[i].Name = name
	h.cmds = append(h.cmds, h.w.changed())
}

// toastText is what the toasts say, with the box's padding and wrapping gone.
func (h *wsHarness) toastText() string { return flat(plain(h.w.toasts.View())) }

func flat(s string) string { return strings.Join(strings.Fields(s), "") }

func TestSaveWritesKeepsCommentsAndToasts(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "renamed")
	h.press("ctrl+s")
	got := readFile(t, h.w.path)
	for _, want := range []string{"# Org membership sync.", "name: renamed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the saved file lacks %q:\n%s", want, got)
		}
	}
	if want := flat("saved " + h.w.path + " (2 rules, 1 test)"); !strings.Contains(h.toastText(), want) {
		t.Fatalf("toast = %q, want it to contain %q", h.toastText(), want)
	}
	h.settleDebounce()
	if h.w.dirty {
		t.Fatal("still unsaved after the save")
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), flat("nothing to save")) {
		t.Fatalf("a second save toasted %q, want nothing to save", h.toastText())
	}
}

func TestSaveOnAFreshFileCreatesIt(t *testing.T) {
	h := freshWs(t, sampleDoc(memberAddedEvent()))
	h.press("ctrl+s")
	if h.w.fresh || h.w.file == nil || h.w.dirty {
		t.Fatalf("fresh = %v, file = %v, dirty = %v after the first save", h.w.fresh, h.w.file != nil, h.w.dirty)
	}
	f, err := mapping.LoadDraft(h.w.path)
	if err != nil || len(f.Doc.Rules) != 1 {
		t.Fatalf("saved file: %v, %v", f, err)
	}
}

func TestSaveRefusesAFileChangedOnDisk(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	writeFile(t, h.w.path, "version: \"1\"\nrules: []\n")
	h.press("ctrl+s")
	if h.w.overlay == nil || !h.w.dirty {
		t.Fatalf("overlay = %v, dirty = %v; want the dialog and the edits kept", h.w.overlay != nil, h.w.dirty)
	}
	v := strings.Join(strings.Fields(h.view()), " ")
	for _, want := range []string{"changed on disk since it was opened", "r reload · o save over it · esc cancel"} {
		if !strings.Contains(v, want) {
			t.Fatalf("the dialog lacks %q:\n%s", want, h.view())
		}
	}
	if got := readFile(t, h.w.path); strings.Contains(got, "mine") {
		t.Fatalf("a refused save wrote the file:\n%s", got)
	}
	h.press("esc")
	if h.w.overlay != nil {
		t.Fatal("esc left the dialog open")
	}
}

func TestReloadAsksBeforeDiscarding(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	writeFile(t, h.w.path, strings.Replace(twoRules, "member-added", "theirs", 1))
	h.press("ctrl+s", "r")
	if !strings.Contains(h.view(), "Discard your unsaved edits") {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("enter")
	if h.w.overlay != nil || h.w.doc.Rules[0].Name != "mine" {
		t.Fatalf("enter reloaded: overlay = %v, name = %q", h.w.overlay != nil, h.w.doc.Rules[0].Name)
	}
	h.press("ctrl+s", "r", "y")
	if h.w.doc.Rules[0].Name != "theirs" || h.w.dirty || h.w.overlay != nil {
		t.Fatalf("name = %q, dirty = %v, overlay = %v; want the disk's rule, clean", h.w.doc.Rules[0].Name, h.w.dirty, h.w.overlay != nil)
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), flat("nothing to save")) {
		t.Fatalf("after a reload a save toasted %q; the file should be current", h.toastText())
	}
}

func TestReloadRebuildsOpenEditors(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	h.press("enter")
	if _, ok := h.topOf(paneRules).(*tupleForm); !ok {
		t.Fatalf("top is %T, want the tuple form", h.topOf(paneRules))
	}
	// An edit, so the save reaches the disk check, then an outside edit that
	// deletes rule 2.
	h.rename(0, "mine")
	oneRule := twoRules[:strings.Index(twoRules, "  - name: member-removed")] + twoRules[strings.Index(twoRules, "tests:"):]
	writeFile(t, h.w.path, oneRule)
	h.press("ctrl+s", "r", "y")
	h.run()
	if len(h.w.doc.Rules) != 1 {
		t.Fatalf("rules = %d after the reload, want 1", len(h.w.doc.Rules))
	}
	if _, ok := h.topOf(paneRules).(*rulesHub); !ok || h.w.stacks[paneRules].Depth() != 1 {
		t.Fatalf("top is %T at depth %d, want the rules list", h.topOf(paneRules), h.w.stacks[paneRules].Depth())
	}
	if r := h.w.focusRule().Rule; r < 0 || r >= len(h.w.doc.Rules) || h.w.preview.at.Rule >= len(h.w.doc.Rules) {
		t.Fatalf("focus on rule %d, preview on %d, with %d rules", r, h.w.preview.at.Rule, len(h.w.doc.Rules))
	}
	_ = h.view()
}

func TestSaveOverAsksThenWrites(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	outside := strings.Replace(twoRules, "member-added", "theirs", 1)
	writeFile(t, h.w.path, outside)
	h.press("ctrl+s", "o")
	if !strings.Contains(h.view(), "Replace the file on disk") {
		t.Fatalf("no confirmation:\n%s", h.view())
	}
	h.press("enter")
	if readFile(t, h.w.path) != outside || h.w.overlay != nil {
		t.Fatal("enter wrote over the file")
	}
	h.press("ctrl+s", "o", "y")
	got := readFile(t, h.w.path)
	if !strings.Contains(got, "name: mine") || !strings.Contains(got, "# Org membership sync.") || h.w.dirty {
		t.Fatalf("dirty = %v, file:\n%s", h.w.dirty, got)
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), flat("nothing to save")) {
		t.Fatalf("a save after saving over toasted %q", h.toastText())
	}
}

func TestSaveOverRecreatesADeletedFile(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	if err := os.Remove(h.w.path); err != nil {
		t.Fatal(err)
	}
	h.press("ctrl+s")
	if _, err := os.Stat(h.w.path); h.w.overlay == nil || !os.IsNotExist(err) {
		t.Fatalf("overlay = %v, stat err = %v; want the conflict dialog and no file", h.w.overlay != nil, err)
	}
	h.press("o", "y")
	if got := readFile(t, h.w.path); !strings.Contains(got, "name: mine") {
		t.Fatalf("the file was not recreated with the edit:\n%s", got)
	}
}

func TestSaveOfACleanFileDeletedOutsideOffersSaveOver(t *testing.T) {
	h := loadedWs(t, twoRules)
	if err := os.Remove(h.w.path); err != nil {
		t.Fatal(err)
	}
	h.press("ctrl+s")
	if h.w.overlay == nil || !strings.Contains(strings.Join(strings.Fields(h.view()), " "), "changed on disk") {
		t.Fatalf("overlay = %v; want the conflict dialog, not %q", h.w.overlay != nil, h.toastText())
	}
	h.press("o", "y")
	if got := readFile(t, h.w.path); !strings.Contains(got, "# Org membership sync.") {
		t.Fatalf("the file was not recreated:\n%s", got)
	}
}

func TestFreshFileThatAppearedOnDiskConflicts(t *testing.T) {
	h := freshWs(t, sampleDoc(memberAddedEvent()))
	writeFile(t, h.w.path, "theirs: true\n")
	h.press("ctrl+s")
	if v := strings.Join(strings.Fields(h.view()), " "); h.w.overlay == nil || !strings.Contains(v, "appeared on disk") {
		t.Fatalf("overlay = %v:\n%s", h.w.overlay != nil, h.view())
	}
	if readFile(t, h.w.path) != "theirs: true\n" {
		t.Fatal("a fresh save replaced a file that appeared")
	}
	h.press("o", "y")
	if h.w.fresh || h.w.file == nil || !strings.Contains(readFile(t, h.w.path), "name: r") {
		t.Fatalf("fresh = %v, file = %v; want it written over", h.w.fresh, h.w.file != nil)
	}
}

func TestGeneratedTestsRefreshOnEverySave(t *testing.T) {
	h := freshWs(t, sampleDoc(memberAddedEvent()))
	h.w.doc.Rules[0].When = `input.type == "organization.member.added"`
	h.w.doc.Rules[0].Tuples = []mapping.Tuple{{User: "user:a", Relation: "member", Object: "organization:1"}}
	h.cmds = append(h.cmds, h.w.changed())
	h.press("ctrl+s")
	first := readFile(t, h.w.path)
	f, err := mapping.LoadDraft(h.w.path)
	if err != nil || len(f.Doc.Tests) != 1 || !strings.Contains(first, "user:a") {
		t.Fatalf("first save: %d tests, err %v:\n%s", len(f.Doc.Tests), err, first)
	}
	h.w.doc.Rules[0].Tuples[0].User = "user:b"
	h.cmds = append(h.cmds, h.w.changed())
	h.press("ctrl+s")
	second := readFile(t, h.w.path)
	f, err = mapping.LoadDraft(h.w.path)
	if err != nil || len(f.Doc.Tests) != 1 || !strings.Contains(second, "user:b") || strings.Contains(second, "user:a") {
		t.Fatalf("second save: %d tests, err %v:\n%s", len(f.Doc.Tests), err, second)
	}
	if len(h.w.doc.Tests) != 0 {
		t.Fatalf("the working document took %d generated tests as its own", len(h.w.doc.Tests))
	}
}

func auth0Ws(t *testing.T) *wsHarness {
	t.Helper()
	o := opened{path: filepath.Join(t.TempDir(), "mapping.yaml"), doc: sampleDoc(memberAddedEvent()), auth0: true}
	return newWsHarness(t, o, Options{})
}

func TestAuth0FirstSaveWritesTheStartingModel(t *testing.T) {
	h := auth0Ws(t)
	h.press("ctrl+s")
	model := filepath.Join(filepath.Dir(h.w.path), "model.fga")
	if got := readFile(t, model); got != auth0.Model() {
		t.Fatal("model.fga is not the starting model")
	}
	if h.w.sess.Index.Empty() {
		t.Fatal("the session still has no model")
	}
	want := "wrote model.fga beside it: a starting model to edit, then push it with 'ofga model write'"
	if !strings.Contains(h.toastText(), flat(want)) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestTheStartingModelLeavesAStoreFetchInFlightAlone(t *testing.T) {
	h := auth0Ws(t)
	cancelled := false
	h.w.model.cancel = func() { cancelled = true }
	h.press("ctrl+s")
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.w.path), "model.fga")); err != nil {
		t.Fatalf("model.fga was not written: %v", err)
	}
	if cancelled || !h.w.sess.Index.Empty() {
		t.Fatalf("cancelled = %v, empty = %v; want the fetch left running", cancelled, h.w.sess.Index.Empty())
	}
}

func TestAnExistingModelIsNeverOverwritten(t *testing.T) {
	for _, name := range []string{"model.fga", "model.json"} {
		h := auth0Ws(t)
		model := filepath.Join(filepath.Dir(h.w.path), name)
		writeFile(t, model, "mine")
		h.press("ctrl+s")
		entries, err := os.ReadDir(filepath.Dir(h.w.path))
		if err != nil {
			t.Fatal(err)
		}
		if readFile(t, model) != "mine" || len(entries) != 2 {
			t.Fatalf("%s: model = %q, %d files; want it untouched and no other model", name, readFile(t, model), len(entries))
		}
	}
}

func TestSaveInReadOnlyIsRefused(t *testing.T) {
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - &t {user: \"user:1\", relation: m, object: \"team:1\"}\n"
	path := filepath.Join(t.TempDir(), "ro.yaml")
	writeFile(t, path, src)
	o, err := openPath(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	h := newWsHarness(t, o, Options{})
	h.press("ctrl+s")
	if want := "this file is read-only: uses YAML anchors or aliases; edit it by hand"; !strings.Contains(h.toastText(), flat(want)) {
		t.Fatalf("toast = %q", h.toastText())
	}
	if readFile(t, path) != src {
		t.Fatal("a read-only file was written")
	}
}

func TestQuitPromptSaveThenQuits(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	h.press("q", "s")
	h.run()
	if !h.quit || !strings.Contains(readFile(t, h.w.path), "name: mine") {
		t.Fatalf("quit = %v, or the edit was not saved", h.quit)
	}
}

func TestQuitPromptSaveConflictStays(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	writeFile(t, h.w.path, "version: \"1\"\nrules: []\n")
	h.press("q", "s")
	h.run()
	if h.quit || h.w.overlay == nil {
		t.Fatalf("quit = %v, overlay = %v; want the conflict dialog and no quit", h.quit, h.w.overlay != nil)
	}
	if !strings.Contains(strings.Join(strings.Fields(h.view()), " "), "changed on disk") {
		t.Fatalf("no conflict dialog:\n%s", h.view())
	}
}

func TestTheStartingModelRelintsTheRules(t *testing.T) {
	d := &mapping.Document{Rules: []mapping.Rule{{
		Name: "r", When: `input.type == "organization.member.added"`,
		Tuples: []mapping.Tuple{{User: "user:{{ input.data.object.user.user_id }}", Relation: "member", Object: "organization:{{ input.data.object.organization.id }}"}},
		Sample: &mapping.Sample{Label: "sample", Event: memberAddedEvent()},
	}}}
	o := opened{path: filepath.Join(t.TempDir(), "mapping.yaml"), doc: d, auth0: true}
	h := newWsHarness(t, o, Options{})
	before := len(h.w.findings)
	h.press("ctrl+s")
	h.run()
	if h.w.sess.Index.Empty() {
		t.Fatal("the session has no model")
	}
	for _, f := range h.w.findings {
		if strings.Contains(f.Message, "no model") || strings.Contains(f.Message, "not in the model") {
			t.Fatalf("a model finding survives the starting model: %v (before: %d findings)", f, before)
		}
	}
	if len(h.w.findings) >= before && before > 0 {
		t.Fatalf("findings %d -> %d; Check did not re-run against the new model", before, len(h.w.findings))
	}
}

func TestReloadOfFileBrokenOnDiskEntersLoadError(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	writeFile(t, h.w.path, "version: \"1\"\nrules: [\n")
	h.press("ctrl+s", "r", "y")
	if h.w.loadErr == nil || h.w.dirty || h.w.overlay != nil {
		t.Fatalf("loadErr = %v, dirty = %v, overlay = %v; want load-error mode", h.w.loadErr != nil, h.w.dirty, h.w.overlay != nil)
	}
}

func TestASaveRemembersTheFile(t *testing.T) {
	recent := filepath.Join(filepath.Dir(configtest.Isolate(t)), "mappings-recent.json")
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	h := newWsHarness(t, opened{path: path, doc: sampleDoc(memberAddedEvent())}, Options{RecentFile: recent})
	h.press("ctrl+s")
	if got := loadRecent(recent); len(got) != 1 || got[0] != path {
		t.Fatalf("recent = %v, want [%s]", got, path)
	}
}

// A5: what a save writes and what Check finds are the whole document's,
// whichever rule the Rules pane is on. An earlier version evaluated only the selected
// rule's sample, so the cursor decided which tests the file carried.
func TestSavedTestsDoNotDependOnTheFocusedRule(t *testing.T) {
	deleted := memberAddedEvent()
	deleted["type"] = "organization.member.deleted"
	rule := func(name, event, relation string, sample map[string]any) mapping.Rule {
		return mapping.Rule{
			Name: name, When: `input.type == "` + event + `"`,
			Tuples: []mapping.Tuple{{User: "user:{{ input.data.object.user.user_id }}", Relation: relation,
				Object: "organization:{{ input.data.object.organization.id }}"}},
			Sample: &mapping.Sample{Label: name, Event: sample},
		}
	}
	h := freshWs(t, &mapping.Document{Rules: []mapping.Rule{
		// The model has no owner: a Check finding on the rule the cursor leaves.
		rule("added", "organization.member.added", "owner", memberAddedEvent()),
		rule("deleted", "organization.member.deleted", "member", deleted),
	}})
	h.cmds = append(h.cmds, h.w.modelChanged(mapping.IndexModel(testModel()), "test model", ""))
	h.press("down")
	if r := h.w.focusRule().Rule; r != 1 {
		t.Fatalf("fixture: focus on rule %d, want 2", r+1)
	}
	h.w.runCheck() // what the next debounce does
	onSecond := slices.Clone(h.w.findings)
	if !slices.ContainsFunc(onSecond, func(f mapping.Finding) bool { return f.Rule == 0 && strings.Contains(f.Message, "owner") }) {
		t.Fatalf("fixture: no finding on rule 1, so Check has nothing to lose: %v", onSecond)
	}

	h.press("ctrl+s")
	f, err := mapping.LoadDraft(h.w.path)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []string
	for _, tc := range f.Doc.Tests {
		inputs = append(inputs, fmt.Sprint(tc.Input["type"]))
	}
	slices.Sort(inputs)
	if want := []string{"organization.member.added", "organization.member.deleted"}; !slices.Equal(inputs, want) {
		t.Fatalf("with the cursor on rule 2 the save wrote tests for %q, want %q", inputs, want)
	}
	saved := readFile(t, h.w.path)

	h.press("up")
	if r := h.w.focusRule().Rule; r != 0 {
		t.Fatalf("fixture: focus on rule %d, want 1", r+1)
	}
	h.w.runCheck()
	if !reflect.DeepEqual(h.w.findings, onSecond) {
		t.Fatalf("Check with the cursor on rule 1 = %v, on rule 2 = %v", h.w.findings, onSecond)
	}
	h.press("ctrl+s")
	if !strings.Contains(h.toastText(), flat("nothing to save")) || readFile(t, h.w.path) != saved {
		t.Fatalf("with the cursor on rule 1 the save wrote something else: toast %q", h.toastText())
	}
}

// ctrl+s in a form is the workspace's save, wherever the form is: the field
// never sees it, so it keeps taking keys afterwards.
func TestCtrlSInAFormSavesAndTheFormKeepsTakingKeys(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("enter", "enter") // the first rule's trigger form
	if _, ok := h.topOf(paneRules).(*triggerForm); !ok {
		t.Fatalf("top is %T, want the trigger form", h.topOf(paneRules))
	}
	h.typeText("x")
	h.press("ctrl+s")
	if got := readFile(t, h.w.path); !strings.Contains(got, "name: xmember-added") {
		t.Fatalf("ctrl+s in the form did not save the edit:\n%s", got)
	}
	if _, ok := h.topOf(paneRules).(*triggerForm); !ok {
		t.Fatalf("after the save the top is %T, want the trigger form", h.topOf(paneRules))
	}
	h.typeText("y")
	if got := h.w.doc.Rules[0].Name; got != "xymember-added" {
		t.Fatalf("typing after the save reached the name as %q, want xymember-added", got)
	}
}

// The starting model an Auth0 first save writes does not replace a model the
// session already checks against: the user chose that one.
func TestTheStartingModelLeavesAModelInUseAlone(t *testing.T) {
	h := auth0Ws(t)
	ix := mapping.IndexModel(testModel())
	h.cmds = append(h.cmds, h.w.modelChanged(ix, "store model 01J0 (2 types)", ""))
	h.press("ctrl+s")
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.w.path), "model.fga")); err != nil {
		t.Fatalf("fixture: the first save wrote no starting model, so there was nothing to replace with: %v", err)
	}
	if h.w.sess.Index != ix || h.w.model.label != "store model 01J0 (2 types)" {
		t.Fatalf("model = %q; the starting model replaced the one in use", h.w.model.label)
	}
}

// failLayout makes every save say the layout cannot be kept, as the engine
// does for a document it cannot lay out; no real input is known that does.
func failLayout(t *testing.T) {
	t.Helper()
	old := saveFile
	t.Cleanup(func() { saveFile = old })
	saveFile = func(f *mapping.File, _ *mapping.Document) (bool, error) {
		return false, fmt.Errorf("%s: %w", f.Path, mapping.ErrLayout)
	}
}

func TestALayoutLosingSaveAsks(t *testing.T) {
	configtest.Isolate(t)
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	failLayout(t)
	h.press("ctrl+s")
	v := plain(h.view())
	// The box wraps the question across lines, so look for its parts.
	for _, want := range []string{"Save reformatted", "Could not keep the file's layout (the saved file would", "differ from the edit). Save it reformatted? Comments", "and key order are lost."} {
		if !strings.Contains(v, want) {
			t.Fatalf("no %q:\n%s", want, v)
		}
	}
	for _, key := range []string{"enter", "n"} {
		h.press(key)
		if readFile(t, h.w.path) != twoRules || !h.w.dirty || h.w.overlay != nil {
			t.Fatalf("%s: the file was written, or the document is clean", key)
		}
		h.press("ctrl+s")
	}
	h.press("y")
	h.run()
	got := readFile(t, h.w.path)
	if !strings.Contains(got, "name: mine") || strings.Contains(got, "# Org membership sync.") || h.w.dirty {
		t.Fatalf("dirty = %v, file:\n%s", h.w.dirty, got)
	}
	if !strings.Contains(h.toastText(), flat("saved "+h.w.path)) {
		t.Fatalf("toast = %q", h.toastText())
	}
}

func TestALayoutLosingSaveFromTheQuitPromptQuitsAfterYes(t *testing.T) {
	configtest.Isolate(t)
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	failLayout(t)
	h.press("q", "s")
	h.run()
	if h.quit || !strings.Contains(plain(h.view()), "Save reformatted") {
		t.Fatalf("quit = %v, no confirm", h.quit)
	}
	h.press("esc")
	h.run()
	if h.quit || !h.w.dirty {
		t.Fatalf("quit = %v, dirty = %v after declining", h.quit, h.w.dirty)
	}
	h.press("q", "s", "y")
	h.run()
	if !h.quit || !strings.Contains(readFile(t, h.w.path), "name: mine") {
		t.Fatalf("quit = %v, or the edit was not saved", h.quit)
	}
}

// forkWithInput opens the add-rule fork on the Rules pane and picks an event
// (its recipe), so it holds something to lose.
func forkWithInput(t *testing.T, h *wsHarness) {
	t.Helper()
	h.press("a", "down", "down", "enter")
	if !h.w.stacks[paneRules].holdsNewRule() {
		t.Fatalf("no fork with a picked event: depth %d\n%s", h.w.stacks[paneRules].Depth(), h.view())
	}
}

func TestAJumpAsksBeforeDroppingANewRule(t *testing.T) {
	h := loadedWs(t, twoRules)
	forkWithInput(t, h)
	depth := h.w.stacks[paneRules].Depth()
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	if h.w.overlay == nil || !strings.Contains(h.view(), "Drop the rule you were adding?") {
		t.Fatalf("no question:\n%s", h.view())
	}
	for _, k := range []string{"n", "esc", "enter"} {
		h.press(k)
		if h.w.overlay != nil || h.w.stacks[paneRules].Depth() != depth {
			t.Fatalf("%s: overlay = %v, depth = %d, want the fork intact at %d", k, h.w.overlay != nil, h.w.stacks[paneRules].Depth(), depth)
		}
		h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	}
	h.press("y")
	h.run()
	if _, ok := h.topOf(paneRules).(*tupleList); !ok || h.w.overlay != nil {
		t.Fatalf("y: top is %T, overlay = %v; want the tuple editor", h.topOf(paneRules), h.w.overlay != nil)
	}
}

func TestAPasteBoxWithTextCountsAsANewRule(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("a", "enter")
	if h.w.stacks[paneRules].holdsNewRule() {
		t.Fatal("an empty paste box holds a rule")
	}
	h.typeText("{")
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	if h.w.overlay == nil {
		t.Fatal("no question over a paste box with text")
	}
}

// ctrl+e on a rule that exists uses the same paste box. No rule is being
// added there, so a jump asks about the pasted text, not about a rule.
func TestAJumpAsksBeforeDroppingAnExistingRulesPaste(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.press("enter", "enter", "ctrl+e") // the first rule's trigger, then its event
	pick, ok := h.topOf(paneRules).(*eventPick)
	if !ok || !pick.list.SelectID(pasteID) {
		t.Fatalf("top is %T, want the event pick with a paste row", h.topOf(paneRules))
	}
	h.press("enter")
	h.typeText("{")
	paste, ok := h.topOf(paneRules).(*eventPaste)
	if !ok {
		t.Fatalf("top is %T, want the paste box", h.topOf(paneRules))
	}
	if h.w.stacks[paneRules].holdsNewRule() {
		t.Fatal("an existing rule's paste counts as a new rule")
	}
	depth := h.w.stacks[paneRules].Depth()
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	view := plain(h.view())
	if h.w.overlay == nil || !strings.Contains(view, "Discard the pasted text?") || strings.Contains(view, "Drop the rule") {
		t.Fatalf("no paste question:\n%s", view)
	}
	h.press("n")
	if h.w.overlay != nil || h.topOf(paneRules) != paste || paste.area.Value() != "{" || h.w.stacks[paneRules].Depth() != depth {
		t.Fatalf("n: overlay = %v, top = %T, paste = %q", h.w.overlay != nil, h.topOf(paneRules), paste.area.Value())
	}
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	h.press("y")
	h.run()
	if _, ok := h.topOf(paneRules).(*tupleList); !ok || h.w.overlay != nil {
		t.Fatalf("y: top is %T, overlay = %v; want the tuple editor", h.topOf(paneRules), h.w.overlay != nil)
	}
}

func TestAJumpWithoutAForkGoesStraightThere(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.cmds = append(h.cmds, h.w.jump(Jump{Rule: 1, Section: "tuple"}))
	if h.w.overlay != nil {
		t.Fatalf("a question with nothing to drop:\n%s", h.view())
	}
	if _, ok := h.topOf(paneRules).(*tupleList); !ok {
		t.Fatalf("top is %T, want the tuple editor", h.topOf(paneRules))
	}
}

// No save keeps a rule being added, so quitting asks even when the document is
// clean, and n leaves the fork where it was.
func TestQuitAsksBeforeDroppingANewRule(t *testing.T) {
	h := loadedWs(t, twoRules)
	forkWithInput(t, h)
	depth := h.w.stacks[paneRules].Depth()
	for _, k := range []string{"n", "esc"} {
		h.press("ctrl+c")
		h.run()
		if h.quit || h.w.overlay == nil || !strings.Contains(plain(h.view()), "Drop the rule you were adding?") {
			t.Fatalf("quit = %v, no question:\n%s", h.quit, h.view())
		}
		h.press(k)
		h.run()
		if h.quit || h.w.overlay != nil || h.w.stacks[paneRules].Depth() != depth {
			t.Fatalf("%s: quit = %v, overlay = %v, depth = %d", k, h.quit, h.w.overlay != nil, h.w.stacks[paneRules].Depth())
		}
	}
	h.press("ctrl+c", "y")
	h.run()
	if !h.quit {
		t.Fatal("y did not quit")
	}
}

// A paste box on a pane not shown counts too, and on an edited document the
// save question follows: saving the document does not keep the paste.
func TestQuitAsksAboutAPasteOnAnyPaneThenAboutTheEdits(t *testing.T) {
	h := loadedWs(t, twoRules)
	h.rename(0, "mine")
	h.press("3", "a")
	pick, ok := h.topOf(paneTests).(*eventPick)
	if !ok || !pick.list.SelectID(pasteID) {
		t.Fatalf("top is %T, want the event pick with a paste row", h.topOf(paneTests))
	}
	h.press("enter")
	h.typeText("{")
	h.cmds = append(h.cmds, h.w.switchTo(paneRules))
	h.run()
	h.press("ctrl+c")
	h.run()
	if h.quit || !strings.Contains(plain(h.view()), "Discard the pasted text?") {
		t.Fatalf("quit = %v, no paste question:\n%s", h.quit, h.view())
	}
	h.press("y")
	h.run()
	if h.quit || !strings.Contains(plain(h.view()), "Unsaved edits") {
		t.Fatalf("quit = %v, no save question:\n%s", h.quit, h.view())
	}
	h.press("d")
	h.run()
	if !h.quit {
		t.Fatal("d did not quit")
	}
}
