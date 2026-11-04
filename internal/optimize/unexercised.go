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
