package tuple

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/fga"
)

// bulkFormat is the on-disk encoding of a bulk --file payload.
type bulkFormat string

// Supported bulk --file encodings.
const (
	formatJSON  bulkFormat = "json"
	formatJSONL bulkFormat = "jsonl"
	formatYAML  bulkFormat = "yaml"
	formatCSV   bulkFormat = "csv"
)

// bulkFormatList is the accepted --file-format values, in help/error order.
const bulkFormatList = "json, jsonl, yaml, csv"

// csvColumns is the CSV header contract, matching the official `fga` CLI.
// Order in the file does not matter; csvRequired must all be present.
var csvColumns = []string{
	"user_type", "user_id", "user_relation", "relation",
	"object_type", "object_id", "condition_name", "condition_context",
}

// csvRequired are the columns a CSV must carry; the rest are optional.
var csvRequired = []string{"user_type", "user_id", "relation", "object_type", "object_id"}

// bulkFileHelp documents the --file formats and their schemas in `--help`, so
// the accepted CSV headers and the read-export round-trip are discoverable
// without leaving the terminal.
const bulkFileHelp = `--file accepts json, jsonl, yaml and csv. The format is inferred from the file
extension (.json, .jsonl/.ndjson, .yaml/.yml, .csv) and can be forced with
--file-format. Stdin ("--file -") has no extension, so it is read as json
unless --file-format says otherwise.

json and yaml accept three shapes:
  - an array of {user, relation, object, condition} tuples
  - an object {"tuples": [ ... ]}
  - the {"key": {...}, "timestamp": ...} array that 'ofga tuples read --json'
    emits, so an export feeds straight back in (timestamp is ignored)

jsonl is one such tuple (or read-export entry) per line.

csv columns, in any order (unknown columns are an error):
  user_type, user_id, user_relation, relation, object_type, object_id,
  condition_name, condition_context
  user_type, user_id, relation, object_type and object_id are required.
  user_relation builds a userset subject (group:eng#member).
  condition_context is a JSON object inside a single csv field.

Unknown fields are rejected rather than silently ignored.`

// tupleInput is one relationship tuple as it appears in a bulk --file: the
// canonical user/relation/object triple, plus an optional ABAC condition.
// Unknown fields are rejected (see parseTupleFile) so a mistyped field name
// surfaces as a parse error instead of silently vanishing.
type tupleInput struct {
	User      string          `json:"user"                yaml:"user"`
	Relation  string          `json:"relation"            yaml:"relation"`
	Object    string          `json:"object"              yaml:"object"`
	Condition *conditionInput `json:"condition,omitempty" yaml:"condition,omitempty"`
}

// conditionInput is a bulk-file tuple's optional ABAC condition: a condition
// name plus its context parameters.
type conditionInput struct {
	Name    string         `json:"name"              yaml:"name"`
	Context map[string]any `json:"context,omitempty" yaml:"context,omitempty"`
}

// exportInput is one entry of the array `ofga tuples read --json` emits, so a
// read export feeds straight back into a bulk write. Timestamp is accepted and
// ignored; it is declared only so strict decoding does not reject it.
type exportInput struct {
	Key       tupleInput `json:"key"                 yaml:"key"`
	Timestamp any        `json:"timestamp,omitempty" yaml:"timestamp,omitempty"`
}

// resolveBulkFormat picks the bulk --file encoding: an explicit --file-format
// wins, otherwise it is inferred from the file extension. An unrecognised (or
// absent, as for stdin) extension falls back to JSON, which is what --file has
// always accepted.
func resolveBulkFormat(file, override string) (bulkFormat, error) {
	if override != "" {
		switch f := bulkFormat(strings.ToLower(strings.TrimSpace(override))); f {
		case formatJSON, formatJSONL, formatYAML, formatCSV:
			return f, nil
		default:
			return "", clierr.WithCode(clierr.CodeUsage,
				fmt.Errorf("--file-format must be one of %s (got %q)", bulkFormatList, override))
		}
	}
	switch strings.ToLower(filepath.Ext(file)) {
	case ".jsonl", ".ndjson":
		return formatJSONL, nil
	case ".yaml", ".yml":
		return formatYAML, nil
	case ".csv":
		return formatCSV, nil
	default:
		return formatJSON, nil
	}
}

