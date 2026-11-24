package mapping

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

func readMatching(t *testing.T, fs *fakeStore, want []language.Tuple) (storeRead, error) {
	t.Helper()
	cl, _, err := fs.cli().ClientWithStore()
	if err != nil {
		t.Fatal(err)
	}
	return storeClient{cl}.ReadMatching(context.Background(), want, nil)
}

func wantMember(users ...string) []language.Tuple {
	var out []language.Tuple
	for _, u := range users {
		out = append(out, language.Tuple{User: u, Relation: "member", Object: "tenant:acme"})
	}
	return out
}

func tenantTuple(user string) map[string]any {
	return map[string]any{"user": user, "relation": "member", "object": "tenant:acme"}
}

// keyReads returns the reads that name a user, relation and object.
func keyReads(rs []readReq) (out []readReq) {
	for _, r := range rs {
		if r.TupleKey["user"] != "" {
			out = append(out, r)
		}
	}
	return out
}

func TestReadMatchingCrowdedPairFallsBackToKeyReads(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t,
		tenantTuple("user:u1"), tenantTuple("user:n1"), tenantTuple("user:n2"), tenantTuple("user:u2"),
		map[string]any{"user": "user:u1", "relation": "reader", "object": "doc:d1"})
	fs.bigStore, fs.pageSize = true, 1
	want := append(wantMember("user:u1", "user:u2", "user:u3"),
		language.Tuple{User: "user:u1", Relation: "reader", Object: "doc:d1"})
	got, err := readMatching(t, fs, want)
	if err != nil {
		t.Fatal(err)
	}
	reads := fs.readRequests()
	pairPages, keys := 0, keyReads(reads)
	for _, r := range reads {
		if len(r.TupleKey) == 2 && r.TupleKey["object"] == "tenant:acme" {
			pairPages++
		}
		if r.TupleKey["user"] != "" && r.TupleKey["object"] == "doc:d1" {
			t.Errorf("the pair that ends within its budget got a key read: %+v", r)
		}
	}
	if pairPages != 1 || len(keys) != 3 {
		t.Fatalf("pair pages = %d, key reads = %d, want 1 and 3: %+v", pairPages, len(keys), reads)
	}
	var ids []string
	for _, tu := range got.Tuples {
		ids = append(ids, snapshot.Identity(tu))
	}
	slices.Sort(ids)
	wantIDs := []string{
		snapshot.Identity(language.Tuple{User: "user:u1", Relation: "member", Object: "tenant:acme"}),
		snapshot.Identity(language.Tuple{User: "user:u1", Relation: "reader", Object: "doc:d1"}),
		snapshot.Identity(language.Tuple{User: "user:u2", Relation: "member", Object: "tenant:acme"}),
	}
	slices.Sort(wantIDs)
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("tuples = %v, want %v (none twice)", ids, wantIDs)
	}
	// 1 crowded page + 2 key hits + 1 doc pair read.
	if got.Read != 4 {
		t.Errorf("Read = %d, want 4", got.Read)
	}
}

func TestReadMatchingPairBudgetFollowsItsTupleCount(t *testing.T) {
	configtest.Isolate(t)
	var stored []map[string]any
	var users []string
	for i := range 20 {
		stored = append(stored, tenantTuple(fmt.Sprintf("user:s%d", i)))
	}
	for i := range 17 {
		users = append(users, fmt.Sprintf("user:s%d", i))
	}
	fs := newFakeStore(t, stored...)
	fs.bigStore, fs.pageSize = true, 1
	got, err := readMatching(t, fs, wantMember(users...))
	if err != nil {
		t.Fatal(err)
	}
	reads := fs.readRequests()
	pairPages := 0
	for _, r := range reads {
		if len(r.TupleKey) == 2 {
			pairPages++
		}
	}
	if pairPages != 3 || len(keyReads(reads)) != 17 || len(got.Tuples) != 17 {
		t.Fatalf("pair pages = %d, key reads = %d, tuples = %d, want 3, 17 and 17", pairPages, len(keyReads(reads)), len(got.Tuples))
	}
}

func TestReadMatchingKeyReadsKeepUserStringsUnchanged(t *testing.T) {
	configtest.Isolate(t)
	users := []string{"user:*", "membership:org_acme|auth0|anne#holder", "user:auth0|anne"}
	fs := newFakeStore(t, tenantTuple(users[1]), tenantTuple("user:n1"), tenantTuple("user:n2"))
	fs.bigStore, fs.pageSize = true, 1
	got, err := readMatching(t, fs, wantMember(users...))
	if err != nil {
		t.Fatal(err)
	}
	var sent []string
	for _, r := range keyReads(fs.readRequests()) {
		if r.TupleKey["relation"] != "member" || r.TupleKey["object"] != "tenant:acme" {
			t.Errorf("key read = %+v", r)
		}
		sent = append(sent, r.TupleKey["user"])
	}
	slices.Sort(sent)
	want := slices.Clone(users)
	slices.Sort(want)
	if !slices.Equal(sent, want) {
		t.Errorf("users sent = %q, want %q", sent, want)
	}
	if len(got.Tuples) != 1 || got.Tuples[0].User != users[1] {
		t.Errorf("tuples = %+v", got.Tuples)
	}
}

func TestReadMatchingKeyReadFailureFailsTheRead(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, tenantTuple("user:u1"), tenantTuple("user:n1"))
	fs.bigStore, fs.pageSize, fs.failReadUser = true, 1, "user:u2"
	if _, err := readMatching(t, fs, wantMember("user:u1", "user:u2")); err == nil {
		t.Fatal("want an error")
	}
}

func TestBackfillCrowdedPairFailedKeyReadWritesNothing(t *testing.T) {
	configtest.Isolate(t)
	fs := newFakeStore(t, storedTuple("u1", "member"), storedTuple("zz", "member"))
	fs.bigStore, fs.pageSize, fs.failReadUser = true, 1, "user:u2"
	useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
	mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
	_, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--force")
	if err == nil || fs.writeCount() != 0 {
		t.Fatalf("err = %v writes = %d\n%s", err, fs.writeCount(), stderr)
	}
}

func TestBackfillCrowdedPairClassifiesLikeAFullRead(t *testing.T) {
	changed := storedTuple("u2", "reader")
	changed["condition"] = map[string]any{"name": "c"}
	stored := []map[string]any{storedTuple("u1", "member"), storedTuple("zz", "member"), storedTuple("yy", "member"), changed}
	summary := func(fs *fakeStore) string {
		configtest.Isolate(t)
		useFakeProvider(t, fakeProvider{types: fakeTypes(), result: fakeResult()})
		mp := writeFile(t, t.TempDir(), "m.yaml", memberSnapshotMapping)
		stdout, stderr, err := runBackfill(t, fs.cli(), nil, mp, "--provider", "fake", "--dry-run")
		if err != nil {
			t.Fatalf("err = %v\n%s", err, stderr)
		}
		for line := range strings.SplitSeq(stdout, "\n") {
			if strings.Contains(line, "tuples from Fake:") {
				return line
			}
		}
		t.Fatalf("no summary line:\n%s", stdout)
		return ""
	}
	whole := newFakeStore(t, stored...)
	crowded := newFakeStore(t, stored...)
	crowded.bigStore, crowded.pageSize = true, 1
	a, b := summary(whole), summary(crowded)
	if a != b || !strings.Contains(a, "2 missing") {
		t.Errorf("whole = %q\ncrowded = %q", a, b)
	}
	if len(keyReads(crowded.readRequests())) == 0 {
		t.Error("the crowded run made no key reads")
	}
}
