package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
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

// mdLinkPattern matches Markdown link targets: "](target)". It does not
// match the CommandDemo import line (a JS import, not a "](...)" link).
var mdLinkPattern = regexp.MustCompile(`\]\(([^)]+)\)`)

// bareSegmentLinkPattern matches a single bare path segment used by the
// group/root index tables, e.g. "create/" or "stores/".
var bareSegmentLinkPattern = regexp.MustCompile(`^[\w.-]+/$`)

// isRelativeInternalLink reports whether target is one of the relative,
// same-site link forms docgen emits (leaf "## Related" siblings use
// "../sibling/", index tables use bare "child/"), as opposed to an absolute
// http(s) URL or a root-absolute path.
func isRelativeInternalLink(target string) bool {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "/") {
		return false
	}
	return strings.HasPrefix(target, "./") || strings.HasPrefix(target, "../") || bareSegmentLinkPattern.MatchString(target)
}

// linkBaseDir returns the directory relative links in relPath (e.g.
// "query/check.mdx", relative to the generated reference root) resolve
// against, following Starlight's routing: an index page routes to its own
// directory, a leaf page routes to a same-named pseudo-directory one level
// below (its URL has a trailing slash).
func linkBaseDir(relPath string) string {
	slash := filepath.ToSlash(relPath)
	dir := path.Dir(slash)
	name := strings.TrimSuffix(path.Base(slash), ".mdx")
	if name == "index" {
		return dir
	}
	return path.Join(dir, name)
}

// TestGeneratedLinksResolve guards against reference-page link drift: every
// relative internal link the generator emits (leaf "## Related" siblings,
// group index tables, the root index table) must resolve to a page that
// docgen actually generates. starlight-links-validator does not check this
// (relative links are either banned outright or ignored entirely), so this
// is the only thing gating it.
func TestGeneratedLinksResolve(t *testing.T) {
	out := filepath.Join(t.TempDir(), "reference")
	rec := t.TempDir()
	if err := run(out, rec); err != nil {
		t.Fatal(err)
	}

	err := filepath.Walk(out, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(p, ".mdx") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(out, p)
		if err != nil {
			return err
		}
		baseDir := linkBaseDir(relPath)

		for _, m := range mdLinkPattern.FindAllStringSubmatch(string(b), -1) {
			target := m[1]
			if !isRelativeInternalLink(target) {
				continue
			}
			resolved := path.Join(baseDir, target)
			leaf := filepath.Join(out, filepath.FromSlash(resolved)+".mdx")
			group := filepath.Join(out, filepath.FromSlash(resolved), "index.mdx")
			if _, err := os.Stat(leaf); err == nil {
				continue
			}
			if _, err := os.Stat(group); err == nil {
				continue
			}
			t.Errorf("%s: link %q resolves to %q, but neither %s nor %s exists", relPath, target, resolved, leaf, group)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
