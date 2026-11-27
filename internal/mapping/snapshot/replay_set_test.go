package snapshot

import (
	"context"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"

	"github.com/sergiught/openfga-cli/internal/mapping"
)

func TestReplayWithMapSetMatchesReplay(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(contestMapping))
	if err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		cev("snapshot.organization.member", "u2", "org", "o2"),
		cev("snapshot.organization.member", "u1"),
		cev("snapshot.grant", "u1", "role", "r1"),
		cev("snapshot.swap", "u1", "org", "o3"),
		cev("snapshot.organization.member", "u3", "drop", true),
	}
	a := NewReplay(m)
	b := NewReplayWith(m, NewMapSet(), mapping.NewMapState())
	for _, r := range []*Replay{a, b} {
		if err := r.Add(context.Background(), events); err != nil {
			t.Fatal(err)
		}
	}
	ad, ac := a.Done()
	bd, bc := b.Done()
	if !reflect.DeepEqual(ad, bd) || !reflect.DeepEqual(ac, bc) {
		t.Fatalf("differs: %v %v vs %v %v", ad, ac, bd, bc)
	}
}

func TestDoneEachOrder(t *testing.T) {
	m, _, err := mapping.CompileSource([]byte(contestMapping))
	if err != nil {
		t.Fatal(err)
	}
	r := NewReplay(m)
	events := []map[string]any{
		cev("snapshot.grant", "u2", "role", "r2"),
		cev("snapshot.organization.member", "u3", "org", "o2"),
		cev("snapshot.grant", "u1", "role", "r1"),
		cev("snapshot.organization.member", "u1", "org", "o9"),
		cev("snapshot.organization.member", "u1", "org", "o1"),
	}
	if err := r.Add(context.Background(), events); err != nil {
		t.Fatal(err)
	}
	var got []language.Tuple
	if _, err := r.DoneEach(context.Background(), func(t language.Tuple) error {
		got = append(got, t)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want, _ := r.Done()
	if len(got) != 5 || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	sorted := append([]language.Tuple(nil), got...)
	sortTuples(sorted)
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("not sorted: %v", got)
	}
}
