package mapping

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	"github.com/sergiught/openfga-cli/internal/output"
)

// sentinel is the placeholder streamDoc swaps for a list. It is a JSON string
// whose text holds NUL characters, so it is `"\u0000LIST:<name>\u0000"` once
// encoded. A string field of real data encodes a NUL the same way, so
// streamDoc checks that each sentinel appears exactly once.
func sentinel(name string) json.RawMessage {
	return json.RawMessage(`"\u0000LIST:` + name + `\u0000"`)
}

// streamJSON writes v, a struct, exactly as output.JSON would, but with the
// list fields named in lists read from the lists instead of from v: the
// tuples are encoded one at a time, so a list that lives on disk is never
// held in memory. Each key of lists is the JSON name of a top-level field
// (promoted from an embedded struct or not). A nil list renders as [].
func streamJSON(w io.Writer, v any, lists map[string]snapshot.Tuples) error {
	doc, err := withSentinels(v, lists)
	if err != nil {
		return err
	}
	return streamDoc(w, doc, lists)
}

// withSentinels copies v's fields, in order and flattening embedded structs
// as encoding/json does, into a new struct whose fields named in lists hold
// the sentinel.
func withSentinels(v any, lists map[string]snapshot.Tuples) (any, error) {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("stream json: %T is not a struct", v)
	}
	var fields []reflect.StructField
	var values []reflect.Value
	var flatten func(rv reflect.Value)
	flatten = func(rv reflect.Value) {
		rt := rv.Type()
		for i := range rt.NumField() {
			f := rt.Field(i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			if f.Anonymous && tag == "" && f.Type.Kind() == reflect.Struct {
				flatten(rv.Field(i))
				continue
			}
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if name == "" {
				name = f.Name
			}
			val := rv.Field(i)
			typ := f.Type
			if _, ok := lists[name]; ok {
				typ, val = reflect.TypeFor[json.RawMessage](), reflect.ValueOf(sentinel(name))
				opts = ""
			}
			jsonTag := name
			if opts != "" {
				jsonTag += "," + opts
			}
			fields = append(fields, reflect.StructField{Name: fmt.Sprintf("F%d", len(fields)), Type: typ, Tag: reflect.StructTag(`json:"` + jsonTag + `"`)})
			values = append(values, val)
		}
	}
	flatten(rv)
	out := reflect.New(reflect.StructOf(fields)).Elem()
	for i, val := range values {
		out.Field(i).Set(val)
	}
	return out.Interface(), nil
}

// streamDoc encodes doc, which holds sentinels where lists belong, with the
// settings output.JSON uses, then writes it with each sentinel replaced by its
// list. Every sentinel must appear exactly once or nothing is written.
func streamDoc(w io.Writer, doc any, lists map[string]snapshot.Tuples) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if !output.Compact {
		enc.SetIndent("", "  ")
	}
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	b := buf.Bytes()

	type spot struct {
		at, end int
		name    string
	}
	var spots []spot
	for name := range lists {
		s := sentinel(name)
		if n := bytes.Count(b, s); n != 1 {
			return fmt.Errorf("stream json: list %q appears %d times in the document, want 1", name, n)
		}
		at := bytes.Index(b, s)
		spots = append(spots, spot{at, at + len(s), name})
	}
	slices.SortFunc(spots, func(a, b spot) int { return a.at - b.at })

	pos := 0
	for _, s := range spots {
		if _, err := w.Write(b[pos:s.at]); err != nil {
			return err
		}
		line := bytes.LastIndexByte(b[:s.at], '\n') + 1
		lead := b[line:s.at]
		indent := string(lead[:len(lead)-len(bytes.TrimLeft(lead, " "))])
		if err := writeList(w, lists[s.name], indent); err != nil {
			return err
		}
		pos = s.end
	}
	_, err := w.Write(b[pos:])
	return err
}

// writeList writes ts as the JSON array encoding/json would produce for a
// []tupleOut sitting at indent, one tuple at a time.
func writeList(w io.Writer, ts snapshot.Tuples, indent string) error {
	if ts == nil {
		_, err := io.WriteString(w, "[]")
		return err
	}
	inner := indent + "  "
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	if !output.Compact {
		enc.SetIndent(inner, "  ")
	}
	enc.SetEscapeHTML(false)
	n := 0
	err := ts.Each(func(t language.Tuple) error {
		buf.Reset()
		sep := "["
		if n > 0 {
			sep = ","
		}
		if !output.Compact {
			sep += "\n" + inner
		}
		n++
		if err := enc.Encode(tupleOut{User: t.User, Relation: t.Relation, Object: t.Object, Condition: t.Condition, Context: t.Context}); err != nil {
			return err
		}
		if _, err := io.WriteString(w, sep); err != nil {
			return err
		}
		_, err := w.Write(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
		return err
	})
	if err != nil {
		return err
	}
	switch {
	case n == 0:
		_, err = io.WriteString(w, "[]")
	case output.Compact:
		_, err = io.WriteString(w, "]")
	default:
		_, err = io.WriteString(w, "\n"+indent+"]")
	}
	return err
}
