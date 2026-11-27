package workfile

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const benchMapping = `version: "1"
rules:
  - name: member
    when: input.type == "snapshot.organization.member"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: member
        object: "organization:{{ input.org }}"
  - name: home
    when: input.type == "snapshot.user"
    tuples:
      - user: "user:{{ input.user_id }}"
        relation: owner
        object: "home:{{ input.user_id }}"
`

// BenchmarkReplayWorkFile saves 20k events, replays them through the work
// file the way a mappings run does, builds the diff against an empty store
// read and reports the size of the file a failed run would keep.
func BenchmarkReplayWorkFile(b *testing.B) {
	m, _, err := mapping.CompileSource([]byte(benchMapping))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	for range b.N {
		path := filepath.Join(b.TempDir(), "w.db")
		f, err := Create(path, eventsMeta())
		if err != nil {
			b.Fatal(err)
		}
		var evs []map[string]any
		for i := 0; i < 10_000; i++ {
			evs = append(evs,
				map[string]any{"type": tUser, "id": fmt.Sprint("u", i), "user_id": fmt.Sprintf("auth0|user%06d", i)},
				map[string]any{"type": tMember, "id": fmt.Sprint("m", i), "user_id": fmt.Sprintf("auth0|user%06d", i), "org": fmt.Sprintf("org_%03d", i%50)})
		}
		for len(evs) > 0 {
			n := min(len(evs), 1000)
			if err := f.Save(snapshot.Unit{Key: "all"}, evs[:n], false); err != nil {
				b.Fatal(err)
			}
			evs = evs[n:]
		}
		start := time.Now()
		r := snapshot.NewReplayWith(m, f.TupleSet(), f.PlanState())
		if _, err := f.Replay(ctx, 1000, func(e []map[string]any) error { return r.Add(ctx, e) }); err != nil {
			b.Fatal(err)
		}
		if _, err := r.DoneEach(ctx, func(t language.Tuple) error { return f.AddDesired(ctx, t) }); err != nil {
			b.Fatal(err)
		}
		if _, err := f.Compare(ctx, nil, nil); err != nil {
			b.Fatal(err)
		}
		elapsed := time.Since(start)
		if err := f.Close(); err != nil {
			b.Fatal(err)
		}
		var size int64
		for _, s := range append([]string{""}, sidecars...) {
			if st, err := os.Stat(path + s); err == nil {
				size += st.Size()
			}
		}
		b.ReportMetric(elapsed.Seconds(), "s/replay")
		b.ReportMetric(float64(size)/(1<<20), "MiB/file")
	}
}
