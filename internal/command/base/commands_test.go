package base

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/output"
)

// runCatalog executes the root command in-process and returns stdout. The
// package-level output/cli flag state is reset afterwards.
func runCatalog(t *testing.T, args ...string) string {
	t.Helper()
	root := metaRoot(t).Command()
	t.Cleanup(func() {
		output.Plain, output.JQ = false, ""
	})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("ofga %v: %v", args, err)
	}
	return out.String()
}

func decodeCatalog(t *testing.T) (catalog, string) {
	t.Helper()
	raw := runCatalog(t, "commands", "--json")
	var c catalog
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("decode catalog: %v\n%s", err, raw)
	}
	return c, raw
}

func TestCommandsJSONListsEveryLeaf(t *testing.T) {
	c, _ := decodeCatalog(t)
	root := metaRoot(t).Command()
	if want := len(cmdmeta.Leaves(root)); len(c.Commands) != want {
		t.Fatalf("catalog has %d commands, want %d", len(c.Commands), want)
	}
	var found *commandInfo
	for i := range c.Commands {
		if c.Commands[i].Path == "ofga tuples write" {
			found = &c.Commands[i]
		}
	}
	if found == nil {
		t.Fatal("ofga tuples write missing")
	}
	if !found.Mutates || !found.RequiresStore {
		t.Errorf("tuples write meta wrong: %+v", found)
	}
	hasFile, hasDry := false, false
	for _, f := range found.Flags {
		if f.Name == "file" && f.Type == "string" {
			hasFile = true
		}
		if f.Name == "dry-run" {
			hasDry = true
		}
	}
	if !hasFile {
		t.Errorf("tuples write flags lack string --file: %+v", found.Flags)
	}
	if found.SupportsDryRun != hasDry {
		t.Errorf("SupportsDryRun=%v but dry-run flag present=%v", found.SupportsDryRun, hasDry)
	}
}

func TestCommandsGlobalFlagsOnce(t *testing.T) {
	c, _ := decodeCatalog(t)
	var out *flagInfo
	for i := range c.GlobalFlags {
		if c.GlobalFlags[i].Name == "output" {
			out = &c.GlobalFlags[i]
		}
	}
	if out == nil || len(out.Enum) == 0 {
		t.Fatalf("global output flag missing or without enum: %+v", out)
	}
	for _, cmd := range c.Commands {
		for _, f := range cmd.Flags {
			if f.Name == "output" {
				t.Errorf("%s lists inherited --output", cmd.Path)
			}
		}
	}
}

func TestCommandsExcludesInternals(t *testing.T) {
	c, _ := decodeCatalog(t)
	for _, cmd := range c.Commands {
		for _, bad := range []string{"__complete", " help", "completion"} {
			if strings.Contains(cmd.Path, bad) {
				t.Errorf("catalog includes internal command %q", cmd.Path)
			}
		}
	}
}

func TestCommandsJSONHasNoNulls(t *testing.T) {
	_, raw := decodeCatalog(t)
	if strings.Contains(raw, "null") {
		t.Fatalf("catalog JSON contains null")
	}
}

func TestCommandsTable(t *testing.T) {
	out := runCatalog(t, "commands")
	if !strings.Contains(out, "ofga tuples write") || !strings.Contains(out, "COMMAND") {
		t.Fatalf("table output unexpected:\n%s", out)
	}
}
