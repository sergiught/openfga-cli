package workfile

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// withoutContestedLocal mirrors withoutContested in command/mapping/drift.go,
// which this package cannot import.
func withoutContestedLocal(stored []language.Tuple, contested []snapshot.Contested) []language.Tuple {
	set := map[string]bool{}
	for _, c := range contested {
		set[snapshot.Identity(c.Tuple)] = true
	}
	return slices.DeleteFunc(slices.Clone(stored), func(t language.Tuple) bool { return set[snapshot.Identity(t)] })
}

func randomTuple(rnd *rand.Rand) language.Tuple {
	var t language.Tuple
	switch rnd.IntN(3) {
	case 0:
		t = language.Tuple{User: fmt.Sprintf("user:u%d", rnd.IntN(12)), Relation: "member", Object: fmt.Sprintf("organization:o%d", rnd.IntN(4))}
	case 1:
		t = language.Tuple{User: fmt.Sprintf("user:u%d", rnd.IntN(12)), Relation: "admin", Object: fmt.Sprintf("organization:o%d", rnd.IntN(4))}
	default:
		t = language.Tuple{User: fmt.Sprintf("group:g%d#member", rnd.IntN(5)), Relation: "viewer", Object: fmt.Sprintf("doc:d%d", rnd.IntN(4))}
	}
	switch rnd.IntN(6) {
	case 1:
		t.Condition = "c"
	case 2:
		t.Condition, t.Context = "c", map[string]any{"n": float64(rnd.IntN(2)), "s": "a<b"}
	case 3:
		t.Condition, t.Context = "c", map[string]any{"deep": map[string]any{"x": []any{"1", float64(rnd.IntN(2))}}}
	case 4:
		t.Context = map[string]any{} // empty context is no context
	}
	return t
}

func randomSet(rnd *rand.Rand, n int) []language.Tuple {
	seen := map[string]bool{}
	var out []language.Tuple
	for range n {
		t := randomTuple(rnd)
		if !seen[snapshot.Identity(t)] {
			seen[snapshot.Identity(t)] = true
			out = append(out, t)
		}
	}
	return out
}

func readDiff(t *testing.T, f *File, kind DiffKind) (ts, was []language.Tuple) {
	t.Helper()
	require.NoError(t, f.DiffRows(context.Background(), kind, func(tp, w language.Tuple) error {
		ts = append(ts, tp)
		if kind == Changed {
			was = append(was, w)
		}
		return nil
	}))
	return ts, was
}

// norm makes an empty context and a nil one equal after a JSON round trip.
func norm(ts []language.Tuple) []language.Tuple {
	out := slices.Clone(ts)
	for i := range out {
		if len(out[i].Context) == 0 {
			out[i].Context = nil
		}
	}
	return out
}

func TestCompareParity(t *testing.T) {
	ctx := context.Background()
	for seed := range uint64(50) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rnd := rand.New(rand.NewPCG(seed, 7)) //nolint:gosec // seeded on purpose
			desired := randomSet(rnd, 5+rnd.IntN(40))
			stored := randomSet(rnd, 5+rnd.IntN(40))
			// Share some identities so Changed and unchanged both occur.
			for _, d := range desired {
				if rnd.IntN(2) == 0 {
					s := d
					if rnd.IntN(2) == 0 {
						s = randomTuple(rnd)
						s.User, s.Relation, s.Object = d.User, d.Relation, d.Object
					}
					stored = append(stored, s)
				}
			}
			var uniq []language.Tuple
			seen := map[string]bool{}
			for _, s := range stored {
				if !seen[snapshot.Identity(s)] {
					seen[snapshot.Identity(s)] = true
					uniq = append(uniq, s)
				}
			}
			stored = uniq
			var contested []snapshot.Contested
			for _, s := range stored {
				if rnd.IntN(8) == 0 {
					contested = append(contested, snapshot.Contested{Tuple: s})
				}
			}
			var owned []mapping.Shape
			if seed%2 == 1 {
				owned = []mapping.Shape{{UserType: "user", Relation: "member", ObjectType: "organization"}, {UserType: "group#member", Relation: "viewer", ObjectType: "doc"}}
			}

			f, _ := newFile(t)
			for _, d := range desired {
				require.NoError(t, f.AddDesired(ctx, d))
			}
			require.NoError(t, f.FlushDesired(ctx))
			require.NoError(t, f.AddStore(ctx, stored))
			// Run twice: the second run must clear the first one's rows.
			var c Counts
			for range 2 {
				var err error
				c, err = f.Compare(ctx, owned, contested)
				require.NoError(t, err)
			}

			want := snapshot.Compare(desired, withoutContestedLocal(stored, contested), owned)
			require.Equal(t, Counts{Desired: len(desired), Missing: len(want.Missing), Changed: len(want.Changed), Stale: len(want.Stale)}, c)
			missing, _ := readDiff(t, f, Missing)
			changed, was := readDiff(t, f, Changed)
			stale, _ := readDiff(t, f, Stale)
			require.Equal(t, norm(want.Missing), norm(missing))
			require.Equal(t, norm(want.Changed), norm(changed))
			require.Equal(t, norm(want.Was), norm(was))
			require.Equal(t, norm(want.Stale), norm(stale))
		})
	}
}

