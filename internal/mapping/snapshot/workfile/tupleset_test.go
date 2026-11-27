package workfile

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const parityMapping = `version: "1"
rules:
  - name: blocked
    when: input.type == "snapshot.user" && input.blocked == true
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: member
    when: input.type == "snapshot.organization.member" && input.drop != true
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: drop
    when: input.type == "snapshot.organization.member" && input.drop == true
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "organization:"
        action: delete
  - name: removed
    when: input.type == "snapshot.removed"
    action: delete
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: grant
    when: input.type == "snapshot.grant"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: assignee
        object: "role:{{ input.role }}"
  - name: grant-conditional
    when: input.type == "snapshot.grant"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: viewer
        object: "doc:{{ input.role }}"
        condition: in_office
        context:
          role: "{{ input.role }}"
          level: "gold"
  - name: revoke
    when: input.type == "snapshot.revoke"
    tuple_filters:
      - user: "user:{{ input.user_id }}"
        object: "role:"
        action: delete
  - name: pin
    when: input.type == "snapshot.pin"
    tuple_filters:
      - object: "organization:{{ input.org }}"
        action: delete
  - name: tick
    when: input.type == "snapshot.tick"
    tuple_filters:
      - relation: assignee
        object: "role:"
        action: delete
  - name: home
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: owner
        object: "home:{{ input.user_id }}"
`

func pev(typ string, rnd *rand.Rand) map[string]any {
	return map[string]any{
		"type": typ, "user_id": fmt.Sprintf("u%d", rnd.Intn(8)), "org": fmt.Sprintf("o%d", rnd.Intn(4)),
		"role": fmt.Sprintf("r%d", rnd.Intn(3)), "blocked": rnd.Intn(2) == 0, "drop": rnd.Intn(2) == 0,
	}
}

func readDesired(t *testing.T, f *File) []language.Tuple {
	t.Helper()
	rows, err := f.db.Query(`SELECT tuple FROM desired ORDER BY shape, user, relation, object`)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	var out []language.Tuple
	for rows.Next() {
		var blob []byte
		require.NoError(t, rows.Scan(&blob))
		tp, err := decodePlanned(blob, 0)
		require.NoError(t, err)
		out = append(out, tp.Tuple)
	}
	return out
}

func TestWorkfileReplayParity(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(parityMapping))
	require.NoError(t, err)
	types := []string{"snapshot.organization.member", "snapshot.grant", "snapshot.removed", "snapshot.user",
		"snapshot.revoke", "snapshot.pin", "snapshot.tick"}
	for seed := int64(0); seed < 30; seed++ {
		for _, windowed := range []bool{false, true} {
			t.Run(fmt.Sprint(seed, windowed), func(t *testing.T) {
				rnd := rand.New(rand.NewSource(seed)) //nolint:gosec // deterministic test data
				var events []map[string]any
				for i := 0; i < 60; i++ {
					events = append(events, pev(types[rnd.Intn(len(types))], rnd))
				}
				mem := snapshot.NewReplay(m)
				f, _ := newFile(t)
				wf := snapshot.NewReplayWith(m, f.TupleSet(), f.PlanState())
				if windowed {
					doc, err := mapping.LoadBytes("m.yaml", []byte(parityMapping))
					require.NoError(t, err)
					w := []string{"snapshot.user"}
					for _, r := range []*snapshot.Replay{mem, wf} {
						r.Window(w, snapshot.UnreadRemovals(doc.Doc, w))
					}
				}
				ctx := context.Background()
				for _, r := range []*snapshot.Replay{mem, wf} {
					require.NoError(t, r.Add(ctx, events))
				}
				wantD, wantC := mem.Done()
				gotC, err := wf.DoneEach(ctx, func(tp language.Tuple) error { return f.AddDesired(ctx, tp) })
				require.NoError(t, err)
				require.NoError(t, f.FlushDesired(ctx))
				n, err := f.DesiredCount()
				require.NoError(t, err)
				require.Equal(t, len(wantD), n)
				if len(wantD) > 0 {
					require.True(t, reflect.DeepEqual(wantD, readDesired(t, f)))
				}
				require.True(t, reflect.DeepEqual(wantC, gotC))
			})
		}
	}
}

func TestDesiredFlushesInBatches(t *testing.T) {
	f, _ := newFile(t)
	ctx := context.Background()
	for i := 0; i < desiredBatch+5; i++ {
		require.NoError(t, f.AddDesired(ctx, language.Tuple{User: "user:u", Relation: "r", Object: fmt.Sprintf("o:%d", i)}))
	}
	n, err := f.DesiredCount()
	require.NoError(t, err)
	require.Equal(t, desiredBatch, n)
	require.Equal(t, 5, len(f.desired))
	require.NoError(t, f.FlushDesired(ctx))
	n, _ = f.DesiredCount()
	require.Equal(t, desiredBatch+5, n)
}

func TestTupleSetSortedPagesAndAllowsWrites(t *testing.T) {
	f, _ := newFile(t)
	ctx := context.Background()
	s := f.TupleSet()
	total := sortedPage*2 + 7
	for i := 0; i < total; i++ {
		tp := language.Tuple{User: "user:u", Relation: "r", Object: fmt.Sprintf("o:%05d", (i*7919)%total)}
		require.NoError(t, s.Put(ctx, snapshot.Identity(tp), snapshot.Stored{Tuple: tp}))
	}
	var prev string
	count := 0
	require.NoError(t, s.Sorted(ctx, func(st snapshot.Stored) error {
		require.True(t, st.Tuple.Object > prev)
		prev = st.Tuple.Object
		count++
		return f.AddDesired(ctx, st.Tuple)
	}))
	require.Equal(t, total, count)
}
