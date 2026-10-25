package tui

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

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
