package mapping

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSaveMergesIntoTheOriginal(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(d *Document)
		golden string
	}{
		{"no-change", func(d *Document) {}, ""}, // "" = Render reports changed=false
		{"relation", func(d *Document) { d.Rules[0].Tuples[0].Relation = "admin" }, "relation.golden.yaml"},
		{"add-rule", func(d *Document) {
			d.Rules = append(d.Rules, Rule{Name: "new", When: `input.type == "x"`,
				Tuples: []Tuple{{User: "user:1", Relation: "member", Object: "team:1"}}})
		}, "add-rule.golden.yaml"},
		{"rename-rule", func(d *Document) { d.Rules[0].Name = "joined" }, "rename-rule.golden.yaml"},
		{"delete-rule", func(d *Document) { d.Rules = d.Rules[1:] }, "delete-rule.golden.yaml"},
		{"delete-rename", func(d *Document) {
			d.Rules = d.Rules[1:]
			d.Rules[0].Name = "gone"
		}, "delete-rename.golden.yaml"},
		{"rename-reorder", func(d *Document) {
			d.Rules[0].Name = "joined"
			d.Rules[0], d.Rules[1] = d.Rules[1], d.Rules[0]
		}, "rename-reorder.golden.yaml"},
		{"delete-tests", func(d *Document) { d.Tests = nil }, "delete-tests.golden.yaml"},
		{"add-variable", func(d *Document) {
			d.Rules[0].Variables = []Variable{{Name: "org", Expr: "input.data.object.organization.id"}}
		}, "add-variable.golden.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Load("testdata/load/commented.yaml")
			if err != nil {
				t.Fatal(err)
			}
			tc.edit(f.Doc)
			out, changed, err := f.Render(f.Doc)
			if err != nil {
				t.Fatal(err)
			}
			if tc.golden == "" {
				if changed {
					t.Fatalf("no edit reported as a change:\n%s", out)
				}
				return
			}
			if !changed {
				t.Fatal("edit not reported as a change")
			}
			want, err := os.ReadFile("testdata/save/" + tc.golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != string(want) {
				t.Fatalf("got:\n%s\nwant:\n%s", out, want)
			}
			// The output must still be a valid mapping with the edit applied.
			if _, err := LoadBytes("out.yaml", out); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSaveRestoresCRLFAndBOM(t *testing.T) {
	f, err := LoadBytes("win.yaml", windowsBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	out, changed, err := f.Render(f.Doc)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if bytes.Contains(out, []byte("\r\r")) {
		t.Fatalf("doubled carriage returns:\n%q", out)
	}
	if !bytes.HasPrefix(out, utf8BOM) || bytes.Count(out, []byte("\n")) != bytes.Count(out, []byte("\r\n")) {
		t.Fatalf("line endings or BOM lost:\n%q", out)
	}
}

func TestSaveWritesNothingWhenUnchanged(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, err := os.ReadFile("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f.Save(f.Doc)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, orig) {
		t.Fatal("file rewritten")
	}
}

func TestSaveWritesTheEditAtomically(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(p, orig, 0o640); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(p)
	want, _ := os.ReadFile("testdata/save/relation.golden.yaml")
	if !bytes.Equal(got, want) {
		t.Fatalf("got:\n%s", got)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", st.Mode().Perm())
	}
	if entries, _ := os.ReadDir(filepath.Dir(p)); len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestSaveRefusesANonEditableFile(t *testing.T) {
	f := &File{Path: "x.yaml", Editable: false, Reason: "because"}
	if _, err := f.Save(&Document{}); err == nil || !strings.Contains(err.Error(), "because") {
		t.Fatalf("err = %v", err)
	}
	// A real one: an alias would be rewritten everywhere it is used.
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - &t {user: u, relation: m, object: o}\n"
	g, err := LoadBytes("alias.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := g.Render(g.Doc); err == nil || !strings.Contains(err.Error(), "anchors or aliases") {
		t.Fatalf("err = %v", err)
	}
}

func TestSavePairsDuplicateNamesInOrder(t *testing.T) {
	tuple := "      - {user: u, relation: m, object: o}\n"
	src := "version: \"1\"\nrules:\n  # first\n  - name: r\n    tuples:\n" + tuple +
		"  # second\n  - name: r\n    tuples:\n" + tuple
	f, err := LoadBytes("dup.yaml", []byte(src))
	if err != nil || !f.Editable {
		t.Fatalf("err=%v editable=%v", err, f.Editable)
	}
	f.Doc.Rules[1].Tuples[0].Relation = "admin"
	out, changed, err := f.Render(f.Doc)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	want := "version: \"1\"\nrules:\n  # first\n  - name: r\n    tuples:\n" + tuple +
		"  # second\n  - name: r\n    tuples:\n      - {user: u, relation: \"admin\", object: o}\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestSaveFollowsAVariableReorder(t *testing.T) {
	f, err := Load("testdata/load/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	v := f.Doc.Rules[0].Variables
	v[0], v[1] = v[1], v[0]
	out, changed, err := f.Render(f.Doc)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	s := string(out)
	if !strings.Contains(s, "region:") || strings.Index(s, "region:") > strings.Index(s, "org:") {
		t.Fatalf("variables not reordered:\n%s", s)
	}
}

func TestSaveSecondSaveComparesAgainstTheDisk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("first: changed=%v err=%v", changed, err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "member"
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("second: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("relation: member")) || bytes.Contains(got, []byte("admin")) {
		t.Fatalf("disk:\n%s", got)
	}
}

func TestSaveKeepsUnchangedScalarSpellings(t *testing.T) {
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - {user: u, relation: m, object: o}\n" +
		"tests:\n  - name: t\n    input: {a: ~, b: True, c: 0x1F, d: 1.50}\n    expect_tuples: []\n"
	f, err := LoadBytes("s.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	out, _, err := f.Render(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "input: {a: ~, b: True, c: 0x1F, d: 1.50}") {
		t.Fatalf("spellings changed:\n%s", out)
	}
}

func inputFile(t *testing.T, input string) *File {
	t.Helper()
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - {user: u, relation: m, object: o}\n" +
		"tests:\n  - name: t\n    input: " + input + "\n    expect_tuples: []\n"
	f, err := LoadBytes("i.yaml", []byte(src))
	if err != nil || !f.Editable {
		t.Fatalf("err=%v", err)
	}
	return f
}

func TestSaveAcceptsAValueChangingType(t *testing.T) {
	for _, tc := range []struct{ in, key string }{{"{b: true}", "b"}, {"{n: 1}", "n"}, {"{n: null}", "n"}} {
		f := inputFile(t, tc.in)
		f.Doc.Tests[0].Input[tc.key] = map[string]string{"{b: true}": "true", "{n: 1}": "1", "{n: null}": "null"}[tc.in]
		out, changed, err := f.Render(f.Doc)
		if err != nil || !changed {
			t.Fatalf("%s: changed=%v err=%v", tc.in, changed, err)
		}
		if !strings.Contains(string(out), tc.key+`: "`) {
			t.Fatalf("%s: new string value missing:\n%s", tc.in, out)
		}
	}
}

func TestSaveAcceptsAnEmptyNullInFlowStyle(t *testing.T) {
	f := inputFile(t, "{e: }")
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	out, changed, err := f.Render(f.Doc)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if !strings.Contains(string(out), "e: null") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestSaveRefreshesEvenWhenTheWrittenDocumentIsInvalid(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Action = "bogus"
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("first: changed=%v err=%v", changed, err)
	}
	f.Doc.Rules[0].Action = ""
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("second: changed=%v err=%v", changed, err)
	}
	got, _ := os.ReadFile(p)
	if bytes.Contains(got, []byte("bogus")) {
		t.Fatalf("disk:\n%s", got)
	}
}

func TestSaveReportsADocumentItCannotParse(t *testing.T) {
	f, err := Load("testdata/load/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	v := f.Doc.Rules[0].Variables
	v[1].Name = v[0].Name
	_, _, err = f.Render(f.Doc)
	if err == nil || strings.Contains(err.Error(), "layout") || !strings.Contains(err.Error(), "full.yaml") {
		t.Fatalf("err = %v", err)
	}
}

func TestSaveKeepsATrailingCommentWithinItsMapping(t *testing.T) {
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - user: u\n        relation: m\n        object: o\n        condition: c\n        # about the end\n" +
		"  - name: s\n    tuples:\n      - {user: u, relation: m, object: o}\n"
	f, err := LoadBytes("c.yaml", []byte(src))
	if err != nil || !f.Editable {
		t.Fatalf("err=%v", err)
	}
	f.Doc.Rules[0].Tuples[0].Condition = ""
	out, _, err := f.Render(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "about the end") {
		t.Fatalf("comment moved onto another key:\n%s", out)
	}
}

func TestSavePairsUnnamedItemsByContent(t *testing.T) {
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      # t0\n      - {user: u0, relation: m, object: o}\n" +
		"      # t1\n      - {user: u1, relation: m, object: o}\n"
	f, err := LoadBytes("t.yaml", []byte(src))
	if err != nil || !f.Editable {
		t.Fatalf("err=%v", err)
	}
	f.Doc.Rules[0].Tuples = f.Doc.Rules[0].Tuples[1:]
	out, _, err := f.Render(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	want := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      # t1\n      - {user: u1, relation: m, object: o}\n"
	if string(out) != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
	g, _ := LoadBytes("t.yaml", []byte(src))
	g.Doc.Rules[0].Tuples[1].Relation = "admin"
	out, _, err = g.Render(g.Doc)
	if err != nil || !strings.Contains(string(out), "# t1\n      - {user: u1, relation: \"admin\"") || !strings.Contains(string(out), "# t0") {
		t.Fatalf("err=%v got:\n%s", err, out)
	}
}

// loadCopy writes the commented fixture to a temp file and loads it.
func loadCopy(t *testing.T) (*File, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, err := os.ReadFile("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return f, p
}

func TestSaveRefusesAFileChangedOnDisk(t *testing.T) {
	f, p := loadCopy(t)
	external := []byte("# edited elsewhere\n")
	if err := os.WriteFile(p, external, 0o600); err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	changed, err := f.Save(f.Doc)
	if !errors.Is(err, ErrChangedOnDisk) || changed || err.Error() != p+" changed on disk since it was loaded; reload it before saving" {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, external) {
		t.Fatalf("disk:\n%s", got)
	}
}

func TestSaveRefusesAFileGoneFromDisk(t *testing.T) {
	f, p := loadCopy(t)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if _, err := f.Save(f.Doc); !errors.Is(err, ErrChangedOnDisk) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("file recreated: %v", err)
	}
}

func TestSaveRefusesAnEditMadeSinceTheLastSave(t *testing.T) {
	f, p := loadCopy(t)
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if _, err := f.Save(f.Doc); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(p)
	external := append(saved, []byte("# appended elsewhere\n")...)
	if err := os.WriteFile(p, external, 0o600); err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "member"
	if _, err := f.Save(f.Doc); !errors.Is(err, ErrChangedOnDisk) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, external) {
		t.Fatalf("disk:\n%s", got)
	}
}

func TestADraftSavesAndReloadsEditable(t *testing.T) {
	f, p := loadCopy(t)
	f.Doc.Rules[0].Tuples[0].User = ""
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if len(f.Invalid) == 0 {
		t.Fatal("Invalid is empty after saving a tuple with no user")
	}
	g, err := LoadDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Editable || len(g.Doc.Rules) != len(f.Doc.Rules) {
		t.Fatalf("editable=%v rules=%d want %d", g.Editable, len(g.Doc.Rules), len(f.Doc.Rules))
	}
}

func TestCreateRefusesAnExistingPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(p, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(p, &Document{}); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("err = %v", err)
	}
	if got, _ := os.ReadFile(p); string(got) != "keep\n" {
		t.Fatalf("disk:\n%s", got)
	}
}

func TestCreateWritesAndLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "m.yaml")
	doc := &Document{Rules: []Rule{{Name: "r", Tuples: []Tuple{{User: "u", Relation: "m", Object: "o"}}}}}
	f, err := Create(p, doc)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Doc.Rules, f.Doc.Rules) || len(got.Doc.Rules) != 1 || got.Doc.Rules[0].Name != "r" {
		t.Fatalf("rules = %+v", got.Doc.Rules)
	}
	if !f.Editable {
		t.Fatalf("not editable: %s", f.Reason)
	}
}

func TestOverwriteIgnoresAnOutsideEdit(t *testing.T) {
	f, p := loadCopy(t)
	if err := os.WriteFile(p, []byte("# edited elsewhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if _, err := f.Save(f.Doc); !errors.Is(err, ErrChangedOnDisk) {
		t.Fatalf("save err = %v", err)
	}
	if wrote, err := f.Overwrite(f.Doc); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Contains(got, []byte("admin")) || bytes.Contains(got, []byte("edited elsewhere")) {
		t.Fatalf("disk:\n%s", got)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "owner"
	if wrote, err := f.Save(f.Doc); err != nil || !wrote {
		t.Fatalf("later save: wrote=%v err=%v", wrote, err)
	}
}

func TestOverwriteRecreatesADeletedFile(t *testing.T) {
	f, p := loadCopy(t)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if wrote, err := f.Overwrite(f.Doc); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
}

func TestChangedOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.yaml")
	if err := os.WriteFile(path, []byte("version: \"1\"\nrules: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := LoadDraft(path)
	if err != nil {
		t.Fatal(err)
	}
	check := func(step string, want bool) {
		t.Helper()
		if got, err := f.ChangedOnDisk(); err != nil || got != want {
			t.Fatalf("%s: changed = %v, err = %v; want %v", step, got, err, want)
		}
	}
	check("untouched", false)
	if err := os.WriteFile(path, []byte("version: \"1\"\nrules: [] \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	check("edited outside", true)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check("deleted", true)
}

func TestAnEmptyListTakesTheStyleOfTheListItGets(t *testing.T) {
	t.Run("rules", func(t *testing.T) {
		f, err := LoadDraftBytes("e.yaml", []byte("version: \"1\"\nrules: []\n"))
		if err != nil {
			t.Fatal(err)
		}
		f.Doc.Rules = []Rule{
			{Name: "a", Tuples: []Tuple{{User: "u:1", Relation: "m", Object: "o:1"}}},
			{Name: "b", Tuples: []Tuple{{User: "u:2", Relation: "m", Object: "o:2"}}},
		}
		out, _, err := f.Render(f.Doc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "[") || !strings.Contains(string(out), "rules:\n  - name: a\n") {
			t.Fatalf("rules not in block style:\n%s", out)
		}
	})
	t.Run("tests", func(t *testing.T) {
		f, err := LoadDraftBytes("e.yaml", []byte("version: \"1\"\nrules:\n  - name: a\ntests: []\n"))
		if err != nil {
			t.Fatal(err)
		}
		f.Doc.Tests = []TestCase{{Name: "t1"}, {Name: "t2"}}
		out, _, err := f.Render(f.Doc)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "tests: [") || !strings.Contains(string(out), "tests:\n  - name: t1\n") {
			t.Fatalf("tests not in block style:\n%s", out)
		}
	})
}

func TestSaveThroughASymlinkWritesTheTarget(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real.yaml"), filepath.Join(dir, "link.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(real, orig, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.yaml", link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	f, err := Load(link)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if changed, err := f.Save(f.Doc); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", st, err)
	}
	got, _ := os.ReadFile(real)
	want, _ := os.ReadFile("testdata/save/relation.golden.yaml")
	if !bytes.Equal(got, want) {
		t.Fatalf("the target was not written:\n%s", got)
	}
	if st, _ := os.Stat(real); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", st.Mode().Perm())
	}
	if changed, err := f.ChangedOnDisk(); err != nil || changed {
		t.Fatalf("ChangedOnDisk = %v, %v after the save", changed, err)
	}
}

func TestSaveReformattedWritesMarshal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	want, err := Marshal(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	if wrote, err := f.SaveReformatted(f.Doc); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, want) {
		t.Fatalf("not Marshal's bytes:\n%s", got)
	}
	if changed, err := f.ChangedOnDisk(); err != nil || changed {
		t.Fatalf("ChangedOnDisk = %v, %v after the save", changed, err)
	}
}

func TestSaveReformattedKeepsCRLFAndBOM(t *testing.T) {
	p := filepath.Join(t.TempDir(), "win.yaml")
	if err := os.WriteFile(p, windowsBytes(t), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules[0].Tuples[0].Relation = "admin"
	if wrote, err := f.SaveReformatted(f.Doc); err != nil || !wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.HasPrefix(got, utf8BOM) || bytes.Count(got, []byte("\n")) != bytes.Count(got, []byte("\r\n")) ||
		bytes.Contains(got, []byte("\r\r")) {
		t.Fatalf("line endings or BOM lost:\n%q", got)
	}
	again, err := Load(p)
	if err != nil || again.Doc.Rules[0].Tuples[0].Relation != "admin" {
		t.Fatalf("the saved file does not hold the edit: %v", err)
	}
	if changed, err := f.ChangedOnDisk(); err != nil || changed {
		t.Fatalf("ChangedOnDisk = %v, %v after the save", changed, err)
	}
}

func TestSaveReformattedRefusesAnOutsideEdit(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.WriteFile(p, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	outside := append([]byte("# outside\n"), orig...)
	if err := os.WriteFile(p, outside, 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := f.SaveReformatted(f.Doc)
	if !errors.Is(err, ErrChangedOnDisk) || wrote {
		t.Fatalf("wrote=%v err=%v", wrote, err)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, outside) {
		t.Fatalf("the outside edit was overwritten:\n%s", got)
	}
}

func TestAnEmptyListsCommentSurvivesItsFirstItem(t *testing.T) {
	src := "version: \"1\"\nrules: [] # note\n"
	f, err := LoadDraftBytes("m.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	f.Doc.Rules = []Rule{{Name: "a", When: "true"}}
	out, _, err := f.Render(f.Doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "rules: # note\n  - name: a") {
		t.Fatalf("the comment was lost:\n%s", out)
	}
}

func TestSaveOverADeletedLinkTargetRecreatesIt(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "sub", "real.yaml"), filepath.Join(dir, "link.yaml")
	orig, _ := os.ReadFile("testdata/load/commented.yaml")
	if err := os.Symlink(filepath.Join("sub", "real.yaml"), link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	f, err := LoadDraftBytes(link, orig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Overwrite(f.Doc); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v, %v", st, err)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("the target was not recreated: %v", err)
	}
	if _, err := Create(link, f.Doc); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("Create on a link: %v", err)
	}
}
