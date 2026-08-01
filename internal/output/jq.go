package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
)

// JQ is the --jq filter applied to machine-readable output. Empty disables it.
//
// It exists so a container image that ships only `ofga` can still extract a
// field — the common `fga store create | jq -r .store.id` idiom otherwise forces
// jq into every image, or two init containers (openfga/cli#395). `gh --jq` sets
// the precedent for the flag name and behaviour.
var JQ string

// compileJQ parses the active filter. Compilation errors are reported before any
// request is issued, so a typo in a filter does not surface only after a slow
// command has already done its work.
func compileJQ(filter string) (*gojq.Code, error) {
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, fmt.Errorf("--jq %q is not a valid filter: %w", filter, err)
	}
	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("--jq %q could not be compiled: %w", filter, err)
	}
	return code, nil
}

// ValidateJQ reports whether the active filter parses, without running it.
func ValidateJQ() error {
	if JQ == "" {
		return nil
	}
	_, err := compileJQ(JQ)
	return err
}

// applyJQ runs the active filter over v and writes each result to w.
//
// Output follows `jq -r`: a string result is written bare so it can be captured
// into a shell variable without stripping quotes, and every other type is
// written as compact JSON. Each result is on its own line, so a filter yielding
// many values feeds a `while read` loop.
func applyJQ(w io.Writer, v any) error {
	code, err := compileJQ(JQ)
	if err != nil {
		return err
	}
	// Round-trip through JSON so the filter sees exactly the document --json
	// would have printed (json tags, nil-slice coercion), not Go's field names.
	generic, err := toGeneric(v)
	if err != nil {
		return err
	}

	iter := code.Run(generic)
	for {
		result, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := result.(error); isErr {
			var halt *gojq.HaltError
			if ok := asHalt(err, &halt); ok && halt.Value() == nil {
				return nil
			}
			return fmt.Errorf("--jq %q failed: %w", JQ, err)
		}
		if err := writeJQResult(w, result); err != nil {
			return err
		}
	}
}

func asHalt(err error, target **gojq.HaltError) bool {
	h, ok := err.(*gojq.HaltError)
	if ok {
		*target = h
	}
	return ok
}

func writeJQResult(w io.Writer, result any) error {
	switch t := result.(type) {
	case nil:
		_, err := fmt.Fprintln(w, "null")
		return err
	case string:
		_, err := fmt.Fprintln(w, t)
		return err
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	}
}

// toGeneric converts v to the map/slice/number shapes gojq operates on, via the
// same JSON encoding --json would emit.
func toGeneric(v any) (any, error) {
	var buf bytes.Buffer
	if err := JSON(&buf, v); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(&buf)
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return normalizeNumbers(generic), nil
}

// normalizeNumbers converts json.Number to the int/float gojq expects; it does
// not understand json.Number and would treat one as an opaque value.
func normalizeNumbers(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeNumbers(val)
		}
		return t
	case []any:
		for i, val := range t {
			t[i] = normalizeNumbers(val)
		}
		return t
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	default:
		return v
	}
}
