package mapping

import (
	"strings"

	"github.com/openfga/mapper/language"
)

// matchesFilter reports whether a tuple falls under a tuple filter. It is
// mapper's isTupleCoveredByFilter and, equally, OpenFGA Read semantics: user
// and relation must match when the filter sets them, and the object must match
// exactly unless the filter's object is a bare `type:` prefix (non-empty type,
// a single trailing colon), which matches every object of that type.
func matchesFilter(t language.Tuple, f language.TupleFilter) bool {
	if f.User != "" && f.User != t.User {
		return false
	}
	if f.Relation != "" && f.Relation != t.Relation {
		return false
	}
	if f.Object != "" {
		if isTypePrefix(f.Object) {
			return strings.HasPrefix(t.Object, f.Object)
		}
		return f.Object == t.Object
	}
	return true
}

func isTypePrefix(s string) bool {
	i := strings.IndexByte(s, ':')
	return i > 0 && i == len(s)-1
}
