package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/openfga/mapper/language"
	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

// addSnapshotTests is the hand edit the example carries on top of the
// recipes: a test for each snapshot event the two writing rules answer, which
// `mappings backfill` and `mappings drift` replay. The recipes already name
// the snapshot types in their `when`.
func addSnapshotTests(d *mapping.Document) {
	const org, user, role = "org_acme", "user_anne", "rol_admin"
	snapshot := func(typ, subject, id string, object map[string]any) map[string]any {
		return map[string]any{
			"specversion": "1.0", "type": typ, "source": "urn:auth0:example.auth0app.com", "id": id,
			"time": "2025-02-01T12:34:56Z", "a0tenant": "my-tenant", "subject": subject,
			"data": map[string]any{
				"context": map[string]any{"tenant": map[string]any{"id": "my-tenant"}},
				"object":  object,
			},
		}
	}
	orgRef := map[string]any{"id": org, "name": "acme"}
	userRef := map[string]any{"user_id": user}
	d.Tests = append(d.Tests,
		mapping.TestCase{
			Name: "snapshot.organization.member",
			Input: snapshot("snapshot.organization.member", "organization "+org+" member "+user, "snap_000001",
				map[string]any{"organization": orgRef, "user": userRef}),
			ExpectTuples: []language.Tuple{
				{User: "user:" + user, Relation: "member", Object: "organization:" + org, Action: "write"},
				{User: "user:" + user, Relation: "holder", Object: "membership:" + org + "|" + user, Action: "write"},
			},
		},
		mapping.TestCase{
			Name: "snapshot.organization.member.role",
			Input: snapshot("snapshot.organization.member.role", "organization "+org+" member "+user+" role "+role, "snap_000002",
				map[string]any{"organization": orgRef, "user": userRef, "role": map[string]any{"id": role, "name": "admin"}}),
			ExpectTuples: []language.Tuple{
				{User: "membership:" + org + "|" + user + "#holder", Relation: "assignee", Object: "role:" + org + "|" + role, Action: "write"},
			},
		},
	)
}

// TestExampleMappingIsWhatTheRecipesProduce rebuilds examples/mappings/auth0.yaml
// the way the workspace does: accept each organization recipe, then save. A
// recipe change that makes the guide's example stale fails here.
func TestExampleMappingIsWhatTheRecipesProduce(t *testing.T) {
	s := testSession()
	for _, typ := range []string{
		"organization.member.added",
		"organization.member.deleted",
		"organization.member.role.assigned",
		"organization.member.role.deleted",
	} {
		e, ok := auth0.Lookup(typ)
		if !ok {
			t.Fatalf("no catalog event %s", typ)
		}
		newRecipeView(s, e).accept()
	}
	d := *s.Doc
	d.Tests = mapping.SavedTests(context.Background(), s.Doc)
	addSnapshotTests(&d)
	got, err := mapping.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../../../examples/mappings/auth0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("examples/mappings/auth0.yaml no longer matches what the four recipes save; regenerate the example from the workspace.\n--- got ---\n%s", got)
	}
}

// TestFullExampleMappingIsWhatTheRecipesProduce rebuilds
// examples/mappings-full/auth0.yaml: accept the recipe of each event a snapshot
// type mirrors, save, then add one test per snapshot type whose input is that
// type's sample and whose tuples are the ones the live event's test expects.
func TestFullExampleMappingIsWhatTheRecipesProduce(t *testing.T) {
	pairs := [][2]string{
		{"connection.created", "snapshot.connection"},
		{"user.created", "snapshot.user"},
		{"organization.created", "snapshot.organization"},
		{"organization.connection.added", "snapshot.organization.connection"},
		{"organization.member.added", "snapshot.organization.member"},
		{"organization.member.role.assigned", "snapshot.organization.member.role"},
	}
	s := testSession()
	for _, p := range pairs {
		e, ok := auth0.Lookup(p[0])
		if !ok {
			t.Fatalf("no catalog event %s", p[0])
		}
		newRecipeView(s, e).accept()
	}
	d := *s.Doc
	d.Tests = mapping.SavedTests(context.Background(), s.Doc)
	live := map[string]mapping.TestCase{}
	for _, tc := range d.Tests {
		live[tc.Name] = tc
	}
	for _, p := range pairs {
		data, err := os.ReadFile("../../../mapping/auth0/samples/" + p[1] + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var input map[string]any
		if err := json.Unmarshal(data, &input); err != nil {
			t.Fatalf("%s sample: %v", p[1], err)
		}
		d.Tests = append(d.Tests, mapping.TestCase{Name: p[1], Input: input, ExpectTuples: live[p[0]].ExpectTuples})
	}
	got, err := mapping.Marshal(&d)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../../../examples/mappings-full/auth0.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("examples/mappings-full/auth0.yaml no longer matches what the six recipes save.\n--- got ---\n%s", got)
	}
}
