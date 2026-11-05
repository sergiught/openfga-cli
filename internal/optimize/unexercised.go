package optimize

import (
	"sort"
	"strings"
)

// Unexercised returns the sorted, de-duplicated "type#relation" of every
// applied finding whose relation depends on a restriction the benchmark
// skipped (or is itself the skipped relation). skipped holds bench.Source
// notices of the form "type#relation: reason". Dependents are computed on
// the rewritten model (rw.After), since that is the model benchmarked.
func Unexercised(m *Model, applied []Finding, skipped []string) []string {
	var targets []string
	for _, s := range skipped {
		rel, _, _ := strings.Cut(s, ": ")
		targets = append(targets, rel)
	}
	if len(targets) == 0 || m == nil {
		return nil
	}
	deps := Dependents(m, targets)
	seen := map[string]bool{}
	var out []string
	for _, f := range applied {
		key := f.Type + "#" + f.Relation
		if seen[key] || !deps[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// UnexercisedByChecks returns the sorted, de-duplicated "type#relation" of
// every applied finding that no check reaches. A check on type#relation
// reaches a rewritten relation X when it is X itself or depends on X (is in
// Dependents(m, [X])). checks holds the checked "type#relation" keys, and
// m is the rewritten model, since that is the model benchmarked.
func UnexercisedByChecks(m *Model, applied []Finding, checks []string) []string {
	if m == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range applied {
		key := f.Type + "#" + f.Relation
		if seen[key] {
			continue
		}
		seen[key] = true
		deps := Dependents(m, []string{key})
		reached := false
		for _, c := range checks {
			if c == key || deps[c] {
				reached = true
				break
			}
		}
		if !reached {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
