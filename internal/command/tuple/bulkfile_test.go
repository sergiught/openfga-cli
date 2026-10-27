package tuple

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/clierr"
)

// wantTuple asserts one parsed entry's triple and condition name.
func wantTuple(t *testing.T, got tupleInput, user, relation, object, condition string) {
	t.Helper()
	if got.User != user || got.Relation != relation || got.Object != object {
		t.Fatalf("tuple = %s/%s/%s, want %s/%s/%s", got.User, got.Relation, got.Object, user, relation, object)
	}
	name := ""
	if got.Condition != nil {
		name = got.Condition.Name
	}
	if name != condition {
		t.Fatalf("condition = %q, want %q", name, condition)
	}
}

func TestResolveBulkFormatFromExtension(t *testing.T) {
	cases := []struct {
		file, override string
		want           bulkFormat
	}{
		{"t.json", "", formatJSON},
		{"t.jsonl", "", formatJSONL},
		{"t.ndjson", "", formatJSONL},
		{"t.yaml", "", formatYAML},
		{"t.yml", "", formatYAML},
		{"t.csv", "", formatCSV},
		{"t.CSV", "", formatCSV},
		// No or unknown extension keeps today's behaviour: parse as JSON.
		{"-", "", formatJSON},
		{"tuples", "", formatJSON},
		{"t.txt", "", formatJSON},
		// Override always wins over the extension.
		{"t.json", "csv", formatCSV},
		{"-", "yaml", formatYAML},
	}
	for _, c := range cases {
		got, err := resolveBulkFormat(c.file, c.override)
		if err != nil {
			t.Fatalf("resolveBulkFormat(%q,%q): %v", c.file, c.override, err)
		}
		if got != c.want {
			t.Errorf("resolveBulkFormat(%q,%q) = %q, want %q", c.file, c.override, got, c.want)
		}
	}
}

func TestResolveBulkFormatRejectsUnknownOverride(t *testing.T) {
	_, err := resolveBulkFormat("t.json", "xml")
	if err == nil {
		t.Fatal("unknown --file-format should be rejected")
	}
	for _, want := range []string{"--file-format", "json", "jsonl", "yaml", "csv", `"xml"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

func TestParseTupleFileJSONShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"bare array", `[{"user":"user:anne","relation":"viewer","object":"doc:1"},
			{"user":"user:bob","relation":"editor","object":"doc:2","condition":{"name":"c","context":{"k":"v"}}}]`},
		{"tuples wrapper", `{"tuples":[{"user":"user:anne","relation":"viewer","object":"doc:1"},
			{"user":"user:bob","relation":"editor","object":"doc:2","condition":{"name":"c","context":{"k":"v"}}}]}`},
		{"read export", `[{"key":{"user":"user:anne","relation":"viewer","object":"doc:1"},"timestamp":"2024-01-01T00:00:00Z"},
			{"key":{"user":"user:bob","relation":"editor","object":"doc:2","condition":{"name":"c","context":{"k":"v"}}},"timestamp":"2024-01-01T00:00:00Z"}]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTupleFile([]byte(c.data), formatJSON, "t.json")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("len = %d, want 2", len(got))
			}
			wantTuple(t, got[0], "user:anne", "viewer", "doc:1", "")
			wantTuple(t, got[1], "user:bob", "editor", "doc:2", "c")
			if got[1].Condition.Context["k"] != "v" {
				t.Errorf("condition context lost: %#v", got[1].Condition.Context)
			}
		})
	}
}

