package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func readTestdata(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func dirReader(dir string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, rel)) }
}

func TestConvert_InlineModel(t *testing.T) {
	ws, err := ConvertNamed("inline", readTestdata(t, "store.fga.yaml"), dirReader("testdata"))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(ws.Model), "type document") {
		t.Fatalf("%q does not contain %q", string(ws.Model), "type document")
	}
	if !strings.Contains(string(ws.Model), "condition in_window") {
		t.Fatalf("%q does not contain %q", string(ws.Model), "condition in_window")
	}
	if !strings.Contains(string(ws.Manifest), "model: ./model.fga") {
		t.Fatalf("%q does not contain %q", string(ws.Manifest), "model: ./model.fga")
	}
	if !strings.Contains(string(ws.Manifest), "fixtures/*.yaml") {
		t.Fatalf("%q does not contain %q", string(ws.Manifest), "fixtures/*.yaml")
	}
	if !strings.Contains(string(ws.Manifest), "tests/*.test.yaml") {
		t.Fatalf("%q does not contain %q", string(ws.Manifest), "tests/*.test.yaml")
	}
	if !strings.Contains(string(ws.Tests), "fixtures:") {
		t.Fatalf("%q does not contain %q", string(ws.Tests), "fixtures:")
	}
	if !strings.Contains(string(ws.Tests), "- inline") {
		t.Fatalf("%q does not contain %q", string(ws.Tests), "- inline")
	}
	// Global tuples land in the fixture, conditions included.
	if !strings.Contains(string(ws.Fixture), "group:readers#member") {
		t.Fatalf("%q does not contain %q", string(ws.Fixture), "group:readers#member")
	}
	if !strings.Contains(string(ws.Fixture), "name: in_window") {
		t.Fatalf("%q does not contain %q", string(ws.Fixture), "name: in_window")
	}
	if !strings.Contains(string(ws.Fixture), "grant_duration: 1h") {
		t.Fatalf("%q does not contain %q", string(ws.Fixture), "grant_duration: 1h")
	}
}

func TestConvert_ModelFile(t *testing.T) {
	dir := filepath.Join("testdata", "modelfile")
	ws, err := ConvertNamed("mf", readTestdata(t, "modelfile/store.fga.yaml"), dirReader(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ws.Model), "define viewer: [user] or owner") {
		t.Fatalf("%q does not contain %q", string(ws.Model), "define viewer: [user] or owner")
	}
	// Global tuples first, then tuple_file tuples.
	f := string(ws.Fixture)
	if !strings.Contains(f, "doc:1") {
		t.Fatalf("%q does not contain %q", f, "doc:1")
	}
	if !strings.Contains(f, "doc:2") {
		t.Fatalf("%q does not contain %q", f, "doc:2")
	}
	if strings.Index(f, "doc:1") >= strings.Index(f, "doc:2") {
		t.Fatalf("%v is not less than %v", strings.Index(f, "doc:1"), strings.Index(f, "doc:2"))
	}
	// Test-level tuple_file becomes inline test tuples.
	if !strings.Contains(string(ws.Tests), "user:erin") {
		t.Fatalf("%q does not contain %q", string(ws.Tests), "user:erin")
	}
}

func TestConvert_TestLevelTuplesAndContext(t *testing.T) {
	ws, err := ConvertNamed("inline", readTestdata(t, "store.fga.yaml"), dirReader("testdata"))
	if err != nil {
		t.Fatal(err)
	}
	tests := string(ws.Tests)
	if !strings.Contains(tests, "tuples:") {
		t.Fatalf("%q does not contain %q", tests, "tuples:")
	}
	if !strings.Contains(tests, "user:carol") {
		t.Fatalf("%q does not contain %q", tests, "user:carol")
	}
	if !strings.Contains(tests, "contextual_tuples:") {
		t.Fatalf("%q does not contain %q", tests, "contextual_tuples:")
	}
	if !strings.Contains(tests, "current_time: \"2023-01-01T00:10:00Z\"") {
		t.Fatalf("%q does not contain %q", tests, "current_time: \"2023-01-01T00:10:00Z\"")
	}
	if !strings.Contains(tests, "description: anne reads through the readers group") {
		t.Fatalf("%q does not contain %q", tests, "description: anne reads through the readers group")
	}
	if !strings.Contains(tests, "list_objects:") {
		t.Fatalf("%q does not contain %q", tests, "list_objects:")
	}
}

