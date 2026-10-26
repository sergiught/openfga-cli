package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("model\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func browseTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "model.fga"))
	touch(t, filepath.Join(dir, "other.json"))
	touch(t, filepath.Join(dir, "notes.md"))
	touch(t, filepath.Join(dir, ".hidden.fga"))
	if err := os.MkdirAll(filepath.Join(dir, "sub", "deep"), 0o750); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(dir, "sub", "inner.fga"))
	return dir
}

func TestDirItemsOrderAndFilter(t *testing.T) {
	dir := browseTree(t)
	items, err := DirItems(dir, []string{".fga"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.TitleText)
	}
	if want := "../ sub/ model.fga"; strings.Join(got, " ") != want {
		t.Fatalf("items = %v, want %s", got, want)
	}
	if items[0].ID != filepath.Dir(dir) || items[2].ID != filepath.Join(dir, "model.fga") {
		t.Fatalf("IDs are not absolute paths: %v", items)
	}

	root, err := DirItems(string(filepath.Separator), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range root {
		if it.TitleText == "../" {
			t.Fatal("the filesystem root offers a parent")
		}
	}
}

func openBrowse(dir string) func(s *Session) component {
	return func(s *Session) component {
		return newFileBrowse(s, "Open", dir, []string{".fga", ".json"})
	}
}

func selectRow(t *testing.T, c *fileBrowse, title string) {
	t.Helper()
	for i := range 20 {
		c.list.SelectIndex(i)
		if it, ok := c.list.Selected(); ok && it.TitleText == title {
			return
		}
	}
	t.Fatalf("no row %q", title)
}

// os.ReadDir reports a link as a link, so a linked folder needs its target
// to show as a folder; its ID keeps the link, as the user named it.
func TestDirItemsListALinkedFolderAsAFolder(t *testing.T) {
	dir := browseTree(t)
	shared := t.TempDir()
	if err := os.Symlink(shared, filepath.Join(dir, "linked")); err != nil {
		t.Skip("no symlinks:", err)
	}
	items, err := DirItems(dir, []string{".fga"})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.TitleText == "linked/" {
			if it.ID != filepath.Join(dir, "linked") {
				t.Fatalf("ID = %q, want the link", it.ID)
			}
			return
		}
	}
	t.Fatalf("the linked folder is missing: %v", items)
}

func TestFileBrowseWalksAndPicks(t *testing.T) {
	dir := browseTree(t)
	h := newHarness(t, nil, nil, openBrowse(dir))
	c := topAs[*fileBrowse](t, h)

	selectRow(t, c, "sub/")
	h.press("enter")
	if h.closed || c.dir != filepath.Join(dir, "sub") {
		t.Fatalf("enter on a folder should walk in: dir = %q, closed = %v", c.dir, h.closed)
	}
	if !strings.Contains(plain(h.view(60, 10)), "inner.fga") {
		t.Fatal("the walked-into folder is not listed")
	}
	selectRow(t, c, "../")
	h.press("enter")
	if c.dir != dir {
		t.Fatalf("../ went to %q", c.dir)
	}
	selectRow(t, c, "model.fga")
	h.press("enter")
	if !h.closed || h.result != filepath.Join(dir, "model.fga") {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestFileBrowseEscClosesWithNothing(t *testing.T) {
	h := newHarness(t, nil, nil, openBrowse(browseTree(t)))
	h.press("esc")
	if !h.closed || h.result != nil {
		t.Fatalf("closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestFileBrowseTypedPath(t *testing.T) {
	dir := browseTree(t)
	h := newHarness(t, nil, nil, openBrowse(dir))
	c := topAs[*fileBrowse](t, h)

	h.press("ctrl+p")
	if !c.typing() {
		t.Fatal("ctrl+p should take text")
	}
	if h.press("esc"); c.typing() || h.closed {
		t.Fatalf("esc while typing should return to the list: typing = %v, closed = %v", c.typing(), h.closed)
	}

	h.press("ctrl+p")
	h.typeText("sub")
	h.press("enter")
	if c.typing() || c.dir != filepath.Join(dir, "sub") {
		t.Fatalf("a typed folder should be walked into: dir = %q", c.dir)
	}

	h.press("ctrl+p")
	h.typeText(filepath.Join(dir, ".hidden.fga"))
	h.press("enter")
	if !h.closed || h.result != filepath.Join(dir, ".hidden.fga") {
		t.Fatalf("a typed file should close with it: closed = %v, result = %#v", h.closed, h.result)
	}
}

func TestFileRowsCannotDriveTheTerminal(t *testing.T) {
	dir := t.TempDir()
	touch(t, filepath.Join(dir, "evil\x1b]0;pwned\x07.fga"))
	h := newHarness(t, nil, nil, openBrowse(dir))
	view := h.view(60, 10)
	if strings.Contains(view, "\x1b]") || strings.Contains(view, "\x07") {
		t.Fatalf("a file name reached the terminal raw: %q", view)
	}
}

func TestFileBrowseFitsTheHeightItIsGiven(t *testing.T) {
	dir := t.TempDir()
	for i := range 30 {
		touch(t, filepath.Join(dir, strings.Repeat("a", i+1)+".fga"))
	}
	h := newHarness(t, nil, nil, openBrowse(dir))
	for _, ht := range []int{1, 2, 5, 12} {
		if n := len(strings.Split(h.view(40, ht), "\n")); n > ht {
			t.Fatalf("height %d drew %d lines", ht, n)
		}
	}
}
