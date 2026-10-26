package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

const miniMapping = "version: \"1\"\nrules:\n  - name: one\n  - name: two\n"

// startHarness drives the start screen at 120×40, on a folder of its own.
type startHarness struct {
	t   *testing.T
	s   *startScreen
	dir string
	rec string
}

func newStartHarness(t *testing.T, files map[string]string) *startHarness {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		writeFile(t, p, content)
	}
	rec := filepath.Join(t.TempDir(), "mappings-recent.json")
	h := &startHarness{t: t, dir: dir, rec: rec}
	h.s = newStartScreen(context.Background(), Options{Dir: dir, RecentFile: rec})
	h.s.Init()
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	return h
}

func (h *startHarness) send(msg tea.Msg) { h.s.Update(msg) }

func (h *startHarness) press(keys ...string) {
	for _, k := range keys {
		h.send(wsKey(k))
	}
}

func (h *startHarness) typeText(s string) {
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (h *startHarness) view() string { return plain(h.s.View()) }

func (h *startHarness) selected() string {
	it, _ := h.s.st.items[0].(*start).list.Selected()
	return it.TitleText
}

// choose moves the cursor to the row whose title contains want.
func (h *startHarness) choose(want string) {
	h.t.Helper()
	l := h.s.st.items[0].(*start).list
	for i := range 40 {
		l.SelectIndex(i)
		if it, ok := l.Selected(); ok && strings.Contains(it.TitleText, want) {
			return
		}
	}
	h.t.Fatalf("no row contains %q", want)
}

func TestStartListsSections(t *testing.T) {
	h := newStartHarness(t, map[string]string{
		"recent.yaml":   miniMapping,
		"found.yaml":    miniMapping + "  - name: three\n",
		"other.yaml":    "name: not a mapping\n",
		"sub/deep.yaml": "version: \"1\"\nrules: []\n",
	})
	if err := rememberRecent(h.rec, filepath.Join(h.dir, "recent.yaml")); err != nil {
		t.Fatal(err)
	}
	h.s = newStartScreen(context.Background(), Options{Dir: h.dir, RecentFile: h.rec})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	v := h.view()
	order := []string{"RECENT", "recent.yaml · 2 rules", "FOUND HERE", "found.yaml · 3 rules",
		"sub/deep.yaml · 0 rules", "NEW", "New from Auth0 events", "New blank mapping"}
	at := 0
	for _, want := range order {
		i := strings.Index(v[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after offset %d:\n%s", want, at, v)
		}
		at += i + len(want)
	}
	if strings.Count(v, "recent.yaml") != 1 {
		t.Fatalf("a recent file is also listed as found:\n%s", v)
	}
	if strings.Contains(v, "other.yaml") {
		t.Fatalf("a YAML file that is not a mapping is listed:\n%s", v)
	}
	if !strings.Contains(h.selected(), "recent.yaml") {
		t.Fatalf("the cursor starts on %q, not on a file", h.selected())
	}
	for _, k := range append(slices.Repeat([]string{"down"}, 8), slices.Repeat([]string{"up"}, 8)...) {
		h.press(k)
		if sel := h.selected(); sel == "" || strings.Contains(sel, "RECENT") || strings.Contains(sel, "FOUND") || strings.Contains(sel, "NEW") {
			t.Fatalf("after %s the cursor is on a heading: %q", k, sel)
		}
	}
}

func TestRecentPath(t *testing.T) {
	dir, home := filepath.FromSlash("/work/proj"), filepath.FromSlash("/home/me")
	for _, tc := range []struct{ path, want string }{
		{"/work/proj/sub/m.yaml", "sub/m.yaml"},
		{"/home/me/maps/m.yaml", "~/maps/m.yaml"},
		{"/work/proj2/m.yaml", "/work/proj2/m.yaml"},
		{"/srv/m.yaml", "/srv/m.yaml"},
		{"/srv/m\x1b[2J.yaml", "/srv/m.yaml"},
	} {
		if got := recentPath(filepath.FromSlash(tc.path), dir, home); got != filepath.FromSlash(tc.want) {
			t.Errorf("recentPath(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
	if got := recentPath(filepath.FromSlash("/home/me/m.yaml"), dir, ""); got != filepath.FromSlash("/home/me/m.yaml") {
		t.Errorf("without a home folder: %q", got)
	}
}

func TestRecentRowKeepsTheNameAndCount(t *testing.T) {
	shown := filepath.FromSlash("/var/folders/n6/T/proj/m.yaml")
	for _, tc := range []struct {
		w      int
		mapped bool
		want   string
	}{
		{0, true, "/var/folders/n6/T/proj/m.yaml · 2 rules"},
		{80, true, "/var/folders/n6/T/proj/m.yaml · 2 rules"},
		{24, true, "…/proj/m.yaml · 2 rules"},
		{5, true, "…/m.yaml · 2 rules"},
		{14, false, "…/proj/m.yaml"},
	} {
		if got := recentRow(shown, 2, tc.mapped, tc.w); got != filepath.FromSlash(tc.want) {
			t.Errorf("recentRow(w=%d, mapped=%v) = %q, want %q", tc.w, tc.mapped, got, tc.want)
		}
	}
}

// A recent file deep in another folder keeps its name and rule count on screen,
// at the first width and after a resize, and the cursor stays on it.
func TestStartFitsALongRecentPath(t *testing.T) {
	h := newStartHarness(t, nil)
	deep := filepath.Join(t.TempDir(), strings.Repeat("a-rather-long-folder-name/", 6), "deep.yaml")
	if err := os.MkdirAll(filepath.Dir(deep), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, deep, miniMapping)
	if err := rememberRecent(h.rec, deep); err != nil {
		t.Fatal(err)
	}
	h.s = newStartScreen(context.Background(), Options{Dir: h.dir, RecentFile: h.rec})
	for _, w := range []int{120, 90} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 40})
		if v := h.view(); !strings.Contains(v, "┃ …") || !strings.Contains(v, string(filepath.Separator)+"deep.yaml · 2 rules") {
			t.Fatalf("at width %d the row lost its name or count:\n%s", w, v)
		}
		if !strings.Contains(h.selected(), "deep.yaml") {
			t.Fatalf("at width %d the cursor is on %q", w, h.selected())
		}
	}
}

func TestStartOpensAFile(t *testing.T) {
	h := newStartHarness(t, map[string]string{"a.yaml": miniMapping})
	h.press("enter")
	if h.s.chosen == nil || h.s.chosen.path != filepath.Join(h.dir, "a.yaml") || h.s.chosen.file == nil {
		t.Fatalf("chosen = %+v", h.s.chosen)
	}
	if got := loadRecent(h.rec); len(got) != 1 || got[0] != h.s.chosen.path {
		t.Fatalf("recent = %v", got)
	}
}

func TestStartOpensABrokenFileInLoadErrorMode(t *testing.T) {
	h := newStartHarness(t, map[string]string{"a.yaml": miniMapping})
	if err := rememberRecent(h.rec, filepath.Join(h.dir, "a.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.dir, "a.yaml"), brokenYAML)
	h.s = newStartScreen(context.Background(), Options{Dir: h.dir, RecentFile: h.rec})
	h.press("enter")
	if h.s.chosen == nil || h.s.chosen.loadErr == nil {
		t.Fatalf("chosen = %+v, want load-error mode", h.s.chosen)
	}
}

func TestStartMissingFileIsAToastAndLeavesRecent(t *testing.T) {
	h := newStartHarness(t, map[string]string{"a.yaml": miniMapping, "b.yaml": miniMapping})
	for _, n := range []string{"a.yaml", "b.yaml"} {
		if err := rememberRecent(h.rec, filepath.Join(h.dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	h.s = newStartScreen(context.Background(), Options{Dir: h.dir, RecentFile: h.rec})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	if err := os.Remove(filepath.Join(h.dir, "b.yaml")); err != nil {
		t.Fatal(err)
	}
	h.press("enter") // b.yaml, the newest
	if h.s.chosen != nil {
		t.Fatal("a missing file opened")
	}
	v := h.view()
	if !strings.Contains(v, "b.yaml is gone") || strings.Contains(v, "b.yaml ·") {
		t.Fatalf("want a toast and no b.yaml row:\n%s", v)
	}
	// Named as its row was: a temporary folder's path wraps the toast.
	if strings.Contains(v, string(filepath.Separator)+"b.yaml") {
		t.Fatalf("the toast names the whole path:\n%s", v)
	}
}

func TestStartNewBlank(t *testing.T) {
	h := newStartHarness(t, nil)
	h.choose("New blank mapping")
	h.press("enter")
	if _, ok := h.s.st.top().(*nameFile); !ok {
		t.Fatalf("top is %T, want the file name", h.s.st.top())
	}
	h.press("enter")
	o := h.s.chosen
	if o == nil || o.path != filepath.Join(h.dir, "mapping.yaml") || o.file != nil || o.auth0 {
		t.Fatalf("chosen = %+v", o)
	}
	if o.doc == nil || len(o.doc.Rules) != 1 {
		t.Fatalf("doc = %+v, want one empty rule", o.doc)
	}
	ws := newWorkspace(context.Background(), Options{}, *o)
	if !ws.fresh || !ws.dirty {
		t.Fatalf("fresh = %v, dirty = %v", ws.fresh, ws.dirty)
	}
}

func TestStartNewFromAuth0(t *testing.T) {
	h := newStartHarness(t, nil)
	h.choose("New from Auth0 events")
	h.press("enter")
	h.press("/")
	h.typeText("user.created")
	h.press("enter", "enter")
	if _, ok := h.s.st.top().(*recipeView); !ok {
		t.Fatalf("top is %T, want the recipe", h.s.st.top())
	}
	h.press("enter")
	if _, ok := h.s.st.top().(*nameFile); !ok {
		t.Fatalf("top is %T, want the file name", h.s.st.top())
	}
	h.press("enter")
	o := h.s.chosen
	if o == nil || !o.auth0 || o.file != nil || o.path != filepath.Join(h.dir, "mapping.yaml") {
		t.Fatalf("chosen = %+v", o)
	}
	if o.doc == nil || len(o.doc.Rules) == 0 || o.doc.Rules[0].Sample == nil {
		t.Fatalf("doc = %+v, want the recipe's rules", o.doc)
	}
	ws := newWorkspace(context.Background(), Options{}, *o)
	if !ws.fresh || !ws.dirty || !ws.auth0 || len(ws.doc.Rules) != len(o.doc.Rules) {
		t.Fatalf("fresh = %v, dirty = %v, auth0 = %v", ws.fresh, ws.dirty, ws.auth0)
	}
}

func TestStartEscInTheForkEmptiesTheDocument(t *testing.T) {
	h := newStartHarness(t, nil)
	h.choose("New from Auth0 events")
	h.press("enter")
	h.press("/")
	h.typeText("user.created")
	h.press("enter", "enter")
	h.press("esc", "esc") // recipe back to the pick, then out of the fork
	c := h.s.st.items[0].(*start)
	if h.s.st.Depth() != 1 || h.s.quit || h.s.chosen != nil || len(c.s.Doc.Rules) != 0 {
		t.Fatalf("depth = %d, quit = %v, rules = %d", h.s.st.Depth(), h.s.quit, len(c.s.Doc.Rules))
	}
	// And a cancelled name after a finished fork empties it too.
	h.choose("New from Auth0 events")
	h.press("enter", "/")
	h.typeText("user.created")
	h.press("enter", "enter", "enter", "esc")
	if h.s.st.Depth() != 1 || len(c.s.Doc.Rules) != 0 {
		t.Fatalf("depth = %d, rules = %d", h.s.st.Depth(), len(c.s.Doc.Rules))
	}
}

func TestStartRecipeModelKeyIsAToast(t *testing.T) {
	h := newStartHarness(t, nil)
	h.choose("New from Auth0 events")
	h.press("enter", "/")
	h.typeText("user.created")
	h.press("enter", "enter", "m")
	if !strings.Contains(h.view(), "model is chosen once") {
		t.Fatalf("no toast:\n%s", h.view())
	}
}

func TestStartQuits(t *testing.T) {
	for _, k := range []string{"esc", "q", "ctrl+c"} {
		h := newStartHarness(t, nil)
		h.press(k)
		if !h.s.quit {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestStartBrowsesWithO(t *testing.T) {
	h := newStartHarness(t, map[string]string{"sub/x.yaml": miniMapping})
	h.press("o")
	if _, ok := h.s.st.top().(*fileBrowse); !ok {
		t.Fatalf("top is %T", h.s.st.top())
	}
	h.press("down", "enter") // sub/, under ../
	h.press("down", "enter") // x.yaml, under ../
	if h.s.chosen == nil || h.s.chosen.path != filepath.Join(h.dir, "sub", "x.yaml") {
		t.Fatalf("chosen = %+v", h.s.chosen)
	}
}

func TestStartRowsCannotDriveTheTerminal(t *testing.T) {
	evil := "evil\x1b]0;pwned\x07.yaml"
	h := newStartHarness(t, map[string]string{evil: miniMapping})
	rec := filepath.Join(h.dir, "r\x1b]0;pwned\x07ecent.yaml")
	writeFile(t, rec, miniMapping)
	if err := rememberRecent(h.rec, rec); err != nil {
		t.Fatal(err)
	}
	h.s = newStartScreen(context.Background(), Options{Dir: h.dir, RecentFile: h.rec})
	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})
	raw := h.s.View()
	if strings.Contains(raw, "\x1b]") || strings.Contains(raw, "\x07") {
		t.Fatalf("a file name reached the terminal raw: %q", raw)
	}
	if !strings.Contains(raw, "evil") {
		t.Fatalf("the found row is not drawn:\n%s", plain(raw))
	}
}

func TestStartHonoursTheMinimumSize(t *testing.T) {
	h := newStartHarness(t, nil)
	h.send(tea.WindowSizeMsg{Width: 50, Height: 10})
	if !strings.Contains(h.view(), "terminal too small") {
		t.Fatal("no size notice")
	}
	h.send(tea.WindowSizeMsg{Width: 60, Height: 16})
	if n := strings.Count(h.s.View(), "\n") + 1; n > 16 {
		t.Fatalf("drew %d lines in 16", n)
	}
}

func TestNewAppWithAMissingPathOpensAFreshMapping(t *testing.T) {
	recent := filepath.Join(filepath.Dir(configtest.Isolate(t)), "mappings-recent.json")
	path := filepath.Join(t.TempDir(), "nope.yaml")
	a, err := NewApp(context.Background(), Options{Path: path, RecentFile: recent})
	if err != nil || a.ws == nil {
		t.Fatalf("err = %v, ws = %v", err, a.ws)
	}
	w := a.ws
	if !w.fresh || !w.dirty || w.path != path || len(w.doc.Rules) != 1 {
		t.Fatalf("fresh = %v, dirty = %v, path = %q, rules = %d", w.fresh, w.dirty, w.path, len(w.doc.Rules))
	}
	if got := loadRecent(recent); len(got) != 0 {
		t.Fatalf("an unsaved path was remembered: %v", got)
	}
	w.Update(wsKey("ctrl+s"))
	if _, err := os.Stat(path); err != nil || w.fresh {
		t.Fatalf("the first save did not create the file: stat err = %v, fresh = %v", err, w.fresh)
	}
	if got := loadRecent(recent); len(got) != 1 {
		t.Fatalf("recent after the save = %v", got)
	}
}

func TestAMissingAncestorIsNamed(t *testing.T) {
	_, err := NewApp(context.Background(), Options{Path: filepath.Join(t.TempDir(), "nope", "a", "x.yaml")})
	if err == nil || clierr.Code(err) != clierr.CodeUsage || !strings.HasPrefix(err.Error(), "nope does not exist") {
		t.Fatalf("code = %d, err = %v", clierr.Code(err), err)
	}
}

func TestAParentThatIsAFileIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a"), "x")
	for _, path := range []string{filepath.Join(dir, "a", "x.yaml"), filepath.Join(dir, "a", "b", "x.yaml")} {
		_, err := NewApp(context.Background(), Options{Path: path})
		if err == nil || clierr.Code(err) != clierr.CodeUsage || err.Error() != "a is a file, not a folder" {
			t.Fatalf("%s: code = %d, err = %v", path, clierr.Code(err), err)
		}
	}
}

func TestABrokenSymlinkIsAUsageError(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "x.yaml")
	gone := filepath.Join(dir, "gone.yaml")
	if err := os.Symlink(gone, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	_, err := NewApp(context.Background(), Options{Path: link})
	if err == nil || clierr.Code(err) != clierr.CodeUsage || err.Error() != "x.yaml is a broken symlink to "+gone {
		t.Fatalf("code = %d, err = %v", clierr.Code(err), err)
	}
}

func TestANewPathInAnExistingFolderOpensFresh(t *testing.T) {
	a, err := NewApp(context.Background(), Options{Path: filepath.Join(t.TempDir(), "new.yaml")})
	if err != nil || a.ws == nil {
		t.Fatalf("a = %v, err = %v", a, err)
	}
}

func TestAppSwitchesToTheWorkspaceAndPassesItsCommandsOn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), miniMapping)
	a, err := NewApp(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a.Update(wsKey("enter"))
	if a.ws == nil || a.start != nil {
		t.Fatal("the workspace did not open")
	}
	if a.ws.width != 120 || a.ws.height != 40 {
		t.Fatalf("the workspace was not told the size: %dx%d", a.ws.width, a.ws.height)
	}
	if v := a.View(); !v.AltScreen {
		t.Fatal("not on the alt screen")
	}
}

func TestNewAppWithAPathOpensTheWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.yaml")
	writeFile(t, path, miniMapping)
	a, err := NewApp(context.Background(), Options{Path: path})
	if err != nil || a.ws == nil {
		t.Fatalf("err = %v, ws = %v", err, a.ws)
	}
	if a.ws.loadErr != nil || a.ws.sess.ReadOnly || a.ws.file == nil {
		t.Fatalf("a plain file should open editable: loadErr = %v, readOnly = %v", a.ws.loadErr, a.ws.sess.ReadOnly)
	}

	bad := filepath.Join(t.TempDir(), "bad.yaml")
	writeFile(t, bad, brokenYAML)
	a, err = NewApp(context.Background(), Options{Path: bad})
	if err != nil || a.ws.loadErr == nil {
		t.Fatalf("broken YAML should open in load-error mode: err = %v", err)
	}
}

func TestAppKeepsStartToastsForTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), miniMapping)
	a, _ := NewApp(context.Background(), Options{Dir: dir})
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a.start.toasts.Push(0, "from the start screen")
	a.Update(wsKey("enter"))
	if !a.ws.toasts.Active() {
		t.Fatal("the start screen's toast was dropped with it")
	}
}
