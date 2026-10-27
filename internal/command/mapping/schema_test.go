package mapping

import (
	"bytes"
	"context"
	"io"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeltest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// runJSON executes a `mappings` subcommand with --json and returns stdout
// alone, whatever the exit status.
func runJSON(t *testing.T, c *cli.CLI, args ...string) []byte {
	t.Helper()
	c.JSON = true
	root := New(c).Command()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	root.SilenceUsage, root.SilenceErrors = true, true
	_ = root.Execute()
	return out.Bytes()
}

// TestSchemasValidateRealOutput runs each mappings command with --json and
// checks the document against the published schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	dir := t.TempDir()
	good := writeFile(t, dir, "mapping.yaml", validMapping)
	bad := writeFile(t, dir, "failing.yaml", failingMapping)
	broken := writeFile(t, dir, "broken.yaml", "version: \"1\"\nrules: [not-a-rule]\n")
	model := writeFile(t, dir, "m.fga", "model\n  schema 1.1\n\ntype user\n")

	newCLI := func() *cli.CLI { return cli.New(log.New(io.Discard), config.New(), "test") }

	for _, tc := range []struct {
		name string
		path string
		args []string
	}{
		{"valid", "ofga mappings validate", []string{"validate", good}},
		{"valid with model", "ofga mappings validate", []string{"validate", good, "--model-file", model}},
		{"invalid", "ofga mappings validate", []string{"validate", broken}},
		{"test passing", "ofga mappings test", []string{"test", good}},
		{"test failing", "ofga mappings test", []string{"test", bad}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outputschematest.Validate(t, tc.path, runJSON(t, newCLI(), tc.args...))
		})
	}

	t.Run("apply", func(t *testing.T) {
		ws, err := modeltest.LoadWorkspace("../../modeltest/testdata/docs")
		if err != nil {
			t.Fatal(err)
		}
		endpoint, storeID, modelID, stop, err := modeltest.Seed(context.Background(), ws, "documents/owner-is-viewer", modeltest.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		cfg := config.New()
		cfg.Set("default", config.Profile{APIURL: endpoint, StoreID: storeID, ModelID: modelID})
		m := writeFile(t, dir, "apply.yaml", `version: "1"
rules:
  - name: shared
    when: input.type == "folder.shared"
    tuples:
      - user: "user:{{ input.user }}"
        relation: viewer
        object: "folder:{{ input.folder }}"
`)
		ev := writeFile(t, dir, "events.jsonl", "{\"type\":\"folder.shared\",\"user\":\"zoe\",\"folder\":\"f1\"}\n{\"type\":\"other\"}\n")
		for _, flag := range []string{"--dry-run", "--force"} {
			c := cli.New(log.New(io.Discard), cfg, "test")
			outputschematest.Validate(t, "ofga mappings apply", runJSON(t, c, "apply", m, "--events", ev, flag))
		}
	})
}