// parseTupleFile decodes a bulk --file payload in the given format. source is
// the --file value, used only to build the error message.
func parseTupleFile(data []byte, format bulkFormat, source string) ([]tupleInput, error) {
	switch format {
	case formatJSON:
		return parseJSONTuples(data, source)
	case formatJSONL:
		return parseJSONLTuples(data, source)
	case formatYAML:
		return parseYAMLTuples(data, source)
	case formatCSV:
		return parseCSVTuples(data, source)
	default:
		return nil, clierr.WithCode(clierr.CodeUsage,
			fmt.Errorf("--file-format must be one of %s (got %q)", bulkFormatList, format))
	}
}

// parseJSONTuples accepts the three JSON shapes: a bare array of tuples, an
// object {"tuples":[...]}, and the {key,timestamp} array `tuples read --json`
// emits. The shape is sniffed first so the strict decode that follows reports
// the precise field error instead of a generic "none of three schemas matched".
func parseJSONTuples(data []byte, source string) ([]tupleInput, error) {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0:
		return nil, schemaErr(source, formatJSON, errors.New("input is empty"))
	case trimmed[0] == '{':
		var wrapper struct {
			Tuples []tupleInput `json:"tuples"`
		}
		if err := fga.DecodeStrictJSON(trimmed, &wrapper); err != nil {
			return nil, schemaErr(source, formatJSON, err)
		}
		return wrapper.Tuples, nil
	case trimmed[0] == '[':
		if jsonArrayIsExport(trimmed) {
			var exports []exportInput
			if err := fga.DecodeStrictJSON(trimmed, &exports); err != nil {
				return nil, schemaErr(source, formatJSON, err)
			}
			return exportKeys(exports), nil
		}
		var raw []tupleInput
		if err := fga.DecodeStrictJSON(trimmed, &raw); err != nil {
			return nil, schemaErr(source, formatJSON, err)
		}
		return raw, nil
	default:
		return nil, schemaErr(source, formatJSON, errors.New("input is neither a JSON array nor a JSON object"))
	}
}

// jsonArrayIsExport reports whether a JSON array's first element looks like a
// `tuples read --json` entry (it has a "key" member) rather than a bare tuple.
func jsonArrayIsExport(data []byte) bool {
	var elems []json.RawMessage
	if err := json.Unmarshal(data, &elems); err != nil || len(elems) == 0 {
		return false
	}
	var first map[string]json.RawMessage
	if err := json.Unmarshal(elems[0], &first); err != nil {
		return false
	}
	_, ok := first["key"]
	return ok
}

// exportKeys drops the timestamps from a read export, leaving the tuples.
func exportKeys(exports []exportInput) []tupleInput {
	out := make([]tupleInput, 0, len(exports))
	for _, e := range exports {
		out = append(out, e.Key)
	}
	return out
}

// parseJSONLTuples decodes one JSON object per line, accepting either a bare
// tuple or a read-export {key,timestamp} entry. Blank lines are skipped and a
// bad line is reported by its line number.
func parseJSONLTuples(data []byte, source string) ([]tupleInput, error) {
	var out []tupleInput
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxJSONLLine)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		t, err := decodeJSONLLine(raw)
		if err != nil {
			return nil, schemaErr(source, formatJSONL, fmt.Errorf("line %d: %w", line, err))
		}
		out = append(out, t)
	}
	if err := sc.Err(); err != nil {
		return nil, schemaErr(source, formatJSONL, fmt.Errorf("line %d: %w", line+1, err))
	}
	return out, nil
}

// maxJSONLLine caps a single JSONL line so a pathological input cannot force an
// unbounded allocation; a tuple with a condition context stays far below it.
const maxJSONLLine = 4 << 20

// decodeJSONLLine decodes one JSONL record, in either accepted shape.
func decodeJSONLLine(raw []byte) (tupleInput, error) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return tupleInput{}, err
	}
	if _, ok := probe["key"]; ok {
		var e exportInput
		if err := fga.DecodeStrictJSON(raw, &e); err != nil {
			return tupleInput{}, err
		}
		return e.Key, nil
	}
	var t tupleInput
	if err := fga.DecodeStrictJSON(raw, &t); err != nil {
		return tupleInput{}, err
	}
	return t, nil
}

// parseYAMLTuples accepts the same three shapes as JSON, sniffing the document
// before decoding it strictly.
func parseYAMLTuples(data []byte, source string) ([]tupleInput, error) {
	var probe any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, schemaErr(source, formatYAML, err)
	}
	switch shape := probe.(type) {
	case nil:
		return nil, schemaErr(source, formatYAML, errors.New("input is empty"))
	case map[string]any:
		var wrapper struct {
			Tuples []tupleInput `yaml:"tuples"`
		}
		if err := decodeYAMLStrict(data, &wrapper); err != nil {
			return nil, schemaErr(source, formatYAML, err)
		}
		return wrapper.Tuples, nil
	case []any:
		if yamlSeqIsExport(shape) {
			var exports []exportInput
			if err := decodeYAMLStrict(data, &exports); err != nil {
				return nil, schemaErr(source, formatYAML, err)
			}
			return exportKeys(exports), nil
		}
		var raw []tupleInput
		if err := decodeYAMLStrict(data, &raw); err != nil {
			return nil, schemaErr(source, formatYAML, err)
		}
		return raw, nil
	default:
		return nil, schemaErr(source, formatYAML, errors.New("input is neither a YAML sequence nor a YAML mapping"))
	}
}

