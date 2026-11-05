package optimize

import (
	"reflect"
	"testing"
)

func TestKeys_ContentIdentity(t *testing.T) {
	a := Finding{Type: "doc", Relation: "viewer", ID: "S3", Proof: "p1"}
	b := Finding{Type: "doc", Relation: "viewer", ID: "S3", Proof: "p2"}
	got := Keys([]Finding{a, b, a})
	want := []FindingKey{
		{Type: "doc", Relation: "viewer", ID: "S3", Proof: "p1"},
		{Type: "doc", Relation: "viewer", ID: "S3", Proof: "p2"},
		{Type: "doc", Relation: "viewer", ID: "S3", Proof: "p1", N: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if s := want[2].String(); s != "doc#viewer:S3:p1#1" {
		t.Fatalf("String = %q", s)
	}
	if s := want[0].String(); s != "doc#viewer:S3:p1" {
		t.Fatalf("String = %q", s)
	}
}
