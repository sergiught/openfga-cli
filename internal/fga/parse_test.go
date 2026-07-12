package fga

import "testing"

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
