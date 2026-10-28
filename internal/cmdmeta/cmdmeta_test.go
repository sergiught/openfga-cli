package cmdmeta_test

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cmdmeta"
)

func TestSetGetRoundTrip(t *testing.T) {
	for _, want := range []cmdmeta.Meta{
		{},
		{RequiresStore: true, Mutates: true},
		{RequiresModel: true, InteractiveOnly: true},
		{RequiresStore: true, RequiresModel: true, Mutates: true, InteractiveOnly: true},
	} {
		cmd := cmdmeta.Set(&cobra.Command{Use: "x"}, want)
		got, ok := cmdmeta.Get(cmd)
		if !ok || got != want {
			t.Errorf("Get = %+v, %v; want %+v, true", got, ok, want)
		}
	}
}

func TestGetMissingKey(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	if _, ok := cmdmeta.Get(cmd); ok {
		t.Fatal("Get on an unannotated command reported ok")
	}
	cmdmeta.Set(cmd, cmdmeta.Meta{})
	delete(cmd.Annotations, "ofga.mutates")
	if _, ok := cmdmeta.Get(cmd); ok {
		t.Fatal("Get with one key missing reported ok")
	}
}

func TestEnumRoundTrip(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("mode", "", "")
	cmd.Flags().String("other", "", "")
	cmdmeta.SetEnum(cmd, "mode", "a", "b")
	if got := cmdmeta.Enum(cmd.Flags().Lookup("mode")); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("Enum = %v", got)
	}
	if got := cmdmeta.Enum(cmd.Flags().Lookup("other")); got != nil {
		t.Errorf("Enum on plain flag = %v, want nil", got)
	}
}

func TestLeavesExcludesHiddenHelpCompletion(t *testing.T) {
	run := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "root"}
	grp := &cobra.Command{Use: "grp"}
	grp.AddCommand(&cobra.Command{Use: "leaf", RunE: run})
	grp.AddCommand(&cobra.Command{Use: "hidden", RunE: run, Hidden: true})
	comp := &cobra.Command{Use: "completion"}
	comp.AddCommand(&cobra.Command{Use: "bash", RunE: run})
	root.AddCommand(grp, comp, &cobra.Command{Use: "help", RunE: run}, &cobra.Command{Use: "top", RunE: run})

	var got []string
	for _, c := range cmdmeta.Leaves(root) {
		got = append(got, c.CommandPath())
	}
	want := []string{"root grp leaf", "root top"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Leaves = %v, want %v", got, want)
	}
}

func TestLeavesExcludesGroupsKeepsInteractiveParents(t *testing.T) {
	run := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "root", RunE: run}
	cmdmeta.Set(root, cmdmeta.Meta{InteractiveOnly: true, Mutates: true})
	group := &cobra.Command{Use: "grp", RunE: run} // GroupRunE-like: no metadata
	group.AddCommand(&cobra.Command{Use: "leaf", RunE: run})
	parent := &cobra.Command{Use: "work", RunE: run}
	cmdmeta.Set(parent, cmdmeta.Meta{Mutates: true})
	parent.AddCommand(&cobra.Command{Use: "sub", RunE: run})
	root.AddCommand(group, parent)

	var got []string
	for _, c := range cmdmeta.Leaves(root) {
		got = append(got, c.CommandPath())
	}
	want := []string{"root", "root grp leaf", "root work", "root work sub"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Leaves = %v, want %v", got, want)
	}
}

// An empty value list would put a flag in the catalog with no allowed values,
// so SetEnum refuses it at registration, as it does an unknown flag.
func TestSetEnumRejectsEmptyList(t *testing.T) {
	cmd := &cobra.Command{Use: "x"}
	cmd.Flags().String("mode", "", "")
	defer func() {
		if recover() == nil {
			t.Error("SetEnum with no values did not panic")
		}
	}()
	cmdmeta.SetEnum(cmd, "mode")
}
