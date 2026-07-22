package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func testRoot() *cobra.Command {
	root := &cobra.Command{Use: "ofga", Short: "root"}
	root.PersistentFlags().String("api-url", "", "OpenFGA API URL (overrides profile/env)")

	stores := &cobra.Command{Use: "stores", Short: "Manage stores"}
	create := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a store",
		Example: "  ofga stores create my-store --use",
		Run:     func(*cobra.Command, []string) {},
	}
	create.Flags().Bool("use", false, "set the new store as the active store")
	hidden := &cobra.Command{Use: "secret", Hidden: true, Run: func(*cobra.Command, []string) {}}
	stores.AddCommand(create, hidden)

	version := &cobra.Command{Use: "version", Short: "Print the version", Run: func(*cobra.Command, []string) {}}
	root.AddCommand(stores, version)
	return root
}

func find(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	cmd := root
	for _, name := range path {
		next := (*cobra.Command)(nil)
		for _, c := range cmd.Commands() {
			if c.Name() == name {
				next = c
			}
		}
		if next == nil {
			t.Fatalf("command %q not found under %q", name, cmd.Name())
		}
		cmd = next
	}
	return cmd
}

func TestSlug(t *testing.T) {
	root := testRoot()
	if got := slug(find(t, root, "stores", "create")); got != "stores-create" {
		t.Errorf("slug = %q, want stores-create", got)
	}
	if got := slug(find(t, root, "version")); got != "version" {
		t.Errorf("slug = %q, want version", got)
	}
}

func TestVisibleCommandsSkipsHiddenAndHelp(t *testing.T) {
	root := testRoot()
	root.InitDefaultHelpCmd()
	names := []string{}
	for _, c := range visibleCommands(root) {
		names = append(names, c.Name())
	}
	want := []string{"stores", "version"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("visibleCommands = %v, want %v", names, want)
	}
	sub := []string{}
	for _, c := range visibleCommands(find(t, root, "stores")) {
		sub = append(sub, c.Name())
	}
	if strings.Join(sub, ",") != "create" {
		t.Errorf("stores children = %v, want [create]", sub)
	}
}

func TestEscapeMDX(t *testing.T) {
	if got := escapeMDX("create <name> {arg}"); got != `create \<name> \{arg}` {
		t.Errorf("escapeMDX = %q", got)
	}
}

func TestRenderLeafPage(t *testing.T) {
	root := testRoot()
	create := find(t, root, "stores", "create")

	page := string(renderLeafPage(create, false, 4, 1))
	for _, want := range []string{
		`title: "ofga stores create"`,
		`description: "Create a store"`,
		"## Usage",
		"ofga stores create <name> [flags]",
		"## Examples",
		"ofga stores create my-store --use",
		"`--use`",
		"set the new store as the active store",
		"Global flags",
		"--api-url",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("leaf page missing %q\n---\n%s", want, page)
		}
	}
	if strings.Contains(page, "CommandDemo") {
		t.Error("leaf page embeds CommandDemo without a recording")
	}

	withRec := string(renderLeafPage(create, true, 4, 1))
	for _, want := range []string{
		`<CommandDemo slug="stores-create" />`,
		"import CommandDemo from '../../../../components/CommandDemo.astro';",
	} {
		if !strings.Contains(withRec, want) {
			t.Errorf("leaf page with recording missing %q", want)
		}
	}
}

func TestRenderGroupIndex(t *testing.T) {
	root := testRoot()
	page := string(renderGroupIndex(find(t, root, "stores"), 2))
	for _, want := range []string{
		`title: "ofga stores"`,
		"[create](create/)",
		"Create a store",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("group index missing %q\n---\n%s", want, page)
		}
	}
}

func TestRunGeneratesRealTree(t *testing.T) {
	out := filepath.Join(t.TempDir(), "reference")
	rec := t.TempDir()
	if err := os.WriteFile(filepath.Join(rec, "stores-create.webm"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(out, rec); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"index.mdx",
		"api.mdx",
		"stores/index.mdx",
		"stores/create.mdx",
		"completion/bash.mdx",
		"config/path.mdx",
	} {
		if _, err := os.Stat(filepath.Join(out, p)); err != nil {
			t.Errorf("expected generated file %s: %v", p, err)
		}
	}
	b, err := os.ReadFile(filepath.Join(out, "stores", "create.mdx"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `<CommandDemo slug="stores-create" />`) {
		t.Error("stores/create.mdx should embed its recording")
	}
}