// yamlSeqIsExport reports whether a YAML sequence's first item carries a "key"
// mapping, i.e. it is a read export rather than a list of bare tuples.
func yamlSeqIsExport(seq []any) bool {
	if len(seq) == 0 {
		return false
	}
	m, ok := seq[0].(map[string]any)
	if !ok {
		return false
	}
	_, ok = m["key"]
	return ok
}

// decodeYAMLStrict decodes a single YAML document into v, rejecting unknown
// fields and any trailing document so a mistyped key or a stray `---` block
// cannot be silently dropped.
func decodeYAMLStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return cleanYAMLErr(err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("unexpected trailing YAML document")
	} else if !errors.Is(err, io.EOF) {
		return cleanYAMLErr(err)
	}
	return nil
}

// cleanYAMLErr drops the Go type names yaml.v3 appends to unknown-field errors
// ("field objekt not found in type tuple.tupleInput"), which mean nothing to
// someone editing a tuples file.
func cleanYAMLErr(err error) error {
	msg := err.Error()
	if !strings.Contains(msg, " in type ") {
		return err
	}
	lines := strings.Split(msg, "\n")
	for i, l := range lines {
		if cut, _, ok := strings.Cut(l, " in type "); ok {
			lines[i] = cut
		}
	}
	return errors.New(strings.Join(lines, "\n"))
}

// parseCSVTuples decodes the official `fga` CLI's CSV contract. Column order is
// free, optional columns may be absent, an unknown column is an error, and any
// malformed row is reported by its line number.
func parseCSVTuples(data []byte, source string) ([]tupleInput, error) {
	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, schemaErr(source, formatCSV, errors.New("input is empty"))
		}
		return nil, schemaErr(source, formatCSV, csvLineErr(err))
	}
	index, err := csvHeaderIndex(header)
	if err != nil {
		return nil, schemaErr(source, formatCSV, err)
	}
	var out []tupleInput
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, schemaErr(source, formatCSV, csvLineErr(err))
		}
		line, _ := r.FieldPos(0)
		t, err := csvRow(record, index)
		if err != nil {
			return nil, schemaErr(source, formatCSV, fmt.Errorf("line %d: %w", line, err))
		}
		out = append(out, t)
	}
	return out, nil
}

// csvHeaderIndex maps each accepted column name to its position, rejecting
// unknown columns and requiring the five mandatory ones.
func csvHeaderIndex(header []string) (map[string]int, error) {
	index := make(map[string]int, len(header))
	for i, raw := range header {
		// A UTF-8 BOM ahead of the first column would otherwise make it unknown.
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff")))
		if !slices.Contains(csvColumns, name) {
			return nil, fmt.Errorf("line 1: unknown column %q", raw)
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("line 1: duplicate column %q", name)
		}
		index[name] = i
	}
	var missing []string
	for _, name := range csvRequired {
		if _, ok := index[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("line 1: missing required column(s) %s", strings.Join(missing, ", "))
	}
	return index, nil
}

// csvRow turns one CSV record into a tupleInput.
func csvRow(record []string, index map[string]int) (tupleInput, error) {
	field := func(name string) string {
		i, ok := index[name]
		if !ok || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}
	for _, name := range csvRequired {
		if field(name) == "" {
			return tupleInput{}, fmt.Errorf("column %s is empty", name)
		}
	}
	user := field("user_type") + ":" + field("user_id")
	if rel := field("user_relation"); rel != "" {
		user += "#" + rel
	}
	t := tupleInput{
		User:     user,
		Relation: field("relation"),
		Object:   field("object_type") + ":" + field("object_id"),
	}
	name, rawCtx := field("condition_name"), field("condition_context")
	if name == "" {
		if rawCtx != "" {
			return tupleInput{}, errors.New("condition_context is set but condition_name is empty")
		}
		return t, nil
	}
	t.Condition = &conditionInput{Name: name}
	if rawCtx != "" {
		var ctx map[string]any
		if err := json.Unmarshal([]byte(rawCtx), &ctx); err != nil {
			return tupleInput{}, fmt.Errorf("condition_context must be a JSON object: %w", err)
		}
		t.Condition.Context = ctx
	}
	return t, nil
}

// csvLineErr rewrites encoding/csv's error to lead with the offending line so
// it reads the same way as the errors this package raises itself.
func csvLineErr(err error) error {
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		if errors.Is(pe.Err, csv.ErrFieldCount) {
			return fmt.Errorf("line %d: wrong number of fields", pe.Line)
		}
		return fmt.Errorf("line %d: %w", pe.Line, pe.Err)
	}
	return err
}

