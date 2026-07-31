package output

import (
	"bytes"
	"strings"
	"testing"
)

type streamItem struct {
	User  string            `json:"user"`
	Count int               `json:"count"`
	Meta  map[string]string `json:"meta,omitempty"`
}

// Streaming exists to bound memory, not to change what a script sees. Emitting
// N values through a Streamer must produce exactly what Emit produces for the
// slice of those N values — otherwise switching a command to streaming silently
// breaks every consumer parsing its output.
func TestStreamerMatchesEmitByteForByte(t *testing.T) {
	cases := map[string][]streamItem{
		"empty": {},
		"one":   {{User: "user:anne", Count: 1}},
		"many": {
			{User: "user:anne", Count: 1, Meta: map[string]string{"k": "v"}},
			{User: "user:bob", Count: 2},
			{User: "user:carol", Count: 3},
		},
		"html-ish": {{User: "user:a&b<c>d", Count: 1}},
	}

	for name, items := range cases {
		for _, asYAML := range []bool{false, true} {
			format := "json"
			if asYAML {
				format = "yaml"
			}
			t.Run(name+"/"+format, func(t *testing.T) {
				var want bytes.Buffer
				if err := Emit(&want, asYAML, items); err != nil {
					t.Fatal(err)
				}

				var got bytes.Buffer
				s := NewStreamer(&got, asYAML)
				for _, it := range items {
					if err := s.Write(it); err != nil {
						t.Fatal(err)
					}
				}
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}

				if got.String() != want.String() {
					t.Errorf("streamed output differs from Emit\n got: %q\nwant: %q", got.String(), want.String())
				}
			})
		}
	}
}

// A write failure partway through must not be swallowed by a later call: the
// command needs to stop, not keep streaming into a broken pipe and then report
// success.
func TestStreamerStickyError(t *testing.T) {
	for _, asYAML := range []bool{false, true} {
		s := NewStreamer(failingWriter{err: errBoom}, asYAML)
		if err := s.Write(streamItem{User: "user:anne"}); err == nil {
			t.Fatal("Write should surface the writer error")
		}
		if err := s.Close(); err == nil {
			t.Fatal("Close should keep reporting the earlier failure")
		}
	}
}

var errBoom = errBoomType{}

type errBoomType struct{}

func (errBoomType) Error() string { return "boom" }

func TestPlainRowMatchesPlainTable(t *testing.T) {
	defer func(p bool) { Plain = p }(Plain)
	Plain = true

	row := []string{"user:anne", "line one\nline two", "tab\tvalue", "esc\x1b[31mred"}

	var want bytes.Buffer
	if err := Table(&want, []string{"A", "B", "C", "D"}, [][]string{row}); err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := PlainRow(&got, row...); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Errorf("PlainRow = %q, want the same record Table emits %q", got.String(), want.String())
	}
	if strings.Count(strings.TrimSpace(got.String()), "\n") != 0 {
		t.Errorf("PlainRow emitted more than one record: %q", got.String())
	}
}
