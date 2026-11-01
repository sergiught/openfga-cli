package bench

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

// FromWorkspace builds a benchmark Source from a loaded ofga test workspace:
// the union of every test's resolved tuples (identical keys collapsed) and one
// Check per (user, relation, object) asserted by any check case. Notices
// describe anything dropped: tests on a different inline model, and tuples
// that share a key but differ in condition (the first wins).
func FromWorkspace(ws *modeltest.Workspace) (*Source, []string, error) {
	src := &Source{Kind: "workspace"}
	var notices []string

	var manifestModel string
	if ws.Manifest != nil && ws.Manifest.Model != "" {
		manifestModel = filepath.Clean(filepath.Join(ws.Root, ws.Manifest.Model))
	}

	seen := map[string]string{} // triple -> full key (with condition)
	seenChecks := map[Check]bool{}
	var tuples []modeltest.TupleKey

	for _, tf := range ws.TestFiles {
		id := ws.TestFileID(tf)
		if tf.Model != "" && filepath.Clean(filepath.Join(filepath.Dir(tf.Path), tf.Model)) != manifestModel {
			why := "not the workspace model"
			if manifestModel == "" {
				why = "manifest has no model"
			}
			notices = append(notices, fmt.Sprintf("skipped test file %s: it uses its own model %q, %s", id, tf.Model, why))
			continue
		}
		for _, tt := range tf.Tests {
			resolved, err := modeltest.ResolveTestTuples(ws, tf, tt)
			if err != nil {
				return nil, nil, fmt.Errorf("%s/%s: %w", id, tt.Name, err)
			}
			for _, tk := range resolved {
				triple := tk.User + "|" + tk.Relation + "|" + tk.Object
				full, err := fullKey(triple, tk)
				if err != nil {
					return nil, nil, fmt.Errorf("%s/%s: %w", id, tt.Name, err)
				}
				prev, ok := seen[triple]
				switch {
				case !ok:
					seen[triple] = full
					tuples = append(tuples, tk)
				case prev != full:
					notices = append(notices, fmt.Sprintf("tuple %s %s %s in %s/%s conflicts with an earlier tuple that has a different condition; keeping the first", tk.User, tk.Relation, tk.Object, id, tt.Name))
				}
			}

			for _, cc := range tt.Check {
				if len(cc.Context) > 0 || len(cc.ContextualTuples) > 0 {
					n := fmt.Sprintf("skipped %s/%s check: uses context or contextual tuples", id, tt.Name)
					if !slices.Contains(notices, n) {
						notices = append(notices, n)
					}
					continue
				}
				users, objects, err := cc.Subjects()
				if err != nil {
					return nil, nil, fmt.Errorf("%s/%s: %w", id, tt.Name, err)
				}
				relations := make([]string, 0, len(cc.Assertions))
				for r := range cc.Assertions {
					relations = append(relations, r)
				}
				slices.Sort(relations)
				for _, u := range users {
					for _, o := range objects {
						for _, r := range relations {
							c := Check{
								Name:     fmt.Sprintf("%s/%s %s %s %s", id, tt.Name, u, r, o),
								User:     u,
								Relation: r,
								Object:   o,
							}
							if !seenChecks[c] {
								seenChecks[c] = true
								src.Checks = append(src.Checks, c)
							}
						}
					}
				}
			}
		}
	}

	proto, err := modeltest.ToProtoTuples(tuples)
	if err != nil {
		return nil, nil, err
	}
	src.Tuples = proto
	src.Skipped = notices
	return src, notices, nil
}

func fullKey(triple string, tk modeltest.TupleKey) (string, error) {
	if tk.Condition == nil {
		return triple, nil
	}
	ctx, err := json.Marshal(tk.Condition.Context)
	if err != nil {
		return "", fmt.Errorf("tuple %s: marshal condition context: %w", triple, err)
	}
	return triple + "|" + tk.Condition.Name + "|" + string(ctx), nil
}
