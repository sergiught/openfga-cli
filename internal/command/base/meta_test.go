package base

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/spf13/cobra"

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

// isGroupRunE reports whether cmd's RunE is cli.GroupRunE, the shared RunE of
// a pure group that only prints help. Method values of one method share a code
// pointer whatever receiver they are bound to.
func isGroupRunE(cmd *cobra.Command) bool {
	if cmd.RunE == nil {
		return false
	}
	return reflect.ValueOf(cmd.RunE).Pointer() == reflect.ValueOf((*cli.CLI)(nil).GroupRunE).Pointer()
}

// unannotatedParents lists runnable, non-hidden commands that have available
// subcommands but no catalog metadata and are not pure groups. cmdmeta.Leaves
// drops such a parent silently, so it would vanish from `ofga commands`.
func unannotatedParents(root *cobra.Command) []string {
	var out []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Hidden {
			return
		}
		if c != root && (c.Name() == "help" || c.Name() == "completion") {
			return
		}
		if _, annotated := cmdmeta.Get(c); c.Runnable() && c.HasAvailableSubCommands() && !annotated && !isGroupRunE(c) {
			out = append(out, c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	return out
}

func TestNoRunnableParentMissingMeta(t *testing.T) {
	if got := unannotatedParents(metaRoot(t).Command()); len(got) > 0 {
		t.Errorf("runnable parents with subcommands but no cmdmeta.Set, so ofga commands omits them: %v\n"+
			"annotate them with cmdmeta.Set, or give a pure help group cli.GroupRunE", got)
	}
}

func TestUnannotatedParentsCatchesPlantedOffender(t *testing.T) {
	var c *cli.CLI
	noop := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "ofga"}
	group := &cobra.Command{Use: "group", RunE: c.GroupRunE}
	group.AddCommand(cmdmeta.Set(&cobra.Command{Use: "leaf", RunE: noop}, cmdmeta.Meta{}))
	offender := &cobra.Command{Use: "offender", RunE: noop}
	offender.AddCommand(cmdmeta.Set(&cobra.Command{Use: "leaf", RunE: noop}, cmdmeta.Meta{}))
	annotated := cmdmeta.Set(&cobra.Command{Use: "annotated", RunE: noop}, cmdmeta.Meta{})
	annotated.AddCommand(cmdmeta.Set(&cobra.Command{Use: "leaf", RunE: noop}, cmdmeta.Meta{}))
	root.AddCommand(group, offender, annotated)

	got := unannotatedParents(root)
	if len(got) != 1 || got[0] != "ofga offender" {
		t.Errorf("unannotatedParents = %v, want [ofga offender]", got)
	}
}
