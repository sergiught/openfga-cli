package workfile

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

const (
	tConn   = "snapshot.connection"
	tUser   = "snapshot.user"
	tOrg    = "snapshot.organization"
	tOrgCon = "snapshot.organization.connection"
	tMember = "snapshot.organization.member"
	tRole   = "snapshot.organization.member.role"
)

func eventsMeta() Meta {
	m := testMeta()
	m.Types = []string{tConn, tUser, tOrg, tOrgCon, tMember, tRole}
	return m
}

func newFile(t *testing.T) (*File, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "w.db")
	f, err := Create(path, eventsMeta())
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f, path
}

func ev(typ, tag string) map[string]any {
	return map[string]any{"type": typ, "id": "orig-" + tag, "tag": tag}
}

func replayAll(t *testing.T, f *File, page int) (all []map[string]any, calls []int) {
	t.Helper()
	n, err := f.Replay(context.Background(), page, func(evs []map[string]any) error {
		calls = append(calls, len(evs))
		all = append(all, evs...)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, len(all), n)
	return all, calls
}

func TestReplayOrder(t *testing.T) {
	f, _ := newFile(t)
	save := func(key string, order int, evs ...map[string]any) {
		t.Helper()
		require.NoError(t, f.Save(snapshot.Unit{Key: key, Order: order}, evs, true))
	}
	save("org-members/org_b", 1, ev(tRole, "b-r1"), ev(tMember, "b-m1"), ev(tMember, "b-m2"))
	save("org-members/org_acme", 0, ev(tRole, "a-r1"), ev(tMember, "a-m1"))
	save("users", 0, ev(tUser, "u1"), ev(tUser, "u2"))
	save("connections", 0, ev(tConn, "c1"))

	all, _ := replayAll(t, f, 100)
	var got []string
	for _, e := range all {
		got = append(got, e["tag"].(string))
	}
	want := []string{"c1", "u1", "u2", "a-m1", "b-m1", "b-m2", "a-r1", "b-r1"}
	require.Equal(t, fmt.Sprint(want), fmt.Sprint(got))
}

func TestReplayRenumbersIDs(t *testing.T) {
	f, _ := newFile(t)
	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "a"), ev(tUser, "b")}, true))
	require.NoError(t, f.Save(snapshot.Unit{Key: "connections"}, []map[string]any{ev(tConn, "c")}, true))
	all, _ := replayAll(t, f, 2)
	for i, e := range all {
		require.Equal(t, fmt.Sprintf("snap_%06d", i+1), e["id"])
	}
	require.Equal(t, "c", all[0]["tag"])
}

func TestReplayPages(t *testing.T) {
	f, _ := newFile(t)
	evs := make([]map[string]any, 2500)
	for i := range evs {
		evs[i] = ev(tUser, fmt.Sprint(i))
	}
	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, evs, true))
	all, calls := replayAll(t, f, 1000)
	require.Equal(t, fmt.Sprint([]int{1000, 1000, 500}), fmt.Sprint(calls))
	require.Equal(t, "2499", all[2499]["tag"])
}

func TestReplayStopsOnError(t *testing.T) {
	f, _ := newFile(t)
	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "a")}, true))
	boom := fmt.Errorf("boom")
	_, err := f.Replay(context.Background(), 10, func([]map[string]any) error { return boom })
	require.ErrorIs(t, err, boom)
}

func TestSaveUnknownType(t *testing.T) {
	f, _ := newFile(t)
	err := f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "a"), ev("snapshot.nope", "b")}, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "snapshot.nope")
	require.True(t, !f.UnitDone("users"))
	all, _ := replayAll(t, f, 10)
	require.Equal(t, 0, len(all))
}

func TestUnfinishedUnitsAreDropped(t *testing.T) {
	f, path := newFile(t)
	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "u1")}, false))
	require.True(t, !f.UnitDone("users"))

	require.NoError(t, f.Save(snapshot.Unit{Key: "connections"}, []map[string]any{ev(tConn, "c1")}, false))
	require.NoError(t, f.Save(snapshot.Unit{Key: "connections"}, []map[string]any{ev(tConn, "c2")}, true))
	require.True(t, f.UnitDone("connections"))

	require.NoError(t, f.DropUnfinished())
	all, _ := replayAll(t, f, 10)
	require.Equal(t, 2, len(all))
	require.Equal(t, "c1", all[0]["tag"])
	require.Equal(t, "c2", all[1]["tag"])

	// A resumed unit continues its item numbers after Close and Open.
	require.NoError(t, f.Close())
	g, err := Open(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	require.True(t, g.UnitDone("connections"))
	require.NoError(t, g.Save(snapshot.Unit{Key: "connections"}, []map[string]any{ev(tConn, "c3")}, true))
	all, _ = replayAll(t, g, 10)
	require.Equal(t, "c3", all[2]["tag"])
}

func TestRememberRecall(t *testing.T) {
	f, path := newFile(t)
	require.Equal(t, "", f.Recall("users-export-job"))
	require.NoError(t, f.Remember("users-export-job", "job_1"))
	require.NoError(t, f.Remember("users-export-job", "job_2"))
	require.NoError(t, f.Remember("users-export-job", "job_1"))
	require.NoError(t, f.Close())
	g, err := Open(path)
	require.NoError(t, err)
	defer func() { _ = g.Close() }()
	require.Equal(t, "job_1", g.Recall("users-export-job"))
}

func TestWindow(t *testing.T) {
	f, _ := newFile(t)
	s, e := f.Window()
	require.True(t, s.IsZero() && e.IsZero())

	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	tick := 0
	old := now
	now = func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Minute) }
	defer func() { now = old }()

	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "a")}, false))      // 1: started
	require.NoError(t, f.Save(snapshot.Unit{Key: "connections"}, []map[string]any{ev(tConn, "c")}, true)) // 2: started and finished
	require.NoError(t, f.Save(snapshot.Unit{Key: "users"}, []map[string]any{ev(tUser, "b")}, true))       // 3: finished

	s, e = f.Window()
	require.True(t, s.Equal(base.Add(1*time.Minute)))
	require.True(t, e.Equal(base.Add(3*time.Minute)))
}
