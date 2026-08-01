package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Streamer emits a sequence of values incrementally, so a command that reads an
// unbounded result set does not have to hold all of it in memory before writing
// anything. The rendered output is byte-for-byte what Emit would have produced
// for the equivalent slice, so switching a command to streaming is not a change
// in its machine output.
//
// Write may be called any number of times, including zero; Close must be called
// exactly once and its error must not be discarded, since it writes the closing
// delimiter.
type Streamer interface {
	Write(v any) error
	Close() error
}

// NewStreamer returns a Streamer matching Emit's choice of format.
//
// Under --jq the values are buffered instead of streamed: a filter like
// `length` or `sort_by(.x)` has to see the whole document, so there is nothing
// to stream. That trades the memory bound for correctness, and only for
// invocations that opted into a filter.
func NewStreamer(w io.Writer, asYAML bool) Streamer {
	if JQ != "" {
		return &bufferedStream{w: w, asYAML: asYAML}
	}
	if asYAML {
		return &yamlStream{w: w}
	}
	return &jsonStream{w: w}
}

// bufferedStream collects values and renders them once, for the cases that
// cannot stream.
type bufferedStream struct {
	w      io.Writer
	asYAML bool
	items  []any
}

func (s *bufferedStream) Write(v any) error {
	s.items = append(s.items, v)
	return nil
}

func (s *bufferedStream) Close() error {
	if s.items == nil {
		s.items = []any{}
	}
	return Emit(s.w, s.asYAML, s.items)
}

// jsonStream writes a JSON array one element at a time, reproducing the
// two-space indentation and disabled HTML escaping of JSON.
type jsonStream struct {
	w     io.Writer
	n     int
	errer error
}

func (s *jsonStream) Write(v any) error {
	if s.errer != nil {
		return s.errer
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// The element sits one level inside the array, so every line after the
	// first carries the array's indent as a prefix.
	enc.SetIndent("  ", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return s.fail(err)
	}
	elem := strings.TrimSuffix(buf.String(), "\n")

	prefix := "[\n  "
	if s.n > 0 {
		prefix = ",\n  "
	}
	if _, err := io.WriteString(s.w, prefix+elem); err != nil {
		return s.fail(err)
	}
	s.n++
	return nil
}

func (s *jsonStream) Close() error {
	if s.errer != nil {
		return s.errer
	}
	// A stream that never saw a value is the empty array, matching JSON's
	// nil-slice coercion.
	if s.n == 0 {
		_, err := io.WriteString(s.w, "[]\n")
		return err
	}
	_, err := io.WriteString(s.w, "\n]\n")
	return err
}

func (s *jsonStream) fail(err error) error {
	s.errer = err
	return err
}

// yamlStream writes a YAML sequence one item at a time. Items are rendered
// through YAML so field names and shapes match --json exactly, then re-indented
// as sequence entries.
type yamlStream struct {
	w     io.Writer
	n     int
	errer error
}

func (s *yamlStream) Write(v any) error {
	if s.errer != nil {
		return s.errer
	}
	var buf bytes.Buffer
	if err := YAML(&buf, v); err != nil {
		return s.fail(err)
	}
	body := strings.TrimSuffix(buf.String(), "\n")
	if body == "" {
		return nil
	}

	var out strings.Builder
	for i, line := range strings.Split(body, "\n") {
		switch {
		case i == 0:
			out.WriteString("- " + line + "\n")
		case line == "":
			out.WriteString("\n")
		default:
			out.WriteString("  " + line + "\n")
		}
	}
	if _, err := io.WriteString(s.w, out.String()); err != nil {
		return s.fail(err)
	}
	s.n++
	return nil
}

func (s *yamlStream) Close() error {
	if s.errer != nil {
		return s.errer
	}
	if s.n == 0 {
		_, err := io.WriteString(s.w, "[]\n")
		return err
	}
	return nil
}

func (s *yamlStream) fail(err error) error {
	s.errer = err
	return err
}

// PlainRow writes one unstyled, tab-separated record, matching the rows Table
// emits in Plain mode. Fields are sanitized so a tab or newline in server data
// cannot forge extra columns or records.
func PlainRow(w io.Writer, fields ...string) error {
	cleaned := make([]string, len(fields))
	for i, f := range fields {
		cleaned[i] = PlainField(f)
	}
	_, err := fmt.Fprintln(w, strings.Join(cleaned, "\t"))
	return err
}
