package auth0

import (
	"context"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
	snapauth0 "github.com/sergiught/openfga-cli/internal/mapping/snapshot/auth0"
)

// Evaluating a mirrored event and its snapshot twin, when both carry the same
// ids, must give the same tuples: that is what lets drift and backfill reuse
// the recipe the user already built.
func TestSnapshotSampleMatchesMirror(t *testing.T) {
	for _, typ := range snapauth0.Types() {
		t.Run(typ.Name, func(t *testing.T) {
			recipe := recipeFor(typ.Mirrors)
			doc := &mapping.Document{Rules: []mapping.Rule{recipe.Rule}}

			mirror, ok := Lookup(typ.Mirrors)
			if !ok {
				t.Fatalf("no catalog entry for %s", typ.Mirrors)
			}

			snap, err := loadSample(typ.Name)
			if err != nil {
				t.Fatal(err)
			}

			want := mapping.Evaluate(context.Background(), doc, mirror.Sample)
			got := mapping.Evaluate(context.Background(), doc, snap)

			for name, p := range map[string]mapping.Preview{"mirror": want, "snapshot": got} {
				if !p.OK() {
					t.Fatalf("%s sample: diagnostics %v, err %v", name, p.Diagnostics, p.EvalErr)
				}
			}

			if recipe.Maps() && want.TupleCount() == 0 {
				t.Fatalf("%s writes nothing for its own sample", typ.Mirrors)
			}

			if !reflect.DeepEqual(want.Tuples, got.Tuples) || !reflect.DeepEqual(want.Filters, got.Filters) {
				t.Errorf("snapshot sample maps to\n%v %v\nwant what %s maps to\n%v %v",
					got.Tuples, got.Filters, typ.Mirrors, want.Tuples, want.Filters)
			}
		})
	}
}

// A recipe that writes tuples answers its snapshot type, and only its
// when changes; one that maps nothing stays out of drift and backfill.
func TestMirroredRecipesNameSnapshot(t *testing.T) {
	types := snapauth0.Types()

	for _, typ := range types {
		t.Run(typ.Name, func(t *testing.T) {
			recipe := recipeFor(typ.Mirrors)

			if recipe.Rule.Name != typ.Mirrors && recipe.Maps() {
				t.Errorf("Rule.Name = %q, want %q", recipe.Rule.Name, typ.Mirrors)
			}

			if got := snapshot.IsSnapshotRule(recipe.Rule, types); got != recipe.Maps() {
				t.Errorf("IsSnapshotRule = %v for a recipe where Maps() = %v (when %q)", got, recipe.Maps(), recipe.Rule.When)
			}

			want := `input.type == "` + typ.Mirrors + `"`
			if recipe.Maps() {
				want += ` || input.type == "` + typ.Name + `"`
			}

			if recipe.Rule.When != want {
				t.Errorf("When = %q, want %q", recipe.Rule.When, want)
			}
		})
	}
}

// No other recipe may name a snapshot type: only the six creation events
// mirror one.
func TestOnlyMirroredRecipesNameSnapshot(t *testing.T) {
	types := snapauth0.Types()
	mirrored := map[string]bool{}

	for _, typ := range types {
		mirrored[typ.Mirrors] = true
	}

	for _, e := range Catalog() {
		if !mirrored[e.Type] && snapshot.IsSnapshotRule(e.Recipe.Rule, types) {
			t.Errorf("%s names a snapshot type: %q", e.Type, e.Recipe.Rule.When)
		}
	}
}

// The event picker offers real Auth0 events, never the snapshot twins.
func TestSnapshotSamplesAreNotCatalogEvents(t *testing.T) {
	for _, e := range Catalog() {
		if strings.HasPrefix(e.Type, "snapshot.") {
			t.Errorf("catalog lists %s", e.Type)
		}
	}

	for _, typ := range snapauth0.Types() {
		if slices.ContainsFunc(Catalog(), func(e Event) bool { return e.Type == typ.Name }) {
			t.Errorf("catalog lists %s", typ.Name)
		}

		if _, err := loadSample(typ.Name); err != nil {
			t.Errorf("snapshot sample missing: %v", err)
		}
	}
}

// The table recipes.go keeps must equal what the provider declares, both ways.
func TestSnapshotMirrorsMatchProviderTypes(t *testing.T) {
	want := map[string]string{}
	for _, typ := range snapauth0.Types() {
		want[typ.Mirrors] = typ.Name
	}

	if !reflect.DeepEqual(snapshotMirrors, want) {
		t.Errorf("snapshotMirrors = %v, want %v", snapshotMirrors, want)
	}
}

// The recipes are data: they must not import the provider or the Auth0 SDK.
func TestRecipesPackageDoesNotImportProvider(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}

		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}

		for _, imp := range parsed.Imports {
			if path := strings.Trim(imp.Path.Value, `"`); strings.Contains(path, "snapshot/auth0") || strings.Contains(path, "go-auth0") {
				t.Errorf("%s imports %s", f, path)
			}
		}
	}
}
