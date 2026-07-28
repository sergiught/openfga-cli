package playground

import (
	"context"
	"io"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

// The tuples pane's /read request carries a tuple_key filter only when a
// filter is active — same shape the CLI's `tuples read` sends.
func TestTupleReadRequestNoFilter(t *testing.T) {
	req := tupleReadRequest(tupleFilter{})
	if req.TupleKey != nil {
		t.Fatalf("no filter should mean no tuple_key, got %+v", req.TupleKey)
	}
	if req.PageSize != 100 {
		t.Fatalf("page size should stay 100, got %d", req.PageSize)
	}
}

func TestTupleReadRequestWithFilter(t *testing.T) {
	req := tupleReadRequest(tupleFilter{user: "user:anne", object: "document:"})
	if req.TupleKey == nil {
		t.Fatal("active filter should set tuple_key")
	}
	if req.TupleKey.User != "user:anne" || req.TupleKey.Relation != "" || req.TupleKey.Object != "document:" {
		t.Fatalf("tuple_key = %+v, want user:anne / \"\" / document:", req.TupleKey)
	}
}

// vFilterObject allows type:id AND bare-type "type:" (unlike vObject), stays
// lenient on empty, and rejects colon-less or userset-shaped values.
func TestVFilterObject(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", true},
		{"document:roadmap", true},
		{"document:", true},
		{"  document:  ", true},
		{"document", false},
		{"document:1#viewer", false},
		{":roadmap", false},
	} {
		err := vFilterObject(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("vFilterObject(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// validateTupleFilter mirrors the server's /read rule: object type required,
// and object id and user not both empty. All-empty is valid (it clears).
func TestValidateTupleFilter(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    tupleFilter
		ok   bool
	}{
		{"empty clears", tupleFilter{}, true},
		{"object type:id", tupleFilter{object: "document:roadmap"}, true},
		{"bare type + user", tupleFilter{object: "document:", user: "user:anne"}, true},
		{"type:id + relation", tupleFilter{object: "document:roadmap", relation: "viewer"}, true},
		{"bare type alone", tupleFilter{object: "document:"}, false},
		{"user alone", tupleFilter{user: "user:anne"}, false},
		{"relation alone", tupleFilter{relation: "viewer"}, false},
		{"user + relation, no object", tupleFilter{user: "user:anne", relation: "viewer"}, false},
		// The server splits the object on its first colon, so a colon-less or
		// leading-colon object has no type and is rejected even with a user set.
		{"colon-less object with user", tupleFilter{object: "document", user: "user:anne"}, false},
		{"leading colon with user", tupleFilter{object: ":roadmap", user: "user:anne"}, false},
	} {
		err := validateTupleFilter(tc.f)
		if (err == nil) != tc.ok {
			t.Errorf("%s: validateTupleFilter(%+v) = %v, want ok=%v", tc.name, tc.f, err, tc.ok)
		}
	}
}

// A filter is store-specific: switching stores must clear it.
func TestSelectStoreClearsTupleFilter(t *testing.T) {
	configtest.Isolate(t)
	cl, _ := openfga.NewClient("http://localhost:8080")
	a := cli.New(log.New(io.Discard), config.New(), "test")
	m := newModel(context.Background(), a, cl, "store-1", "")
	m.tupleFilter = tupleFilter{object: "document:roadmap"}

	m.selectStore(openfga.Store{ID: "store-2", Name: "other"})

	if m.tupleFilter.active() {
		t.Fatalf("store switch must clear the tuple filter, got %+v", m.tupleFilter)
	}
}
