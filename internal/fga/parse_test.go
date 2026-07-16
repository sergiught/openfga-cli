package fga

import "testing"

func TestTriple(t *testing.T) {
	tests := []struct {
		name                       string
		args                       []string
		uFlag, rFlag, oFlag        string
		wantUser, wantRel, wantObj string
		wantErr                    bool
	}{
		{name: "three positionals", args: []string{"user:anne", "viewer", "document:roadmap"},
			wantUser: "user:anne", wantRel: "viewer", wantObj: "document:roadmap"},
		{name: "all flags, no positionals", uFlag: "user:anne", rFlag: "viewer", oFlag: "document:roadmap",
			wantUser: "user:anne", wantRel: "viewer", wantObj: "document:roadmap"},
		// The point of CLI-3: --user set, remaining positionals fill relation+object
		// left to right instead of shifting by index.
		{name: "user flag then two positionals", args: []string{"viewer", "document:roadmap"}, uFlag: "user:anne",
			wantUser: "user:anne", wantRel: "viewer", wantObj: "document:roadmap"},
		{name: "object flag then two positionals", args: []string{"user:anne", "viewer"}, oFlag: "document:roadmap",
			wantUser: "user:anne", wantRel: "viewer", wantObj: "document:roadmap"},
		{name: "missing part", args: []string{"user:anne", "viewer"}, wantErr: true},
		{name: "too many: three positionals plus a flag", args: []string{"user:anne", "viewer", "document:roadmap"}, uFlag: "user:bob", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, r, o, err := Triple(tt.args, tt.uFlag, tt.rFlag, tt.oFlag)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Triple err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if u != tt.wantUser || r != tt.wantRel || o != tt.wantObj {
				t.Errorf("Triple = (%q,%q,%q), want (%q,%q,%q)", u, r, o, tt.wantUser, tt.wantRel, tt.wantObj)
			}
		})
	}
}

func TestValidateUserRef(t *testing.T) {
	for _, ok := range []string{"user:anne", "team:eng#member", "user:*"} {
		if err := ValidateUserRef(ok); err != nil {
			t.Errorf("ValidateUserRef(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{"", "anne", "document"} {
		if ValidateUserRef(bad) == nil {
			t.Errorf("ValidateUserRef(%q) = nil, want error", bad)
		}
	}
}

func TestValidateObjectRef(t *testing.T) {
	if err := ValidateObjectRef("document:roadmap"); err != nil {
		t.Errorf("ValidateObjectRef(document:roadmap) = %v, want nil", err)
	}
	for _, bad := range []string{"", "document", "document:*", "document:1#viewer"} {
		if ValidateObjectRef(bad) == nil {
			t.Errorf("ValidateObjectRef(%q) = nil, want error", bad)
		}
	}
}

func TestParseTuple(t *testing.T) {
	tests := []struct {
		name                   string
		user, relation, object string
		wantErr                bool
	}{
		{name: "valid", user: "user:anne", relation: "viewer", object: "document:roadmap"},
		{name: "valid userset", user: "team:eng#member", relation: "viewer", object: "document:roadmap"},
		{name: "valid public wildcard user", user: "user:*", relation: "viewer", object: "document:roadmap"},
		{name: "empty user", user: "", relation: "viewer", object: "document:roadmap", wantErr: true},
		{name: "user missing type (swapped args)", user: "anne", relation: "viewer", object: "document:roadmap", wantErr: true},
		{name: "object missing type", user: "user:anne", relation: "viewer", object: "roadmap", wantErr: true},
		{name: "object missing id", user: "user:anne", relation: "viewer", object: "document:", wantErr: true},
		{name: "object wildcard rejected", user: "user:anne", relation: "viewer", object: "document:*", wantErr: true},
		{name: "object userset rejected", user: "user:anne", relation: "viewer", object: "team:eng#member", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseTuple(tt.user, tt.relation, tt.object)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTuple(%q,%q,%q) err = %v, wantErr %v", tt.user, tt.relation, tt.object, err, tt.wantErr)
			}
		})
	}
}
