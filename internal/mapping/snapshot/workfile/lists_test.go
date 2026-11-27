package workfile

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

func collect(t *testing.T, ts snapshot.Tuples) []language.Tuple {
	t.Helper()
	var out []language.Tuple
	require.NoError(t, ts.Each(func(x language.Tuple) error {
		out = append(out, x)
		return nil
	}))
	require.Equal(t, len(out), ts.Len())
	return out
}

func TestListsReadInOrder(t *testing.T) {
	ctx := context.Background()
	for seed := range uint64(10) {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rnd := rand.New(rand.NewPCG(seed, 7)) //nolint:gosec // seeded on purpose
			desired := randomSet(rnd, 5+rnd.IntN(40))
			stored := randomSet(rnd, 5+rnd.IntN(40))
			for _, d := range desired {
				if rnd.IntN(2) == 0 {
					s := randomTuple(rnd)
					s.User, s.Relation, s.Object = d.User, d.Relation, d.Object
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
			owned := []mapping.Shape{{UserType: "user", Relation: "member", ObjectType: "organization"}, {UserType: "group#member", Relation: "viewer", ObjectType: "doc"}}

			f, _ := newFile(t)
			for _, d := range desired {
				require.NoError(t, f.AddDesired(ctx, d))
			}
			require.NoError(t, f.FlushDesired(ctx))
			require.NoError(t, f.AddStore(ctx, stored))
			_, err := f.Compare(ctx, owned, nil)
			require.NoError(t, err)

			want := snapshot.Compare(desired, stored, owned)
			sorted := append([]language.Tuple(nil), desired...)
			slices.SortStableFunc(sorted, func(a, b language.Tuple) int {
				return cmp.Or(strings.Compare(snapshot.ShapeKey(a), snapshot.ShapeKey(b)), strings.Compare(a.User, b.User),
					strings.Compare(a.Relation, b.Relation), strings.Compare(a.Object, b.Object))
			})
			got := f.Diff()
			require.Equal(t, norm(sorted), norm(collect(t, f.Desired())))
			require.Equal(t, norm(want.Missing), norm(collect(t, got.Missing)))
			require.Equal(t, norm(want.Changed), norm(collect(t, got.Changed)))
			require.Equal(t, norm(want.Was), norm(collect(t, got.Was)))
			require.Equal(t, norm(want.Stale), norm(collect(t, got.Stale)))
		})
	}
}

func TestDesiredReadsMoreThanOnePage(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	var want []language.Tuple
	for i := range 2*pageRows + 7 {
		tup := language.Tuple{User: fmt.Sprintf("user:u%05d", i), Relation: "member", Object: "organization:acme"}
		want = append(want, tup)
		require.NoError(t, f.AddDesired(ctx, tup))
	}
	require.NoError(t, f.FlushDesired(ctx))
	require.Equal(t, norm(want), norm(collect(t, f.Desired())))
}

func TestListEachReportsReadErrors(t *testing.T) {
	f, _ := newFile(t)
	require.NoError(t, f.db.Close())
	l := f.Desired()
	require.Equal(t, 0, l.Len())
	require.Error(t, l.Each(func(language.Tuple) error { return nil }))
}

func TestListEachNamesTheFileInDatabaseErrorsOnly(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	for _, u := range []string{"a", "b"} {
		require.NoError(t, f.AddDesired(ctx, language.Tuple{User: "user:" + u, Relation: "member", Object: "organization:acme"}))
	}
	require.NoError(t, f.FlushDesired(ctx))
	stop := errors.New("stop")
	err := f.Desired().Each(func(language.Tuple) error { return stop })
	require.True(t, err == stop)
	require.NoError(t, f.db.Close())
	err = f.Desired().Each(func(language.Tuple) error { return nil })
	require.Error(t, err)
	require.Contains(t, err.Error(), f.Path())
	require.Contains(t, f.Named(sql.ErrConnDone).Error(), f.Path())
	require.True(t, f.Named(stop) == stop)
}

func TestPagesSkipTheSentRows(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	var want []language.Tuple
	for i := range 25 {
		tup := language.Tuple{User: fmt.Sprintf("user:u%02d", i), Relation: "member", Object: "organization:acme"}
		want = append(want, tup)
		require.NoError(t, f.AddDesired(ctx, tup))
	}
	_, err := f.Compare(ctx, nil, nil)
	require.NoError(t, err)

	var sizes []int
	var got []language.Tuple
	require.NoError(t, f.Pages(Missing, 10, func(page, was []language.Tuple) error {
		sizes = append(sizes, len(page))
		require.Equal(t, 0, len(was))
		got = append(got, page...)
		if len(sizes) == 1 {
			return f.MarkSent(Missing, page) // the first page landed, the second did not
		}
		return nil
	}))
	require.Equal(t, []int{10, 10, 5}, sizes)
	require.Equal(t, norm(want), norm(got))
	n, err := f.SentCount(Missing)
	require.NoError(t, err)
	require.Equal(t, 10, n)

	got = nil
	require.NoError(t, f.Pages(Missing, 10, func(page, _ []language.Tuple) error {
		got = append(got, page...)
		return nil
	}))
	require.Equal(t, norm(want[10:]), norm(got))
}

func TestPagesPairChangedWithTheirStoredVersion(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	d := language.Tuple{User: "user:u1", Relation: "member", Object: "organization:acme", Condition: "new"}
	s := language.Tuple{User: "user:u1", Relation: "member", Object: "organization:acme", Condition: "old"}
	require.NoError(t, f.AddDesired(ctx, d))
	require.NoError(t, f.AddStore(ctx, []language.Tuple{s}))
	_, err := f.Compare(ctx, nil, nil)
	require.NoError(t, err)
	require.NoError(t, f.Pages(Changed, 10, func(page, was []language.Tuple) error {
		require.Equal(t, norm([]language.Tuple{d}), norm(page))
		require.Equal(t, norm([]language.Tuple{s}), norm(was))
		return nil
	}))
}

func TestResetSentOffersEveryRowAndCarriesTheCounts(t *testing.T) {
	ctx := context.Background()
	f, _ := newFile(t)
	for i := range 5 {
		require.NoError(t, f.AddDesired(ctx, language.Tuple{User: fmt.Sprintf("user:u%d", i), Relation: "member", Object: "organization:acme"}))
	}
	_, err := f.Compare(ctx, nil, nil)
	require.NoError(t, err)
	require.NoError(t, f.Pages(Missing, 2, func(page, _ []language.Tuple) error { return f.MarkSent(Missing, page) }))

	require.NoError(t, f.ResetSent())
	n, err := f.SentCount(Missing)
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.Equal(t, 5, f.Carried(Missing))
	var rows int
	require.NoError(t, f.Pages(Missing, 10, func(page, _ []language.Tuple) error { rows += len(page); return nil }))
	require.Equal(t, 5, rows)

	// A second reset adds to what was carried.
	require.NoError(t, f.Pages(Missing, 10, func(page, _ []language.Tuple) error { return f.MarkSent(Missing, page[:2]) }))
	require.NoError(t, f.ResetSent())
	require.Equal(t, 7, f.Carried(Missing))
}
