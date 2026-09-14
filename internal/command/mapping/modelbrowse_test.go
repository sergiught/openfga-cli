package mapping

import (
	"os"
	"path/filepath"
	"testing"

	"charm.land/lipgloss/v2"
)

// browseAt runs the wizard from dir and chooses the file source, which is what
// opens the browser.
func browseAt(t *testing.T, dir string) *wizardModel {
	t.Helper()
	t.Chdir(dir)
	m := atModelSource(t, nil)
	selectSource(t, m, "file")
	send(m, key("enter"))
	if m.top() != screenModelBrowse {
		t.Fatalf("the file source should open the browser: top = %v", m.top())
	}
	return m
}

// selectRow moves to a row and checks it is the one the test means, so a test
// that drives by position says out loud what it expects to find there.
func selectRow(t *testing.T, m *wizardModel, i int, wantTitle string) {
	t.Helper()
	m.modelFiles.SelectIndex(i)
	it, ok := m.modelFiles.Selected()
	if !ok {
		t.Fatalf("row %d does not exist", i)
	}
	if it.TitleText != wantTitle {
		t.Fatalf("row %d is %q, want %q", i, it.TitleText, wantTitle)
	}
}

func writeModel(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("model\n  schema 1.1\ntype user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The browser offers the way up, the folders to walk into, and the files a
// model can be in — and nothing else. A listing that also carried README.md and
// .git would be a file manager, which is not what the user came here to use.
func TestModelDirItemsOffersFoldersAndModelsOnly(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, filepath.Join(dir, "model.fga"))
	writeModel(t, filepath.Join(dir, "mapping.json"))
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("no"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}

	items, err := modelDirItems(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.TitleText)
	}
	// Folders before files, each group as ReadDir sorted it.
	want := []string{"../", "sub/", "mapping.json", "model.fga"}
	if len(got) != len(want) {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("listing = %q, want %q", got, want)
		}
	}
}

// Walking down into a folder and back up, then loading what is there. The
// wizard leaves the model question entirely on a load: the user asked for a
// model, not for a file browser, and the browser is only how they answered.
func TestModelBrowseWalksIntoAFolderAndLoadsTheModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeModel(t, filepath.Join(dir, "sub", "model.fga"))

	m := browseAt(t, dir)

	selectRow(t, m, 1, "sub/")
	send(m, key("enter"))
	selectRow(t, m, 1, "model.fga")

	// Up and back down, because a browser you cannot leave a wrong turn in is
	// worse than a text field.
	selectRow(t, m, 0, "../")
	send(m, key("enter"))
	selectRow(t, m, 1, "sub/")
	send(m, key("enter"))

	selectRow(t, m, 1, "model.fga")
	send(m, key("enter"))
	if m.top() != screenRules {
		t.Fatalf("loading a model should leave the model screens: top = %v", m.top())
	}
	if m.index.Empty() {
		t.Fatal("model was not indexed")
	}
}

// A file that is not a model leaves the user on the listing with the reason,
// one keystroke from trying the next row.
func TestModelBrowseKeepsTheListingWhenTheFileIsNotAModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.fga"), []byte("not a model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := browseAt(t, dir)
	selectRow(t, m, 1, "model.fga")
	send(m, key("enter"))

	if m.top() != screenModelBrowse {
		t.Fatalf("a bad model must keep the listing: top = %v", m.top())
	}
	if m.errMsg == "" {
		t.Fatalf("expected an error on the listing:\n%s", m.viewString())
	}
}

// The typed field is now three screens deep, and it used to pop a fixed two.
// Loading from it must still land where the model was asked for rather than on
// the browser the user typed their way out of.
func TestTypedPathFromTheBrowserLeavesEveryModelScreen(t *testing.T) {
	dir := t.TempDir()
	writeModel(t, filepath.Join(dir, "model.fga"))

	m := browseAt(t, dir)
	send(m, key("ctrl+p"))
	if m.top() != screenModelFile {
		t.Fatalf("^p should open the path field: top = %v", m.top())
	}

	m.modelPath.SetValues([]string{filepath.Join(dir, "model.fga")})
	send(m, key("enter"))
	if m.top() != screenRules {
		t.Fatalf("top = %v, want the screen that asked for the model", m.top())
	}
	if m.index.Empty() {
		t.Fatal("model was not indexed")
	}
}

// The header says which directory the rows came from, and a long one keeps the
// end: the last segments are the ones that say where you are.
func TestPathTailKeepsTheEnd(t *testing.T) {
	const p = "/home/someone/Projects/openfga-cli/internal/command"
	if got := pathTail(p, 80); got != p {
		t.Fatalf("a path that fits should be left alone: %q", got)
	}
	got := pathTail(p, 30)
	if lipgloss.Width(got) > 30 {
		t.Fatalf("pathTail(%q, 30) = %q, too wide", p, got)
	}
	if got[len(got)-len("command"):] != "command" {
		t.Fatalf("pathTail dropped the end: %q", got)
	}
}
