package auth0_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping/auth0"
)

func TestCatalogCoversAllTwentyOneEventTypes(t *testing.T) {
	cat := auth0.Catalog()
	if len(cat) != 21 {
		t.Fatalf("catalog has %d events, want 21", len(cat))
	}
	seen := map[string]bool{}
	for _, e := range cat {
		if seen[e.Type] {
			t.Fatalf("duplicate event type %q", e.Type)
		}
		seen[e.Type] = true
	}
}

func TestCatalogEntriesAreComplete(t *testing.T) {
	groups := map[string]bool{"User": true, "Organization": true, "Group": true, "Connection": true}
	for _, e := range auth0.Catalog() {
		if e.Type == "" || e.Summary == "" {
			t.Errorf("incomplete entry: %+v", e)
		}
		if !groups[e.Group] {
			t.Errorf("%s: unknown group %q", e.Type, e.Group)
		}
		if len(e.Sample) == 0 {
			t.Errorf("%s: empty sample", e.Type)
			continue
		}
		if got := e.Sample["type"]; got != e.Type {
			t.Errorf("%s: sample type = %v", e.Type, got)
		}
		data, ok := e.Sample["data"].(map[string]any)
		if !ok {
			t.Errorf("%s: sample has no data object", e.Type)
			continue
		}
		if _, ok := data["object"].(map[string]any); !ok {
			t.Errorf("%s: sample has no data.object", e.Type)
		}
	}
}

func TestCatalogIsGroupedThenAlphabetical(t *testing.T) {
	// The picker renders the catalog in order, so the order is the UI.
	want := []string{"User", "Organization", "Group", "Connection"}
	var order []string
	for _, e := range auth0.Catalog() {
		if len(order) == 0 || order[len(order)-1] != e.Group {
			order = append(order, e.Group)
		}
	}
	if len(order) != len(want) {
		t.Fatalf("group order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("group order = %v, want %v", order, want)
		}
	}
	var prev string
	var prevGroup string
	for _, e := range auth0.Catalog() {
		if e.Group != prevGroup {
			prevGroup, prev = e.Group, ""
		}
		if prev != "" && e.Type < prev {
			t.Fatalf("%s out of order after %s", e.Type, prev)
		}
		prev = e.Type
	}
}

func TestLookup(t *testing.T) {
	e, ok := auth0.Lookup("organization.member.added")
	if !ok {
		t.Fatal("organization.member.added not found")
	}
	if e.Group != "Organization" {
		t.Fatalf("group = %q", e.Group)
	}
	obj := e.Sample["data"].(map[string]any)["object"].(map[string]any)
	if _, ok := obj["organization"]; !ok {
		t.Fatalf("sample object = %v", obj)
	}
	if _, ok := auth0.Lookup("nope"); ok {
		t.Fatal("unknown type resolved")
	}
}

func TestSamplesOnDiskMatchTheCatalog(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("samples"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("unexpected file in samples/: %s", e.Name())
		}
		files = append(files, strings.TrimSuffix(e.Name(), ".json"))
	}
	var types []string
	for _, e := range auth0.Catalog() {
		types = append(types, e.Type)
	}
	sort.Strings(files)
	sort.Strings(types)
	if strings.Join(files, ",") != strings.Join(types, ",") {
		t.Fatalf("samples/ and the catalog disagree:\nfiles: %v\ntypes: %v", files, types)
	}
}

func TestSamplesAreValidJSONWithTheCloudEventsEnvelope(t *testing.T) {
	for _, e := range auth0.Catalog() {
		raw, err := os.ReadFile(filepath.Join("samples", e.Type+".json"))
		if err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
		var ev map[string]any
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("%s: %v", e.Type, err)
		}
		for _, k := range []string{"specversion", "type", "source", "id", "time", "data"} {
			if _, ok := ev[k]; !ok {
				t.Errorf("%s: sample is missing %q", e.Type, k)
			}
		}
	}
}

// TestSampleIsACopy guards the embedded samples: the wizard mutates nothing, but
// a caller that did would otherwise corrupt every later Lookup of the same type.
func TestSampleIsACopy(t *testing.T) {
	a, _ := auth0.Lookup("user.created")
	a.Sample["type"] = "mutated"
	b, _ := auth0.Lookup("user.created")
	if b.Sample["type"] != "user.created" {
		t.Fatalf("mutation leaked: %v", b.Sample["type"])
	}
}