func TestParseTupleFileYAMLShapes(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{"bare array", "- user: user:anne\n  relation: viewer\n  object: doc:1\n- user: user:bob\n  relation: editor\n  object: doc:2\n  condition:\n    name: c\n    context:\n      k: v\n"},
		{"tuples wrapper", "tuples:\n  - user: user:anne\n    relation: viewer\n    object: doc:1\n  - user: user:bob\n    relation: editor\n    object: doc:2\n    condition:\n      name: c\n      context:\n        k: v\n"},
		{"read export", "- key:\n    user: user:anne\n    relation: viewer\n    object: doc:1\n  timestamp: \"2024-01-01T00:00:00Z\"\n- key:\n    user: user:bob\n    relation: editor\n    object: doc:2\n    condition:\n      name: c\n      context:\n        k: v\n  timestamp: \"2024-01-01T00:00:00Z\"\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseTupleFile([]byte(c.data), formatYAML, "t.yaml")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("len = %d, want 2", len(got))
			}
			wantTuple(t, got[0], "user:anne", "viewer", "doc:1", "")
			wantTuple(t, got[1], "user:bob", "editor", "doc:2", "c")
			if got[1].Condition.Context["k"] != "v" {
				t.Errorf("condition context lost: %#v", got[1].Condition.Context)
			}
		})
	}
}

func TestParseTupleFileJSONL(t *testing.T) {
	data := `{"user":"user:anne","relation":"viewer","object":"doc:1"}
{"key":{"user":"user:bob","relation":"editor","object":"doc:2","condition":{"name":"c"}},"timestamp":"2024-01-01T00:00:00Z"}

{"user":"user:cid","relation":"owner","object":"doc:3"}
`
	got, err := parseTupleFile([]byte(data), formatJSONL, "t.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (blank lines skipped)", len(got))
	}
	wantTuple(t, got[1], "user:bob", "editor", "doc:2", "c")
}

func TestParseTupleFileCSV(t *testing.T) {
	data := "user_type,user_id,user_relation,relation,object_type,object_id,condition_name,condition_context\n" +
		"user,anne,,viewer,doc,1,,\n" +
		"group,eng,member,editor,doc,2,c,\"{\"\"k\"\":\"\"v\"\"}\"\n"
	got, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	wantTuple(t, got[0], "user:anne", "viewer", "doc:1", "")
	wantTuple(t, got[1], "group:eng#member", "editor", "doc:2", "c")
	if got[1].Condition.Context["k"] != "v" {
		t.Errorf("condition_context lost: %#v", got[1].Condition.Context)
	}
}

func TestParseTupleFileCSVHeaderOrderAndOptionalColumns(t *testing.T) {
	data := "object_id,relation,object_type,user_id,user_type\n1,viewer,doc,anne,user\n"
	got, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	wantTuple(t, got[0], "user:anne", "viewer", "doc:1", "")
}

func TestParseTupleFileCSVRejectsUnknownColumn(t *testing.T) {
	data := "user_type,user_id,relation,object_type,object_id,usr_relation\nuser,anne,viewer,doc,1,\n"
	_, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err == nil {
		t.Fatal("unknown CSV column should be rejected")
	}
	if !strings.Contains(err.Error(), "usr_relation") || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("error should name the column and the header line: %q", err)
	}
	if !strings.Contains(err.Error(), "user_relation") {
		t.Errorf("error should list the accepted columns: %q", err)
	}
}

func TestParseTupleFileCSVRejectsMissingRequiredColumn(t *testing.T) {
	data := "user_type,user_id,relation,object_type\nuser,anne,viewer,doc\n"
	_, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err == nil {
		t.Fatal("missing required CSV column should be rejected")
	}
	if !strings.Contains(err.Error(), "object_id") {
		t.Errorf("error should name the missing column: %q", err)
	}
}

func TestParseTupleFileCSVMalformedRowNamesLineNumber(t *testing.T) {
	data := "user_type,user_id,relation,object_type,object_id\n" +
		"user,anne,viewer,doc,1\n" +
		"user,bob,viewer,doc\n"
	_, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err == nil {
		t.Fatal("short CSV row should be rejected")
	}
	if !strings.Contains(err.Error(), "line 3") {
		t.Errorf("error should name line 3: %q", err)
	}
}

