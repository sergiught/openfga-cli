package base

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/outputschema"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

func TestEveryMachineLeafHasOutputType(t *testing.T) {
	root := metaRoot(t).Command()
	types := OutputTypes()

	want := map[string]bool{}
	for _, c := range cmdmeta.Leaves(root) {
		meta, _ := cmdmeta.Get(c)
		if meta.InteractiveOnly {
			continue
		}
		want[c.CommandPath()] = true
	}
	var missing, unknown []string
	for p := range want {
		if _, ok := types[p]; !ok {
			missing = append(missing, p)
		}
	}
	for p := range types {
		if !want[p] {
			unknown = append(unknown, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(unknown)
	if len(missing) > 0 || len(unknown) > 0 {
		t.Fatalf("OutputTypes out of step with the command tree\nleaves without an output type:\n  %s\noutput types that name no machine leaf:\n  %s",
			strings.Join(missing, "\n  "), strings.Join(unknown, "\n  "))
	}
}

func TestCommandsLinksOutputSchema(t *testing.T) {
	c, _ := decodeCatalog(t)
	got := map[string]string{}
	for _, ci := range c.Commands {
		got[ci.Path] = ci.OutputSchema
	}
	if want := outputschema.URL("ofga stores list"); got["ofga stores list"] != want {
		t.Errorf("stores list output_schema = %q, want %q", got["ofga stores list"], want)
	}
	if v, ok := got["ofga playground"]; !ok || v != "" {
		t.Errorf("playground output_schema = %q (present %v), want none", v, ok)
	}
}

func TestEverySchemaFileHasALeaf(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "site", "public", "schemas", "output")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	types := OutputTypes()
	known := map[string]bool{outputschema.FileName(outputschema.EnvelopePath): true}
	for p := range types {
		known[outputschema.FileName(p)] = true
	}
	var orphans []string
	for _, e := range entries {
		if !known[e.Name()] {
			orphans = append(orphans, e.Name())
		}
	}
	if len(orphans) > 0 {
		t.Fatalf("schema files with no command: %v", orphans)
	}
	for p := range types {
		if !slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == outputschema.FileName(p) }) {
			t.Errorf("no schema file for %q (run `make schemas`)", p)
		}
	}
}

func TestSchemasValidateRealOutput(t *testing.T) {
	for _, tc := range []struct{ path, sub string }{
		{"ofga version", "version"},
		{"ofga commands", "commands"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			outputschematest.Validate(t, tc.path, []byte(runCatalog(t, tc.sub, "--json")))
		})
	}
}
