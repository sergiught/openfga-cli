package mapping

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
)

// ErrNoEvent is DecodeEvent's error for empty or blank input.
var ErrNoEvent = errors.New("no event")

// ParseEvents reads events as one JSON object, a JSON array of objects, or a
// stream of them (JSONL) — a json.Decoder walks all three alike. Numbers are
// exact (see exactNumbers), and one too large for a float64 is refused. Errors
// read "<source>:<line>:<column>: …"; the column is 1-based and counts bytes,
// not runes.
func ParseEvents(r io.Reader, source string) ([]map[string]any, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, utf8BOM)
	dec := json.NewDecoder(bytes.NewReader(data))
	var out []map[string]any
	for {
		start := dec.InputOffset()
		var raw json.RawMessage
		if err := dec.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			line, col := position(data, start+syntaxOffset(data[start:]))
			return nil, fmt.Errorf("%s:%d:%d: %v", source, line, col, err)
		}
		rawAt := start + int64(leadingSpace(data[start:]))
		type item struct {
			raw json.RawMessage
			at  int64
		}
		var items []item
		if raw[0] == '[' {
			sub := json.NewDecoder(bytes.NewReader(raw))
			_, _ = sub.Token() // the '['
			for sub.More() {
				pos := sub.InputOffset()
				var it json.RawMessage
				if err := sub.Decode(&it); err != nil {
					line, col := position(data, rawAt)
					return nil, fmt.Errorf("%s:%d:%d: %v", source, line, col, err)
				}
				at := pos + int64(len(raw[pos:])-len(bytes.TrimLeft(raw[pos:], " \t\r\n,")))
				items = append(items, item{it, rawAt + at})
			}
		} else {
			items = []item{{raw, rawAt}}
		}
		for _, it := range items {
			ev, err := objectOf(it.raw)
			var big *rangeError
			if errors.As(err, &big) {
				line, col := position(data, it.at+big.at)
				return nil, fmt.Errorf("%s:%d:%d: %v", source, line, col, big)
			}
			if err != nil || ev == nil {
				line, col := position(data, it.at)
				return nil, fmt.Errorf("%s:%d:%d: event %d is not a JSON object", source, line, col, len(out)+1)
			}
			out = append(out, ev)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no events in %s", source)
	}
	return out, nil
}

// DecodeEvent reads one event: a single JSON object. Numbers are exact (an
// integer that fits in 64 bits is an int, anything else a float64), and one
// too large for a float64 is refused. Errors name the position: "line 3,
// column 7: …", or say what the value was instead of an object ("an event
// must be a JSON object, not an array"). Columns are 1-based and count bytes,
// not runes.
func DecodeEvent(data []byte) (map[string]any, error) {
	data = bytes.TrimPrefix(data, utf8BOM)
	lead := leadingSpace(data)
	if lead == len(data) {
		return nil, ErrNoEvent
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		line, col := position(data, syntaxOffset(data))
		return nil, fmt.Errorf("line %d, column %d: %v", line, col, err)
	}
	if kind := valueKind(raw[0]); kind != "" {
		return nil, fmt.Errorf("an event must be a JSON object, not %s", kind)
	}
	ev, err := objectOf(raw)
	var big *rangeError
	if errors.As(err, &big) {
		line, col := position(data, int64(lead)+big.at)
		return nil, fmt.Errorf("line %d, column %d: %v", line, col, big)
	}
	if err != nil {
		return nil, err
	}
	if rest := data[dec.InputOffset():]; leadingSpace(rest) < len(rest) {
		line, col := position(data, dec.InputOffset()+int64(leadingSpace(rest)))
		return nil, fmt.Errorf("line %d, column %d: paste one event; this is a second one", line, col)
	}
	return ev, nil
}

// valueKind names a JSON value other than an object by its first byte, or
// returns "" for an object.
func valueKind(b byte) string {
	switch b {
	case '[':
		return "an array"
	case '"':
		return "a string"
	case 't', 'f':
		return "true or false"
	case 'n':
		return "null"
	case '{':
		return ""
	}
	return "a number"
}

// objectOf decodes one JSON value that must be an object, with exact numbers.
// A number no float64 holds is a *rangeError.
func objectOf(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var ev map[string]any
	if err := dec.Decode(&ev); err != nil {
		return nil, err
	}
	fits := true
	for k, v := range ev {
		ev[k] = exactNumbers(v, &fits)
	}
	if !fits {
		return nil, firstOutOfRange(raw)
	}
	return ev, nil
}

// exactNumbers turns a json.Number into an int when it is an integer that
// fits in 64 bits, and into a float64 otherwise, walking maps and slices. It
// clears *fits on a number too large for a float64.
func exactNumbers(v any, fits *bool) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(x.String(), 10, 64); err == nil {
			return int(i)
		}
		f, err := x.Float64()
		if err != nil || math.IsInf(f, 0) {
			*fits = false
		}
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = exactNumbers(e, fits)
		}
	case []any:
		for i, e := range x {
			x[i] = exactNumbers(e, fits)
		}
	}
	return v
}

// rangeError is a number too large for a float64, at byte offset at of the
// value it was read from.
type rangeError struct {
	num json.Number
	at  int64
}

func (e *rangeError) Error() string { return "number " + e.num.String() + " is out of range" }

// firstOutOfRange is the first number in raw, in reading order, too large for
// a float64; nil when there is none.
func firstOutOfRange(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil
		}
		if n, ok := tok.(json.Number); ok {
			if f, err := n.Float64(); err != nil || math.IsInf(f, 0) {
				return &rangeError{num: n, at: dec.InputOffset() - int64(len(n))}
			}
		}
	}
}

// syntaxOffset is the 0-based byte index, within data, of the first value's
// syntax error (the end of the input when it is cut short, 0 when there is
// no error). A fresh decoder keeps the offset
// relative to data, whatever an earlier decoder had read.
func syntaxOffset(data []byte) int64 {
	var raw json.RawMessage
	var se *json.SyntaxError
	err := json.NewDecoder(bytes.NewReader(data)).Decode(&raw)
	if errors.As(err, &se) {
		return max(se.Offset-1, 0)
	}
	if err != nil {
		// Truncated input has no offset of its own: the value is missing its
		// end, so point at the last thing in it.
		return int64(max(len(bytes.TrimRight(data, " \t\r\n"))-1, 0))
	}
	return 0
}

// position converts a byte offset into a 1-based line and column.
func position(data []byte, off int64) (line, col int) {
	head := data[:min(off, int64(len(data)))]
	return bytes.Count(head, []byte("\n")) + 1, len(head) - bytes.LastIndexByte(head, '\n')
}

func leadingSpace(b []byte) int { return len(b) - len(bytes.TrimLeft(b, " \t\r\n")) }