func TestParseTupleFileCSVMalformedConditionContextNamesLineNumber(t *testing.T) {
	data := "user_type,user_id,relation,object_type,object_id,condition_name,condition_context\n" +
		"user,anne,viewer,doc,1,c,notjson\n"
	_, err := parseTupleFile([]byte(data), formatCSV, "t.csv")
	if err == nil {
		t.Fatal("malformed condition_context should be rejected")
	}
	if !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), "condition_context") {
		t.Errorf("error should name the line and the column: %q", err)
	}
}

func TestParseTupleFileRejectsUnknownFields(t *testing.T) {
	cases := []struct {
		format bulkFormat
		data   string
	}{
		{formatJSON, `[{"user":"user:anne","relation":"viewer","object":"doc:1","tenant":"x"}]`},
		{formatJSON, `{"tuples":[{"user":"user:anne","relation":"viewer","object":"doc:1","tenant":"x"}]}`},
		{formatYAML, "- user: user:anne\n  relation: viewer\n  object: doc:1\n  tenant: x\n"},
		{formatJSONL, `{"user":"user:anne","relation":"viewer","object":"doc:1","tenant":"x"}`},
	}
	for _, c := range cases {
		_, err := parseTupleFile([]byte(c.data), c.format, "t")
		if err == nil {
			t.Errorf("%s: unknown field should be rejected", c.format)
			continue
		}
		if !strings.Contains(err.Error(), "tenant") {
			t.Errorf("%s: error should name the unknown field: %q", c.format, err)
		}
		// yaml.v3 appends the Go type name ("... in type tuple.tupleInput"),
		// which means nothing to whoever is editing the file.
		if strings.Contains(err.Error(), "in type ") {
			t.Errorf("%s: error should not leak the Go type name: %q", c.format, err)
		}
	}
}

func TestParseTupleFileErrorNamesExpectedSchemas(t *testing.T) {
	_, err := parseTupleFile([]byte(`{"nope":1}`), formatJSON, "t.json")
	if err == nil {
		t.Fatal("unrecognised shape should be rejected")
	}
	msg := err.Error()
	for _, want := range []string{"t.json", "user,relation,object", `"tuples"`, `"key"`, "tuples read"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should mention %q: %q", want, msg)
		}
	}
}

func TestParseTupleFileStdinErrorMentionsDefaultFormat(t *testing.T) {
	_, err := parseTupleFile([]byte("user_type,user_id\n"), formatJSON, "-")
	if err == nil {
		t.Fatal("CSV fed to stdin without --file-format should fail")
	}
	if !strings.Contains(err.Error(), "--file-format") {
		t.Errorf("stdin parse error should point at --file-format: %q", err)
	}
}

func TestEncodeTupleFileRoundTrips(t *testing.T) {
	keys := []openfga.TupleKey{
		{User: "user:anne", Relation: "viewer", Object: "doc:1"},
		{User: "group:eng#member", Relation: "editor", Object: "doc:2",
			Condition: &openfga.RelationshipCondition{Name: "c", Context: map[string]any{"k": "v"}}},
	}
	for _, format := range []bulkFormat{formatJSON, formatJSONL, formatYAML, formatCSV} {
		t.Run(string(format), func(t *testing.T) {
			var buf bytes.Buffer
			if err := encodeTupleFile(&buf, keys, format); err != nil {
				t.Fatal(err)
			}
			got, err := parseTupleFile(buf.Bytes(), format, "round-trip."+string(format))
			if err != nil {
				t.Fatalf("re-parse: %v\n%s", err, buf.String())
			}
			if len(got) != 2 {
				t.Fatalf("len = %d, want 2\n%s", len(got), buf.String())
			}
			wantTuple(t, got[0], "user:anne", "viewer", "doc:1", "")
			wantTuple(t, got[1], "group:eng#member", "editor", "doc:2", "c")
			if got[1].Condition.Context["k"] != "v" {
				t.Errorf("condition context lost through %s: %#v", format, got[1].Condition.Context)
			}
		})
	}
}