func TestStoreReadIsSaved(t *testing.T) {
	ctx := context.Background()
	f, path := newFile(t)
	_, _, _, ok := f.StoreRead()
	require.True(t, !ok)

	at := time.Date(2026, 11, 27, 10, 0, 0, 5, time.UTC)
	require.NoError(t, f.AddStore(ctx, []language.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}))
	require.NoError(t, f.MarkStoreRead(at, 42, 3))
	require.NoError(t, f.Close())

	f, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	got, total, objects, ok := f.StoreRead()
	require.True(t, ok)
	require.True(t, at.Equal(got))
	require.Equal(t, 42, total)
	require.Equal(t, 3, objects)

	require.NoError(t, f.ResetStore(ctx))
	_, _, _, ok = f.StoreRead()
	require.True(t, !ok)
	var n int
	require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM store`).Scan(&n))
	require.Equal(t, 0, n)
}

func TestWantedPairsAndKeys(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	for i := range 2500 {
		require.NoError(t, f.AddDesired(ctx, language.Tuple{User: fmt.Sprintf("user:u%d", i), Relation: "member", Object: "organization:big"}))
	}
	require.NoError(t, f.AddDesired(ctx, language.Tuple{User: "user:a", Relation: "admin", Object: "organization:small"}))
	require.NoError(t, f.FlushDesired(ctx))
	w := f.Wanted()
	pairs, err := w.Pairs(ctx)
	require.NoError(t, err)
	require.Equal(t, []snapshot.Pair{{Object: "organization:big", Relation: "member", N: 2500}, {Object: "organization:small", Relation: "admin", N: 1}}, pairs)
	n := 0
	require.NoError(t, w.Keys(ctx, "organization:big", "member", func(language.Tuple) error {
		n++
		// The connection is free to use while Keys walks.
		return f.AddStore(ctx, []language.Tuple{{User: "user:x", Relation: "member", Object: "organization:big"}})
	}))
	require.Equal(t, 2500, n)
}

// planOf returns the EXPLAIN QUERY PLAN detail lines of a query.
func planOf(t *testing.T, f *File, q string, args ...any) string {
	t.Helper()
	rows, err := f.db.Query("EXPLAIN QUERY PLAN "+q, args...)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
		out += detail + "\n"
	}
	return out
}

func TestWantedUsesThePairIndex(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	for i := range 6000 {
		require.NoError(t, f.AddDesired(ctx, language.Tuple{User: fmt.Sprintf("user:u%d", i), Relation: "member", Object: fmt.Sprintf("organization:o%d", i%300)}))
	}
	require.NoError(t, f.FlushDesired(ctx))
	keys := planOf(t, f, `SELECT id, tuple FROM desired WHERE object = ? AND relation = ? AND id > ? ORDER BY id LIMIT ?`, "o", "r", "", 10)
	t.Log(keys)
	require.True(t, strings.Contains(keys, "desired_pair") && !strings.Contains(keys, "SCAN"))
	pairs := planOf(t, f, `SELECT object, relation, COUNT(*) FROM desired GROUP BY 1, 2 ORDER BY 1, 2`)
	t.Log(pairs)
	require.True(t, strings.Contains(pairs, "desired_pair") && !strings.Contains(pairs, "TEMP B-TREE"))

	n := 0
	for i := range 300 {
		require.NoError(t, f.Wanted().Keys(ctx, fmt.Sprintf("organization:o%d", i), "member", func(language.Tuple) error { n++; return nil }))
	}
	require.Equal(t, 6000, n)
}

func TestResetReplayKeepsEventsAndTheStoreRead(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	require.NoError(t, f.AddStore(ctx, []language.Tuple{{User: "user:a", Relation: "member", Object: "organization:o"}}))
	require.NoError(t, f.MarkStoreRead(time.Now(), 1, 1))
	_, err := f.db.Exec(`INSERT INTO tupleset(id, shape, user, relation, object, tuple, writer, typ, marked) VALUES('x','s','u','r','o',x'00','w','t',0)`)
	require.NoError(t, err)
	require.NoError(t, f.ResetReplay(ctx))

	for table, want := range map[string]int{"tupleset": 0, "plan": 0, "desired": 0, "diff": 0, "store": 1} {
		var n int
		require.NoError(t, f.db.QueryRow(`SELECT COUNT(*) FROM `+table).Scan(&n))
		if n != want {
			t.Errorf("%s has %d rows, want %d", table, n, want)
		}
	}
	_, _, _, ok := f.StoreRead()
	require.True(t, ok)
}
