// Command schemagen generates the JSON Schemas that describe what each command
// prints under --json (docs/site/public/schemas/output). Schemas for typed
// outputs are reflected from the Go types each command package declares in
// OutputTypes; the rest are copied from tools/schemagen/handwritten. Output is
// deterministic so CI can fail on drift.
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/invopop/jsonschema"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/outputschema"
)

//go:embed handwritten/*.json
var handwritten embed.FS

func main() {
	out := flag.String("out", "docs/site/public/schemas/output", "output directory for the generated schemas")
	flag.Parse()
	files, err := generate(base.OutputTypes())
	if err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
	if err := write(*out, files); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

// generate returns file name -> content for every schema.
func generate(types map[string]any) (map[string][]byte, error) {
	files := map[string][]byte{}
	used := map[string]bool{}

	env, err := envelopeSchema()
	if err != nil {
		return nil, err
	}
	files[outputschema.FileName(outputschema.EnvelopePath)] = env

	paths := make([]string, 0, len(types))
	for p := range types {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		name := outputschema.FileName(path)
		var raw []byte
		if _, ok := types[path].(outputschema.Handwritten); ok {
			used[name] = true
			raw, err = handwritten.ReadFile("handwritten/" + name)
			if err != nil {
				return nil, fmt.Errorf("%s: hand-written schema handwritten/%s: %w", path, name, err)
			}
			var doc struct {
				ID string `json:"$id"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				return nil, fmt.Errorf("handwritten/%s: %w", name, err)
			}
			if want := outputschema.URL(path); doc.ID != want {
				return nil, fmt.Errorf("handwritten/%s: $id = %q, want %q", name, doc.ID, want)
			}
		} else {
			s := reflector().ReflectFromType(reflect.TypeOf(types[path]))
			s.ID = jsonschema.ID(outputschema.URL(path))
			raw, err = json.Marshal(s)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if raw, err = allowNull(raw, reflect.TypeOf(types[path])); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		if files[name], err = canonical(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	entries, err := handwritten.ReadDir("handwritten")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !used[e.Name()] {
			return nil, fmt.Errorf("handwritten/%s belongs to no command marked outputschema.Handwritten", e.Name())
		}
	}
	return files, nil
}

func reflector() *jsonschema.Reflector {
	return &jsonschema.Reflector{}
}

// envelopeSchema reflects clierr.Envelope and pins what reflection cannot see:
// the closed set of codes.
func envelopeSchema() ([]byte, error) {
	s := reflector().Reflect(&clierr.Envelope{})
	s.ID = jsonschema.ID(outputschema.URL(outputschema.EnvelopePath))
	body, ok := s.Definitions["EnvelopeBody"]
	if !ok {
		return nil, fmt.Errorf("envelope schema has no EnvelopeBody definition")
	}
	code, ok := body.Properties.Get("code")
	if !ok {
		return nil, fmt.Errorf("EnvelopeBody has no code property")
	}
	code.Enum = nil
	for _, k := range clierr.Kinds() {
		code.Enum = append(code.Enum, string(k))
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	// No allowNull here: the envelope contract is that details is always an
	// array and never null (NewEnvelope guarantees it).
	return canonical(raw)
}

// canonical re-encodes JSON with sorted keys and 2-space indentation.
func canonical(raw []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// write replaces the *.v1.json files in dir with files.
func write(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	old, err := filepath.Glob(filepath.Join(dir, "*.v1.json"))
	if err != nil {
		return err
	}
	for _, f := range old {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// nullableFields walks t and records, per struct (keyed by the name invopop
// gives its $defs entry), every JSON property whose Go type is a slice, map or
// pointer and is not omitempty/omitzero: encoding/json writes null for a nil
// value there, so the schema must accept it.
func nullableFields(t reflect.Type, seen map[reflect.Type]bool, out map[string]map[string]bool) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	fields := out[t.Name()]
	if fields == nil {
		fields = map[string]bool{}
		out[t.Name()] = fields
	}
	var walk func(st reflect.Type)
	walk = func(st reflect.Type) {
		for i := range st.NumField() {
			f := st.Field(i)
			tag, hasTag := f.Tag.Lookup("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			if f.Anonymous && name == "" {
				ft := f.Type
				if ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
					continue
				}
			}
			if !f.IsExported() {
				continue
			}
			if !hasTag || name == "" {
				name = f.Name
			}
			switch f.Type.Kind() {
			case reflect.Slice, reflect.Map, reflect.Pointer:
				if !strings.Contains(","+opts+",", ",omitempty,") && !strings.Contains(","+opts+",", ",omitzero,") {
					fields[name] = true
				}
			}
			nullableFields(f.Type, seen, out)
		}
	}
	walk(t)
}

// allowNull rewrites the reflected schema so every property recorded by
// nullableFields also accepts null: "type": "x" becomes ["x","null"], and a
// $ref becomes anyOf [$ref, null].
func allowNull(raw []byte, root reflect.Type) ([]byte, error) {
	nl := map[string]map[string]bool{}
	nullableFields(root, map[reflect.Type]bool{}, nl)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	defs, _ := doc["$defs"].(map[string]any)
	for name, props := range nl {
		def, _ := defs[name].(map[string]any)
		properties, _ := def["properties"].(map[string]any)
		for prop := range props {
			ps, ok := properties[prop].(map[string]any)
			if !ok {
				continue
			}
			switch typ := ps["type"].(type) {
			case string:
				ps["type"] = []any{typ, "null"}
			case nil:
				if ref, ok := ps["$ref"]; ok {
					delete(ps, "$ref")
					ps["anyOf"] = []any{map[string]any{"$ref": ref}, map[string]any{"type": "null"}}
				}
			}
		}
	}
	return json.Marshal(doc)
}
