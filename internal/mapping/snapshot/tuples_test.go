package snapshot

import (
	"errors"
	"reflect"
	"testing"

	"github.com/openfga/mapper/language"
)

func TestCountShapesMatchesByShape(t *testing.T) {
	ts := []language.Tuple{
		{User: "user:a", Relation: "member", Object: "organization:x"},
		{User: "user:b", Relation: "admin", Object: "organization:x"},
		{User: "user:c", Relation: "member", Object: "organization:y"},
	}
	got, err := CountShapes(Slice(ts))
	if err != nil || !reflect.DeepEqual(got, ByShape(ts)) {
		t.Fatalf("CountShapes = %v, %v; want %v", got, err, ByShape(ts))
	}
}

func TestSliceStopsOnError(t *testing.T) {
	stop := errors.New("stop")
	n := 0
	err := Slice(make([]language.Tuple, 3)).Each(func(language.Tuple) error {
		n++
		return stop
	})
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("got %v after %d calls", err, n)
	}
}

func TestDiffListsKeepChangedAndWasPaired(t *testing.T) {
	d := Compare(
		[]language.Tuple{{User: "user:a", Relation: "member", Object: "organization:x", Condition: "c"}},
		[]language.Tuple{{User: "user:a", Relation: "member", Object: "organization:x"}},
		nil).Lists()
	if d.Changed.Len() != 1 || d.Was.Len() != 1 || d.Missing.Len() != 0 || d.Stale.Len() != 0 {
		t.Fatalf("lists = %+v", d)
	}
}