// schemaErr wraps a decode failure with the source, the format it was read as,
// and the schemas that format accepts — with three shapes in play, the bare
// decoder error alone does not tell the user what the file should have looked
// like.
func schemaErr(source string, format bulkFormat, cause error) error {
	label := fmt.Sprintf("file %q", source)
	if source == "-" {
		label = "from stdin"
	}
	msg := fmt.Sprintf("parse tuples %s as %s: %v; expected %s", label, format, cause, schemaHint(format))
	if source == "-" && format == formatJSON {
		msg += ". stdin has no extension, so it is read as json; pass --file-format to change that"
	} else {
		msg += ". Use --file-format to override the format inferred from the file extension"
	}
	return clierr.WithCode(clierr.CodeUsage, errors.New(msg))
}

// schemaHint describes the shapes a format accepts.
func schemaHint(format bulkFormat) string {
	const readExport = `the {"key":{…},"timestamp":…} array emitted by 'ofga tuples read --json'`
	switch format {
	case formatJSONL:
		return `one JSON object per line: either {user,relation,object,condition} or an entry of ` + readExport
	case formatYAML:
		return `a YAML sequence of {user,relation,object,condition} mappings, a mapping {"tuples":[…]}, or the YAML form of ` + readExport
	case formatCSV:
		return "a header row drawn from " + strings.Join(csvColumns, ",") +
			" (order does not matter; " + strings.Join(csvRequired, ",") + " are required)"
	case formatJSON:
		return `a JSON array of {user,relation,object,condition} objects, an object {"tuples":[…]}, or ` + readExport
	default:
		return "one of " + bulkFormatList
	}
}

// encodeTupleFile writes keys in the given format, producing a file that
// parseTupleFile reads back unchanged. It backs --failed-file, so the output
// must be directly re-runnable as a --file input.
func encodeTupleFile(w io.Writer, keys []openfga.TupleKey, format bulkFormat) error {
	inputs := make([]tupleInput, 0, len(keys))
	for _, k := range keys {
		t := tupleInput{User: k.User, Relation: k.Relation, Object: k.Object}
		if k.Condition != nil {
			t.Condition = &conditionInput{Name: k.Condition.Name, Context: k.Condition.Context}
		}
		inputs = append(inputs, t)
	}
	switch format {
	case formatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(inputs)
	case formatJSONL:
		enc := json.NewEncoder(w)
		for _, t := range inputs {
			if err := enc.Encode(t); err != nil {
				return err
			}
		}
		return nil
	case formatYAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		if err := enc.Encode(inputs); err != nil {
			return err
		}
		return enc.Close()
	case formatCSV:
		return encodeCSVTuples(w, inputs)
	default:
		return fmt.Errorf("cannot encode tuples as %q", format)
	}
}

// encodeCSVTuples writes the full CSV header and one row per tuple, splitting
// the user and object back into their type/id (and userset relation) parts.
func encodeCSVTuples(w io.Writer, inputs []tupleInput) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvColumns); err != nil {
		return err
	}
	for _, t := range inputs {
		userType, userID, userRel := splitUser(t.User)
		objType, objID := splitTypeID(t.Object)
		name, ctx := "", ""
		if t.Condition != nil {
			name = t.Condition.Name
			if len(t.Condition.Context) > 0 {
				raw, err := json.Marshal(t.Condition.Context)
				if err != nil {
					return err
				}
				ctx = string(raw)
			}
		}
		if err := cw.Write([]string{userType, userID, userRel, t.Relation, objType, objID, name, ctx}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// splitUser breaks "group:eng#member" into its type, id and userset relation.
func splitUser(user string) (typ, id, relation string) {
	if i := strings.Index(user, "#"); i >= 0 {
		relation = user[i+1:]
		user = user[:i]
	}
	typ, id = splitTypeID(user)
	return typ, id, relation
}

// splitTypeID breaks "doc:1" into its type and id, on the first colon.
func splitTypeID(s string) (typ, id string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}
