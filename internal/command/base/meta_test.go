package base

import (
	"io"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/cmdmeta"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
)

func metaRoot(t *testing.T) *Command {
	t.Helper()
	configtest.Isolate(t)
	return New(cli.New(log.New(io.Discard), config.New(), "test"))
}

func TestEveryLeafHasMeta(t *testing.T) {
	root := metaRoot(t).Command()
	leaves := cmdmeta.Leaves(root)
	if len(leaves) == 0 {
		t.Fatal("no leaves found")
	}
	var missing []string
	for _, c := range leaves {
		if _, ok := cmdmeta.Get(c); !ok {
			missing = append(missing, c.CommandPath())
		}
	}
	if len(missing) > 0 {
		t.Fatalf("leaf commands without ofga.* metadata:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestMetaSpotChecks(t *testing.T) {
	root := metaRoot(t).Command()
	tests := []struct {
		path []string
		want cmdmeta.Meta
	}{
		{[]string{"tuples", "write"}, cmdmeta.Meta{RequiresStore: true, Mutates: true}},
		{[]string{"query", "check"}, cmdmeta.Meta{RequiresStore: true}},
		{[]string{"stores", "list"}, cmdmeta.Meta{}},
		{[]string{"playground"}, cmdmeta.Meta{Mutates: true, InteractiveOnly: true}},
		{[]string{"version"}, cmdmeta.Meta{}},
	}
	for _, tc := range tests {
		cmd, _, err := root.Find(tc.path)
		if err != nil || cmd == root {
			t.Fatalf("find %v: %v", tc.path, err)
		}
		got, ok := cmdmeta.Get(cmd)
		if !ok {
			t.Errorf("%v: no metadata", tc.path)
			continue
		}
		if got.RequiresStore != tc.want.RequiresStore || got.Mutates != tc.want.Mutates ||
			got.InteractiveOnly != tc.want.InteractiveOnly {
			t.Errorf("%v: got %+v, want %+v", tc.path, got, tc.want)
		}
	}
}

func TestEnumFlagsAnnotated(t *testing.T) {
	root := metaRoot(t).Command()
	if got := cmdmeta.Enum(root.PersistentFlags().Lookup("output")); strings.Join(got, " ") != "json jsonl yaml plain table" {
		t.Errorf("-o enum = %v", got)
	}
	for _, tc := range []struct {
		path []string
		flag string
		want string
	}{
		{[]string{"tuples", "write"}, "on-duplicate", "error ignore"},
		{[]string{"tuples", "delete"}, "on-missing", "error ignore"},
		{[]string{"tuples", "write"}, "file-format", "json jsonl yaml csv"},
		{[]string{"query", "check"}, "consistency", "HIGHER_CONSISTENCY MINIMIZE_LATENCY UNSPECIFIED"},
		{[]string{"model", "test"}, "report", "junit json github"},
	} {
		cmd, _, err := root.Find(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(cmdmeta.Enum(cmd.Flags().Lookup(tc.flag)), " "); got != tc.want {
			t.Errorf("%v --%s enum = %q, want %q", tc.path, tc.flag, got, tc.want)
		}
	}
}

func TestLeavesAreNotGroups(t *testing.T) {
	got := map[string]bool{}
	for _, c := range cmdmeta.Leaves(metaRoot(t).Command()) {
		got[c.CommandPath()] = true
	}
	for _, p := range []string{"ofga stores", "ofga tuples", "ofga model", "ofga profiles", "ofga config"} {
		if got[p] {
			t.Errorf("group %q listed as a leaf", p)
		}
	}
	for _, p := range []string{"ofga", "ofga mappings", "ofga model test", "ofga stores list"} {
		if !got[p] {
			t.Errorf("%q missing from leaves", p)
		}
	}
}
