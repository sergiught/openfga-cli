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