func TestLoadTupleFileMatchesWriteFile(t *testing.T) {
	const fixture = `[{"user":"user:a","relation":"viewer","object":"doc:1"},` +
		`{"user":"user:b","relation":"editor","object":"doc:2","condition":{"name":"c","context":{"x":1}}}]`
	path := filepath.Join(t.TempDir(), "t.json")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTupleFile(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, err := bulkTuples(&cobra.Command{}, path, formatJSON, nil, "", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadTupleFile = %+v, write path = %+v", got, want)
	}

	_, err = LoadTupleFile(filepath.Join(t.TempDir(), "missing.json"), nil)
	if clierr.Classify(err) != clierr.KindValidation {
		t.Fatalf("kind = %q, want validation; err=%v", clierr.Classify(err), err)
	}
}

func TestLoadTupleFileStdinSniff(t *testing.T) {
	const bare = `{"user":"user:a","relation":"viewer","object":"doc:1"}`
	const export = `{"key":{"user":"user:a","relation":"viewer","object":"doc:1"},"timestamp":"2026-01-02T03:04:05Z"}`
	const second = `{"user":"user:b","relation":"editor","object":"doc:2"}`
	cases := []struct {
		name  string
		input string
		want  []string // users
	}{
		{"single-line bare JSONL", bare + "\n", []string{"user:a"}},
		{"single-line bare JSONL without newline", bare, []string{"user:a"}},
		{"single-line read-export JSONL", export + "\n", []string{"user:a"}},
		{"multi-line JSONL", bare + "\n" + second + "\n", []string{"user:a", "user:b"}},
		{"JSON array", "[" + bare + "," + second + "]", []string{"user:a", "user:b"}},
		{"leading whitespace array", "\n  [" + bare + "]", []string{"user:a"}},
		{"tuples document", `{"tuples":[` + bare + `,` + second + `]}`, []string{"user:a", "user:b"}},
		{"pretty tuples document", "{\n  \"tuples\": [\n" + bare + "\n  ]\n}\n", []string{"user:a"}},
		{"BOM array", "\ufeff[" + bare + "]", []string{"user:a"}},
		{"BOM JSONL", "\ufeff" + bare + "\n", []string{"user:a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadTupleFile("-", strings.NewReader(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var users []string
			for _, k := range got {
				users = append(users, k.User)
			}
			if !reflect.DeepEqual(users, tc.want) {
				t.Fatalf("users = %v, want %v", users, tc.want)
			}
		})
	}

	t.Run("empty", func(t *testing.T) {
		_, err := LoadTupleFile("-", strings.NewReader(" \n"))
		if err == nil || clierr.Classify(err) != clierr.KindValidation {
			t.Fatalf("err = %v (kind %q), want a validation error", err, clierr.Classify(err))
		}
	})
}

func TestLoadTupleFileHintOmitsFileFormatFlag(t *testing.T) {
	_, err := LoadTupleFile("-", strings.NewReader(`{"user":"user:a","bogus":1}`+"\n"))
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), "--file-format") {
		t.Fatalf("hint mentions --file-format, which query commands lack: %v", err)
	}
	if !strings.Contains(err.Error(), "one tuple per line") {
		t.Fatalf("hint = %v, want query-appropriate guidance", err)
	}
}

func TestLoadTupleFileErrorsCarryZeroBasedIndex(t *testing.T) {
	in := `[{"user":"user:a","relation":"viewer","object":"doc:1"},{"user":"bad","relation":"viewer","object":"doc:1"}]`
	_, err := LoadTupleFile("-", strings.NewReader(in))
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "tuple 1:") {
		t.Fatalf("err = %v, want 0-based \"tuple 1:\"", err)
	}
	env := clierr.NewEnvelope(err)
	found := false
	for _, d := range env.Error.Details {
		if d["index"] == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("details = %v, want index 1", env.Error.Details)
	}
}
