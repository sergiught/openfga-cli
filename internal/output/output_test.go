package output

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPlainTableUnchangedAndStyledFramed(t *testing.T) {
	defer func() { Plain, Interactive = false, false }()

	var b bytes.Buffer
	Plain = true
	if err := Table(&b, []string{"A"}, [][]string{{"x"}}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "x\n" {
		t.Fatalf("plain output changed: %q", b.String())
	}

	// Styled output to a terminal is framed with box-drawing.
	Plain, Interactive = false, true
	b.Reset()
	if err := Table(&b, []string{"A"}, [][]string{{"x"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "╭") {
		t.Fatal("styled table on a TTY should be framed")
	}

	// Styled output to a pipe (non-TTY) drops the frame so rows stay
	// grep/awk-friendly.
	Interactive = false
	b.Reset()
	if err := Table(&b, []string{"A"}, [][]string{{"x"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "╭") {
		t.Fatalf("piped table should not be framed: %q", b.String())
	}
	if strings.Contains(b.String(), "─") {
		t.Fatalf("piped table should not contain box-drawing: %q", b.String())
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

func TestYAMLPreservesLargeIntegers(t *testing.T) {
	var b bytes.Buffer
	const n int64 = 9_007_199_254_740_993
	if err := YAML(&b, map[string]int64{"value": n}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(b.String()); got != "value: 9007199254740993" {
		t.Fatalf("large integer lost precision: %q", got)
	}
}

func TestSanitizeFieldRemovesTerminalControls(t *testing.T) {
	got := SanitizeField("safe\x1b]52;c;secret\a\nforged\x1b[31mred\x1b[0m\u009b31m")
	if got != "safeforgedred31m" {
		t.Fatalf("SanitizeField() = %q", got)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestTablePropagatesWriteErrors(t *testing.T) {
	want := fmt.Errorf("write failed")
	if err := Table(failingWriter{err: want}, []string{"A"}, [][]string{{"x"}}); !errors.Is(err, want) {
		t.Fatalf("Table() error = %v, want %v", err, want)
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
