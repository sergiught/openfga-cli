package fga

import (
	"testing"

	"github.com/sergiught/go-openfga/openfga"
)

// The tuple_key is attached only when something is set: /read treats an
// all-empty tuple_key differently from an absent one.
func TestReadFilterTupleKey(t *testing.T) {
	if got := (ReadFilter{}).TupleKey(); got != nil {
		t.Fatalf("no filter should mean no tuple_key, got %+v", got)
	}
	got := ReadFilter{User: "user:anne", Object: "document:"}.TupleKey()
	if got == nil {
		t.Fatal("an active filter should set tuple_key")
	}
	want := openfga.ReadRequestTupleKey{User: "user:anne", Object: "document:"}
	if *got != want {
		t.Fatalf("tuple_key = %+v, want %+v", *got, want)
	}
}

// ValidateReadObject allows type:id AND bare-type "type:" (unlike ValidateObjectRef),
// stays lenient on empty, and rejects colon-less or userset-shaped values.
func TestValidateReadObject(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"", true},
		{"   ", true}, // whitespace-only reads as unset, like every other field
		{"document:roadmap", true},
		{"document:", true},
		{"  document:  ", true},
		{"document", false},
		{"document:1#viewer", false},
		{":roadmap", false},
		// /read matches object ids literally, so a wildcard would silently
		// return nothing rather than "every document".
		{"document:*", false},
	} {
		err := ValidateReadObject(tc.in)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateReadObject(%q) = %v, want ok=%v", tc.in, err, tc.ok)
		}
	}
}

// ReadFilter.Validate mirrors the server's /read rule: object type required,
// and object id and user not both empty. All-empty is valid (it clears).
func TestReadFilterValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    ReadFilter
		ok   bool
	}{
		{"empty clears", ReadFilter{}, true},
		{"object type:id", ReadFilter{Object: "document:roadmap"}, true},
		{"bare type + user", ReadFilter{Object: "document:", User: "user:anne"}, true},
		{"type:id + relation", ReadFilter{Object: "document:roadmap", Relation: "viewer"}, true},
		{"bare type alone", ReadFilter{Object: "document:"}, false},
		// The server's rule ignores the relation, so it cannot stand in for the
		// object id or the user.
		{"bare type + relation only", ReadFilter{Object: "document:", Relation: "viewer"}, false},
		{"padded leading colon with user", ReadFilter{Object: " :roadmap", User: "user:anne"}, false},
		{"padded user with bare type", ReadFilter{Object: "document:", User: " user:anne "}, true},
		{"whitespace user with bare type", ReadFilter{Object: "document:", User: "   "}, false},
		{"user alone", ReadFilter{User: "user:anne"}, false},
		{"relation alone", ReadFilter{Relation: "viewer"}, false},
		{"user + relation, no object", ReadFilter{User: "user:anne", Relation: "viewer"}, false},
		// The server splits the object on its first colon, so a colon-less or
		// leading-colon object has no type and is rejected even with a user set.
		{"colon-less object with user", ReadFilter{Object: "document", User: "user:anne"}, false},
		{"leading colon with user", ReadFilter{Object: ":roadmap", User: "user:anne"}, false},
	} {
		err := tc.f.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Validate(%+v) = %v, want ok=%v", tc.name, tc.f, err, tc.ok)
		}
	}
}
