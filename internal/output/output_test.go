package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestPlainTableUnchangedAndStyledFramed(t *testing.T) {
	defer func() { Plain, Interactive = false, false }()

	var b bytes.Buffer
	Plain = true
	Table(&b, []string{"A"}, [][]string{{"x"}})
	if b.String() != "x\n" {
		t.Fatalf("plain output changed: %q", b.String())
	}

	// Styled output to a terminal is framed with box-drawing.
	Plain, Interactive = false, true
	b.Reset()
	Table(&b, []string{"A"}, [][]string{{"x"}})
	if !strings.Contains(b.String(), "╭") {
		t.Fatal("styled table on a TTY should be framed")
	}

	// Styled output to a pipe (non-TTY) drops the frame so rows stay
	// grep/awk-friendly.
	Interactive = false
	b.Reset()
	Table(&b, []string{"A"}, [][]string{{"x"}})
	if strings.Contains(b.String(), "╭") {
		t.Fatalf("piped table should not be framed: %q", b.String())
	}
}

func TestJSONNilSliceSerializesAsEmptyArray(t *testing.T) {
	var b bytes.Buffer
	var empty []string // typed nil: would marshal to `null` without coercion
	if err := JSON(&b, empty); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Fatalf("nil slice should serialize as [], got %q", got)
	}

	// A non-nil slice is untouched.
	b.Reset()
	if err := JSON(&b, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != `[
  "x"
]` {
		t.Fatalf("non-nil slice should serialize normally, got %q", got)
	}
}

func TestYAMLMatchesJSONFieldNamesAndCoercesNilSlice(t *testing.T) {
	type payload struct {
		StoreID string `json:"store_id"`
		Count   int    `json:"count"`
	}

	var b bytes.Buffer
	if err := YAML(&b, payload{StoreID: "01ABC", Count: 3}); err != nil {
		t.Fatal(err)
	}
	got := b.String()
	if !strings.Contains(got, "store_id: 01ABC") || !strings.Contains(got, "count: 3") {
		t.Fatalf("YAML should use the json struct tag names, got %q", got)
	}

	// A typed nil slice serializes as an empty YAML sequence, matching JSON's
	// [] coercion, rather than YAML's `null`/`~`.
	b.Reset()
	var empty []string
	if err := YAML(&b, empty); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "[]" {
		t.Fatalf("nil slice should serialize as [], got %q", got)
	}
}

func TestEmitPicksJSONOrYAML(t *testing.T) {
	v := map[string]string{"k": "v"}

	var jb bytes.Buffer
	if err := Emit(&jb, false, v); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jb.String(), `"k": "v"`) {
		t.Fatalf("Emit(false, …) should produce JSON, got %q", jb.String())
	}

	var yb bytes.Buffer
	if err := Emit(&yb, true, v); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(yb.String()) != "k: v" {
		t.Fatalf("Emit(true, …) should produce YAML, got %q", yb.String())
	}
}

func TestErrorfNotSuppressedByQuiet(t *testing.T) {
	Plain = false
	Quiet = true
	defer func() { Quiet = false }()

	var b bytes.Buffer
	Errorf(&b, "boom %d", 1)
	if !strings.Contains(b.String(), "boom 1") {
		t.Fatalf("Errorf output missing message under Quiet: %q", b.String())
	}
	if !strings.Contains(b.String(), "●") {
		t.Fatalf("Errorf output missing dot: %q", b.String())
	}
}
