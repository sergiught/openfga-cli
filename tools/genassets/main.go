// Command genassets generates the shell completions and man pages that ship
// inside the release archives and OS packages. It builds the same cobra tree
// the binary exposes, so the assets can never describe a command set the
// release does not have.
//
// goreleaser runs this from a before-hook; the output directory is disposable
// and gitignored. Run it by hand with:
//
//	go run ./tools/genassets -out ./dist/assets
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"charm.land/log/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/command/base"
	"github.com/sergiught/openfga-cli/internal/config"
)

func main() {
	out := flag.String("out", "dist/assets", "output directory for completions/ and man/")
	version := flag.String("version", "dev", "version string stamped into the man page footer")
	flag.Parse()
	if err := run(*out, *version); err != nil {
		fmt.Fprintln(os.Stderr, "genassets:", err)
		os.Exit(1)
	}
}

func run(out, version string) error {
	root := buildTree()

	compDir := filepath.Join(out, "completions")
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		return err
	}
	completions := []struct {
		file string
		gen  func(io.Writer) error
	}{
		{"ofga.bash", func(w io.Writer) error { return root.GenBashCompletionV2(w, true) }},
		{"ofga.zsh", root.GenZshCompletion},
		{"ofga.fish", func(w io.Writer) error { return root.GenFishCompletion(w, true) }},
	}
	for _, c := range completions {
		if err := writeFile(filepath.Join(compDir, c.file), c.gen); err != nil {
			return err
		}
	}

	manDir := filepath.Join(out, "man")
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}
	// A fixed date keeps the man pages reproducible: goreleaser builds the same
	// tag on different days and the footer should not be what makes the
	// archives differ.
	hdr := &doc.GenManHeader{
		Title:   "OFGA",
		Section: "1",
		Source:  "ofga " + version,
		Manual:  "ofga Manual",
		Date:    &fixedDate,
	}
	return doc.GenManTree(root, hdr, manDir)
}

var fixedDate = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// buildTree constructs the full ofga command tree without loading any user
// config. InitDefaultCompletionCmd is normally called during Execute, so call
// it here so `ofga completion` is documented like every other command.
func buildTree() *cobra.Command {
	c := cli.New(log.New(io.Discard), config.New(), "assets")
	root := base.New(c).Command()
	root.InitDefaultCompletionCmd()
	// Cobra derives the man page and completion names from the root command's
	// Use string, which carries usage syntax ("ofga [flags]") the generators
	// would otherwise bake into filenames.
	root.Use = "ofga"
	return root
}

func writeFile(path string, gen func(io.Writer) error) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := gen(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
