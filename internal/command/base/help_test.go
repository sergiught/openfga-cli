package base

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestDocsURLPointsAtTheCommandsReferencePage(t *testing.T) {
	root := &cobra.Command{Use: "ofga"}
	model := &cobra.Command{Use: "model"}
	test := &cobra.Command{Use: "test [file]"}
	initCmd := &cobra.Command{Use: "init"}
	version := &cobra.Command{Use: "version"}
	root.AddCommand(model, version)
	model.AddCommand(test)
	test.AddCommand(initCmd)

	for _, tt := range []struct {
		cmd  *cobra.Command
		want string
	}{
		{root, "https://sergiught.github.io/openfga-cli/"},
		{version, "https://sergiught.github.io/openfga-cli/reference/version/"},
		{model, "https://sergiught.github.io/openfga-cli/reference/model/"},
		{test, "https://sergiught.github.io/openfga-cli/reference/model/test/"},
		// The reference stops at two levels; a deeper command links its ancestor.
		{initCmd, "https://sergiught.github.io/openfga-cli/reference/model/test/"},
	} {
		if got := docsURL(tt.cmd); got != tt.want {
			t.Errorf("docsURL(%q) = %q, want %q", tt.cmd.CommandPath(), got, tt.want)
		}
	}
}
