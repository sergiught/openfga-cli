package modeltest

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// setupScope wraps eng.Setup and returns the resulting Scope.
func setupScope(t *testing.T, eng Engine, model *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey) (Scope, error) {
	t.Helper()
	storeID, modelID, err := eng.Setup(context.Background(), model, tuples)
	if err != nil {
		return Scope{}, err
	}
	return Scope{StoreID: storeID, ModelID: modelID}, nil
}

func TestEmbeddedEngineChecksOwner(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	lm, err := loadModel(filepath.Join("testdata", "docs", "model.fga"))
	if err != nil {
		t.Fatal(err)
	}

	tuples := []*openfgav1.TupleKey{{User: "user:anne", Relation: "owner", Object: "document:1"}}
	sc, err := setupScope(t, eng, lm.Proto, tuples)
	if err != nil {
		t.Fatal(err)
	}

	got, err := eng.Check(context.Background(), sc, CheckReq{User: "user:anne", Relation: "viewer", Object: "document:1"})
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("owner should resolve viewer=true")
	}
}

// TestToIntAcceptsNumericTypes covers toInt's three accepted numeric shapes —
// int and int64 (as a manifest's YAML `server:` map would decode a plain
// integer) and float64 (as encoding/json would decode one) — plus its error
// on a non-numeric value.
func TestToIntAcceptsNumericTypes(t *testing.T) {
	cases := []struct {
		name    string
		val     any
		want    int
		wantErr bool
	}{
		{"int", 5, 5, false},
		{"int64", int64(7), 7, false},
		{"float64", float64(9), 9, false},
		{"string is an error", "5", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := toInt("k", tc.val)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("toInt(%v) = %d, <nil>, want an error", tc.val, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("toInt(%v): unexpected error: %v", tc.val, err)
			}
			if got != tc.want {
				t.Fatalf("toInt(%v) = %d, want %d", tc.val, got, tc.want)
			}
		})
	}
}

// TestToUint32RejectsNegative covers toUint32's extra bound over toInt: a
// negative value is rejected even though it parses fine as an int.
func TestToUint32RejectsNegative(t *testing.T) {
	if _, err := toUint32("k", -1); err == nil {
		t.Fatal("want an error for a negative value, got nil")
	}
	got, err := toUint32("k", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 3 {
		t.Fatalf("toUint32(3) = %d, want 3", got)
	}
}

// TestParseServerOptsUnknownKeyErrors covers the manifest `server:` map's
// unknown-key guard: a key that doesn't match any of the mapped options must
// return a clear error naming it, not be silently ignored.
func TestParseServerOptsUnknownKeyErrors(t *testing.T) {
	_, _, err := parseServerOpts(map[string]any{"not_a_real_option": 1})
	if err == nil {
		t.Fatal("want an error for an unknown server option, got nil")
	}
	if !strings.Contains(err.Error(), "not_a_real_option") {
		t.Fatalf("want the error to name the unknown key, got %v", err)
	}
}

// TestParseServerOptsNegativeValueRejected covers a known uint32-typed option
// (resolve_node_limit) given a negative value: it must be rejected, not
// silently truncated/wrapped into a huge uint32.
func TestParseServerOptsNegativeValueRejected(t *testing.T) {
	_, _, err := parseServerOpts(map[string]any{"resolve_node_limit": -1})
	if err == nil {
		t.Fatal("want an error for a negative resolve_node_limit, got nil")
	}
}

// TestNewEmbeddedEngineMaxTypesOptionIsWired covers the accepted-and-wired
// half of finding #4: a manifest `server: {max_types_per_authorization_model:
// N}` must actually reach the datastore, not just be accepted without error.
// It's driven behaviorally rather than by inspecting private state: with the
// limit set to 1, writing testdata/docs' 3-type model (user, folder,
// document) through Setup must fail; the same engine accepts a 1-type model.
func TestNewEmbeddedEngineMaxTypesOptionIsWired(t *testing.T) {
	eng, err := NewEmbeddedEngine(map[string]any{"max_types_per_authorization_model": 1})
	if err != nil {
		t.Fatalf("NewEmbeddedEngine with a known server option: %v", err)
	}
	defer eng.Close()

	lm, err := loadModel(filepath.Join("testdata", "docs", "model.fga"))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(lm.Proto.GetTypeDefinitions()); got != 3 {
		t.Fatalf("testdata/docs/model.fga: want 3 type definitions, got %d (test assumption invalid)", got)
	}

	if _, _, err := eng.Setup(context.Background(), lm.Proto, nil); err == nil {
		t.Fatal("want Setup to reject a 3-type model when max_types_per_authorization_model=1, got nil error")
	}

	oneType := &openfgav1.AuthorizationModel{
		SchemaVersion:   lm.Proto.GetSchemaVersion(),
		TypeDefinitions: []*openfgav1.TypeDefinition{{Type: "user"}},
	}
	if _, _, err := eng.Setup(context.Background(), oneType, nil); err != nil {
		t.Fatalf("want Setup to accept a 1-type model when max_types_per_authorization_model=1, got %v", err)
	}
}

// TestEmbeddedEngineSetupChunksTupleWrites covers the writeChunkSize-bounded
// Write loop in Setup: seeding more tuples than one chunk must not drop any
// of them. It writes writeChunkSize+5 grants (so the loop runs at least
// twice) and confirms, via ListUsers, that every one of them — including
// ones past index writeChunkSize, only reachable in the second chunk — is
// actually present.
func TestEmbeddedEngineSetupChunksTupleWrites(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	lm, err := loadModel(filepath.Join("testdata", "docs", "model.fga"))
	if err != nil {
		t.Fatal(err)
	}

	const n = writeChunkSize + 5
	want := make(map[string]bool, n)
	tuples := make([]*openfgav1.TupleKey, n)
	for i := 0; i < n; i++ {
		user := fmt.Sprintf("user:u%d", i)
		tuples[i] = &openfgav1.TupleKey{User: user, Relation: "owner", Object: "document:1"}
		want[user] = true
	}

	sc, err := setupScope(t, eng, lm.Proto, tuples)
	if err != nil {
		t.Fatal(err)
	}

	// A tuple in the first chunk and one only reachable past writeChunkSize.
	for _, idx := range []int{0, writeChunkSize + 1} {
		got, err := eng.Check(context.Background(), sc, CheckReq{User: fmt.Sprintf("user:u%d", idx), Relation: "owner", Object: "document:1"})
		if err != nil {
			t.Fatal(err)
		}
		if !got {
			t.Fatalf("user:u%d (chunk write) should resolve owner=true", idx)
		}
	}

	users, err := eng.ListUsers(context.Background(), sc, ListUsersReq{Object: "document:1", Relation: "owner", Filters: []ListUsersFilter{{Type: "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != n {
		t.Fatalf("want all %d chunked tuples written, ListUsers returned %d: %v", n, len(users), users)
	}
	for _, u := range users {
		if !want[u] {
			t.Errorf("unexpected user %q in ListUsers result", u)
		}
		delete(want, u)
	}
	if len(want) != 0 {
		t.Errorf("missing users after chunked write (dropped by a chunk boundary): %v", want)
	}
}
