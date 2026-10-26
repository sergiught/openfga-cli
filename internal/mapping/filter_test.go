package mapping

import (
	"testing"

	"github.com/openfga/mapper/language"
)

func TestMatchesFilter(t *testing.T) {
	tup := language.Tuple{User: "user:1", Relation: "member", Object: "team:1"}
	tests := []struct {
		name string
		f    language.TupleFilter
		want bool
	}{
		{"empty filter matches all", language.TupleFilter{}, true},
		{"user mismatch", language.TupleFilter{User: "user:2"}, false},
		{"relation mismatch", language.TupleFilter{Relation: "admin"}, false},
		{"exact object", language.TupleFilter{Object: "team:1"}, true},
		{"other object", language.TupleFilter{Object: "team:2"}, false},
		{"type prefix", language.TupleFilter{Object: "team:"}, true},
		{"other type prefix", language.TupleFilter{Object: "org:"}, false},
		{"bare colon is not a prefix", language.TupleFilter{Object: ":"}, false},
		{"all fields", language.TupleFilter{User: "user:1", Relation: "member", Object: "team:"}, true},
	}
	for _, tc := range tests {
		if got := matchesFilter(tup, tc.f); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