func TestConvert_ListUsers(t *testing.T) {
	ws, err := ConvertNamed("inline", readTestdata(t, "store.fga.yaml"), dirReader("testdata"))
	if err != nil {
		t.Fatal(err)
	}
	tests := string(ws.Tests)
	if !strings.Contains(tests, "list_users:") {
		t.Fatalf("%q does not contain %q", tests, "list_users:")
	}
	if !strings.Contains(tests, "user_filter:") {
		t.Fatalf("%q does not contain %q", tests, "user_filter:")
	}
	if !strings.Contains(tests, "relation: member") {
		t.Fatalf("%q does not contain %q", tests, "relation: member")
	}
	if !strings.Contains(tests, "group:readers#member") {
		t.Fatalf("%q does not contain %q", tests, "group:readers#member")
	}
}

func TestConvert_RejectsUnmappedKeys(t *testing.T) {
	cases := map[string]string{
		"top level":    "name: x\nmodel: |\n  model\n    schema 1.1\n  type user\nbogus: 1\n",
		"test level":   "model: |\n  model\n    schema 1.1\n  type user\ntests:\n  - name: t\n    bogus: 1\n",
		"check level":  "model: |\n  model\n    schema 1.1\n  type user\ntests:\n  - name: t\n    check:\n      - user: user:a\n        object: user:b\n        bogus: 1\n        assertions: {}\n",
		"list_objects": "model: |\n  model\n    schema 1.1\n  type user\ntests:\n  - name: t\n    list_objects:\n      - user: user:a\n        type: user\n        contextual_tuples:\n          - {user: user:a, relation: r, object: user:b}\n        assertions: {}\n",
		"no model":     "name: x\n",
		"both models":  "model: x\nmodel_file: m.fga\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ConvertNamed("x", []byte(src), dirReader("testdata"))
			if err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestConvert_OutputLoads(t *testing.T) {
	for _, tc := range []struct{ name, sub string }{
		{"inline", ""},
		{"mf", "modelfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws, err := ConvertNamed(tc.name, readTestdata(t, filepath.Join(tc.sub, "store.fga.yaml")), dirReader(filepath.Join("testdata", tc.sub)))
			if err != nil {
				t.Fatal(err)
			}

			out := t.TempDir()
			if err := writeWorkspace(out, tc.name, ws, "abc123"); err != nil {
				t.Fatal(err)
			}

			loaded, err := modeltest.LoadWorkspace(out)
			if err != nil {
				t.Fatal(err)
			}
			eng, err := modeltest.NewEmbeddedEngine(nil)
			if err != nil {
				t.Fatal(err)
			}
			res, err := modeltest.Run(context.Background(), loaded, modeltest.Options{Engine: eng})
			if err != nil {
				t.Fatal(err)
			}
			if res.Summary.Total <= 0 {
				t.Fatalf("got %v, want > 0", res.Summary.Total)
			}
			if res.Summary.Passed != res.Summary.Total {
				t.Fatalf("passed %d of %d: %+v", res.Summary.Passed, res.Summary.Total, res.Tests)
			}

			b, err := os.ReadFile(filepath.Join(out, "model.fga"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(b), "# Generated by tools/samplestores from openfga/sample-stores@abc123 (Apache-2.0). Do not edit.") {
				t.Fatalf("%q does not contain %q", string(b), "# Generated by tools/samplestores from openfga/sample-stores@abc123 (Apache-2.0). Do not edit.")
			}
		})
	}
}
