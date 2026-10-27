package configcmd

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/spf13/cobra"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each config-related command with --json
// against an isolated config and checks the document against the schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a := cli.New(charmlog.New(io.Discard), cfg, "test")
	a.JSON = true

	for _, tc := range []struct {
		path string
		cmd  *cobra.Command
		args []string
	}{
		{"ofga config path", New(a).pathCmd(), nil},
		{"ofga theme", NewTheme(a), nil},
		{"ofga theme", NewTheme(a), []string{"aurora"}},
		{"ofga init", NewInit(a), []string{"--api-url", "http://localhost:8080", "--force"}},
	} {
		t.Run(tc.path+" "+tc.cmd.Name(), func(t *testing.T) {
			var out bytes.Buffer
			tc.cmd.SetOut(&out)
			tc.cmd.SetErr(io.Discard)
			tc.cmd.SilenceUsage, tc.cmd.SilenceErrors = true, true
			tc.cmd.SetArgs(tc.args)
			if err := tc.cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}
}
