package query

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/command/tuple"
	"github.com/sergiught/openfga-cli/internal/fga"
	"github.com/sergiught/openfga-cli/internal/readlimit"
)

// batchFileUsage is the --file flag text of batch-check.
const batchFileUsage = `read checks from a JSON array or JSONL file ("-" reads stdin); each entry is {user, relation, object, context?, contextual_tuples?, correlation_id?}`

// maxBatchLine caps one JSONL line.
const maxBatchLine = 4 << 20

// batchEntry is one check of a batch-check --file. Unknown fields are rejected.
type batchEntry struct {
	User             string            `json:"user"`
	Relation         string            `json:"relation"`
	Object           string            `json:"object"`
	Context          map[string]any    `json:"context,omitempty"`
	ContextualTuples []json.RawMessage `json:"contextual_tuples,omitempty"` // each decoded with the tuple-file shape
	CorrelationID    string            `json:"correlation_id,omitempty"`
}

// batchProblem is one validation failure of a batch file entry.
type batchProblem struct {
	index          int
	field, problem string
}

// readBatchFile reads checks from path ("-" reads stdin), validating every
// entry before returning. Entries without a correlation ID get no ID here;
// the caller assigns them.
func readBatchFile(path string, stdin io.Reader) ([]openfga.BatchCheckItem, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = readlimit.All(stdin, readlimit.Document, "batch checks from stdin")
	} else {
		data, err = readlimit.File(path, readlimit.Document, "batch-check file")
	}
	if err != nil {
		return nil, err
	}
	entries, err := decodeBatchEntries(data, path)
	if err != nil {
		return nil, malformedBatch(err)
	}
	if len(entries) == 0 {
		return nil, malformedBatch(fmt.Errorf("batch-check file %s: no checks", path))
	}

	var problems []batchProblem
	items := make([]openfga.BatchCheckItem, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for i, e := range entries {
		add := func(field, problem string) { problems = append(problems, batchProblem{i, field, problem}) }
		if strings.TrimSpace(e.User) == "" {
			add("user", "is required")
		} else if err := fga.ValidateUserRef(e.User); err != nil {
			add("user", err.Error())
		}
		if strings.TrimSpace(e.Relation) == "" {
			add("relation", "is required")
		}
		if strings.TrimSpace(e.Object) == "" {
			add("object", "is required")
		} else if err := fga.ValidateObjectRef(e.Object); err != nil {
			add("object", err.Error())
		}
		if e.CorrelationID != "" {
			if _, dup := seen[e.CorrelationID]; dup {
				add("correlation_id", fmt.Sprintf("duplicate correlation id %q", e.CorrelationID))
			}
			seen[e.CorrelationID] = struct{}{}
		}
		var keys []openfga.TupleKey
		for j, raw := range e.ContextualTuples {
			key, err := tuple.DecodeTuple(raw)
			if err != nil {
				add("contextual_tuples", fmt.Sprintf("tuple %d: %v", j, err))
				continue
			}
			keys = append(keys, key)
		}
		item := openfga.BatchCheckItem{
			TupleKey:      openfga.CheckRequestTupleKey{User: strings.TrimSpace(e.User), Relation: strings.TrimSpace(e.Relation), Object: strings.TrimSpace(e.Object)},
			CorrelationID: e.CorrelationID,
			Context:       e.Context,
		}
		if len(keys) > 0 {
			item.ContextualTuples = &openfga.ContextualTupleKeys{TupleKeys: keys}
		}
		items[i] = item
	}
	if len(problems) > 0 {
		return nil, validationErr(path, problems)
	}
	return items, nil
}

func malformedBatch(err error) error {
	return clierr.WithKind(clierr.KindValidation, clierr.WithCode(clierr.CodeUsage, err))
}

func validationErr(path string, problems []batchProblem) error {
	first := problems[0]
	msg := fmt.Sprintf("batch-check file %s: entry %d: %s: %s", path, first.index, first.field, first.problem)
	if n := len(problems) - 1; n > 0 {
		msg += fmt.Sprintf(" (and %d more)", n)
	}
	details := make([]map[string]any, len(problems))
	for i, p := range problems {
		details[i] = map[string]any{"index": p.index, "field": p.field, "problem": p.problem}
	}
	return clierr.WithKind(clierr.KindValidation,
		clierr.WithDetails(clierr.WithCode(clierr.CodeUsage, errors.New(msg)), details...))
}

// decodeBatchEntries decodes the file: .jsonl is JSONL; .json, "-" and any
// other name is a JSON array when the first non-space byte is '[', else JSONL.
func decodeBatchEntries(data []byte, path string) ([]batchEntry, error) {
	label := "batch-check file " + path
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("%s: input is empty", label)
	}
	var entries []batchEntry
	if !strings.EqualFold(filepath.Ext(path), ".jsonl") && trimmed[0] == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		for i, raw := range raws {
			var e batchEntry
			if err := fga.DecodeStrictJSON(raw, &e); err != nil {
				return nil, fmt.Errorf("%s: entry %d: %w", label, i, err)
			}
			entries = append(entries, e)
		}
		return entries, nil
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), maxBatchLine)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var e batchEntry
		if err := fga.DecodeStrictJSON(raw, &e); err != nil {
			return nil, fmt.Errorf("%s: line %d: %w", label, line, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: line %d: %w", label, line+1, err)
	}
	return entries, nil
}

// mergeContextual returns the global contextual tuples followed by an item's
// own, or nil when both are empty.
func mergeContextual(global, own *openfga.ContextualTupleKeys) *openfga.ContextualTupleKeys {
	var keys []openfga.TupleKey
	if global != nil {
		keys = append(keys, global.TupleKeys...)
	}
	if own != nil {
		keys = append(keys, own.TupleKeys...)
	}
	if len(keys) == 0 {
		return nil
	}
	return &openfga.ContextualTupleKeys{TupleKeys: keys}
}

// assignCorrelationIDs fills in missing correlation IDs with "c<n>", skipping
// any ID already used.
func assignCorrelationIDs(items []openfga.BatchCheckItem) {
	used := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.CorrelationID != "" {
			used[it.CorrelationID] = struct{}{}
		}
	}
	n := 0
	for i := range items {
		if items[i].CorrelationID != "" {
			continue
		}
		for {
			id := fmt.Sprintf("c%d", n)
			n++
			if _, taken := used[id]; !taken {
				items[i].CorrelationID = id
				used[id] = struct{}{}
				break
			}
		}
	}
}
