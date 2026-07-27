package tuple

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"
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
