package mapping

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func windowsBytes(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte("\uFEFF"), bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))...)
}

func TestLoadConvertsEveryConstruct(t *testing.T) {
	f, err := Load("testdata/load/commented.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Doc.Rules) != 2 {
		t.Fatalf("rules = %d, want 2", len(f.Doc.Rules))
	}
	r := f.Doc.Rules[0]
	if r.Name != "member-added" || r.When != `input.type == "organization.member.added"` || len(r.Tuples) != 1 {
		t.Fatalf("rule 0 = %+v", r)
	}
	if r.Tuples[0].Object != "organization:{{ input.data.object.organization.id }}" || r.Tuples[0].Relation != "member" {
		t.Fatalf("tuple = %+v", r.Tuples[0])
	}
	if f.Doc.Rules[1].Action != "delete" {
		t.Fatalf("rule 1 action = %q", f.Doc.Rules[1].Action)
	}
	if len(f.Doc.Tests) != 1 || f.Doc.Tests[0].ExpectTuples[0].Object != "organization:o1" {
		t.Fatalf("tests = %+v", f.Doc.Tests)
	}
	if line, _ := f.RuleLine(1); line != 11 {
		t.Fatalf("rule 1 line = %d, want 11", line)
	}
	if line, _ := f.TestLine(0); line != 19 {
		t.Fatalf("test 0 line = %d, want 19", line)
	}
}

func TestLoadConvertsTheRichConstructs(t *testing.T) {
	f, err := Load("testdata/load/full.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r := f.Doc.Rules[0]
	if len(r.Variables) != 2 || r.Variables[0].Name != "org" || r.Variables[1].Name != "region" {
		t.Fatalf("variables = %+v", r.Variables)
	}
	if r.Iterator == nil || r.Iterator.As != "member" || len(r.Iterator.Tuples) != 1 {
		t.Fatalf("iterator = %+v", r.Iterator)
	}
	if len(r.Filters) != 2 || r.Filters[0].Action != "patch" || r.Filters[1].Action != "delete" {
		t.Fatalf("filters = %+v", r.Filters)
	}
	tup := r.Iterator.Tuples[0]
	if tup.When != `member.user_id != ""` || tup.Action != "write" {
		t.Fatalf("tuple-level guard or action lost: %+v", tup)
	}
	if tup.Condition != "in_business_hours" || len(tup.Context) != 2 {
		t.Fatalf("tuple = %+v", tup)
	}
	tc := f.Doc.Tests[0]
	if !tc.AssertWritesCoveredByFilter || len(tc.ExpectTupleFilters) != 2 {
		t.Fatalf("test = %+v", tc)
	}
}

func TestEveryGoldenAndRecipeIsEditable(t *testing.T) {
	paths, _ := filepath.Glob("testdata/load/*.yaml")
	for _, p := range paths {
		f, err := Load(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if !f.Editable {
			t.Errorf("%s: not editable: %s", p, f.Reason)
		}
	}
}

func TestLoadReportsParseErrorsWithPosition(t *testing.T) {
	_, err := LoadBytes("bad.yaml", []byte("version: \"1\"\nrules:\n  - name: r\n    bogus: 1\n"))
	var le *LoadError
	if !errors.As(err, &le) || !strings.HasPrefix(err.Error(), "bad.yaml:4:") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadErrorWithoutPositionOmitsLineAndColumn(t *testing.T) {
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - user: u\n        relation: member\n"
	_, err := LoadBytes("bad.yaml", []byte(src))
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(err.Error(), "bad.yaml: ") || strings.Contains(err.Error(), ":0:0") {
		t.Fatalf("err = %q", err)
	}
}

func TestLoadHandlesCRLFAndBOM(t *testing.T) {
	f, err := LoadBytes("win.yaml", windowsBytes(t))
	if err != nil {
		t.Fatal(err)
	}
	if !f.crlf || !f.bom || len(f.Doc.Rules) != 2 {
		t.Fatalf("crlf=%v bom=%v rules=%d", f.crlf, f.bom, len(f.Doc.Rules))
	}
	if line, _ := f.RuleLine(1); line != 11 {
		t.Fatalf("rule 1 line = %d, want 11", line)
	}
}

func TestLoadRefusesToEditWhatTheDocumentCannotHold(t *testing.T) {
	// 1e+20 is how Go prints this float back, so the editor would rewrite it.
	src := "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - {user: u, relation: m, object: o}\n" +
		"tests:\n  - name: t\n    input: {n: 100000000000000000000.0}\n    expect_tuples: []\n"
	f, err := LoadBytes("float.yaml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	const reason = "the editor cannot represent everything in this file; validate, test and apply still work"
	if f.Editable || f.Reason != reason {
		t.Fatalf("Editable=%v Reason=%q", f.Editable, f.Reason)
	}
}

func TestLoadRefusesToEditAnchorsAliasesAndMergeKeys(t *testing.T) {
	const tuple = "{user: u, relation: m, object: o}"
	for name, src := range map[string]string{
		"anchor": "version: \"1\"\nrules:\n  - name: &n r\n    tuples: [" + tuple + "]\n",
		"alias":  "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - &t " + tuple + "\n  - name: s\n    tuples:\n      - *t\n",
		"merge":  "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - <<: " + tuple + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			f, err := LoadBytes(name+".yaml", []byte(src))
			if err != nil {
				t.Fatal(err)
			}
			if f.Editable || f.Reason != "uses YAML anchors or aliases; edit it by hand" {
				t.Fatalf("Editable=%v Reason=%q", f.Editable, f.Reason)
			}
		})
	}
}

// noUser is a file whose YAML parses but that mapper rejects: a tuple with no
// user.
const noUser = "version: \"1\"\nrules:\n  - name: r\n    tuples:\n      - {relation: m, object: o}\n"

func TestLoadDraftKeepsAFileMapperRejects(t *testing.T) {
	f, err := LoadDraftBytes("d.yaml", []byte(noUser))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Invalid) == 0 || !f.Editable {
		t.Fatalf("Invalid=%v Editable=%v reason=%q", f.Invalid, f.Editable, f.Reason)
	}
	var le *LoadError
	if _, err := LoadBytes("d.yaml", []byte(noUser)); !errors.As(err, &le) {
		t.Fatalf("strict load err = %v", err)
	}
}

func TestLoadDraftRefusesYAMLThatDoesNotParse(t *testing.T) {
	_, err := LoadDraftBytes("d.yaml", []byte("rules: [unclosed\n"))
	var le *LoadError
	if !errors.As(err, &le) {
		t.Fatalf("err = %v", err)
	}
}
