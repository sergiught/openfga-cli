package mapping

import (
	"context"
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/go-openfga/openfga"
)

// Backfill and drift ask for higher consistency on every tuple read
// themselves, not through the client's default: a read served from a cache
// could miss a tuple and plan a duplicate or a wrong delete. The client here
// has no default, so the fake store, which refuses any read without higher
// consistency, answers only reads that ask for it.
func TestStoreReadsAskForHigherConsistency(t *testing.T) {
	fs := newFakeStore(t, tenantTuple("user:anne"))
	cl, err := openfga.NewClient(fs.srv.URL, openfga.WithStoreID(applyStoreID))
	if err != nil {
		t.Fatal(err)
	}
	s := storeClient{cl}
	ctx := context.Background()
	reads := map[string]func() error{
		"ReadTuples": func() error {
			_, err := s.ReadTuples(ctx, language.TupleFilter{Object: "tenant:acme"})
			return err
		},
		"ReadAll": func() error { _, err := s.ReadAll(ctx); return err },
		"ReadKept": func() error {
			_, _, err := s.ReadKept(ctx, func(language.Tuple) bool { return true }, nil)
			return err
		},
		"ReadMatching": func() error {
			_, err := s.ReadMatching(ctx, wantMember("user:anne", "user:bob"), nil)
			return err
		},
	}
	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			if err := read(); err != nil {
				t.Fatalf("read without higher consistency: %v", err)
			}
		})
	}

	// A store too large to read whole, with one object crowded past its page
	// budget, takes ReadMatching through all three of its reads: the first
	// pages of the whole store, each object and relation, then each key.
	t.Run("ReadMatching by object and key", func(t *testing.T) {
		fs := newFakeStore(t,
			tenantTuple("user:u1"), tenantTuple("user:n1"), tenantTuple("user:n2"), tenantTuple("user:u2"),
			map[string]any{"user": "user:u1", "relation": "reader", "object": "doc:d1"})
		fs.bigStore, fs.pageSize = true, 1
		cl, err := openfga.NewClient(fs.srv.URL, openfga.WithStoreID(applyStoreID))
		if err != nil {
			t.Fatal(err)
		}
		want := append(wantMember("user:u1", "user:u2"),
			language.Tuple{User: "user:u1", Relation: "reader", Object: "doc:d1"})
		if _, err := (storeClient{cl}).ReadMatching(ctx, want, nil); err != nil {
			t.Fatalf("read without higher consistency: %v", err)
		}
		if len(keyReads(fs.readRequests())) == 0 {
			t.Fatal("no key reads: the fixture no longer reaches the last phase")
		}
	})
}
