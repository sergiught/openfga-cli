package profiles

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/zalando/go-keyring"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each profiles command with --json against
// an isolated on-disk config and checks the document against the schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	keyring.MockInit()
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	a := cli.New(charmlog.New(io.Discard), cfg, "test")
	a.JSON = true

	run := func(t *testing.T, path string, args ...string) {
		t.Helper()
		root := New(a).Command()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(io.Discard)
		root.SetArgs(args)
		root.SilenceUsage, root.SilenceErrors = true, true
		if err := root.Execute(); err != nil {
			t.Fatalf("profiles %v: %v", args, err)
		}
		outputschematest.Validate(t, path, out.Bytes())
	}

	steps := []struct {
		path string
		args []string
	}{
		{"ofga profiles add", []string{"add", "prod", "--api-url", "http://localhost:8080", "--store-id", "01ARZ3NDEKTSV4RRFFQ69G5FAV"}},
		{"ofga profiles add", []string{"add", "staging", "--api-url", "http://localhost:8081", "--use"}},
		{"ofga profiles list", []string{"list"}},
		{"ofga profiles current", []string{"current"}},
		{"ofga profiles show", []string{"show"}},
		{"ofga profiles show", []string{"show", "prod"}},
		{"ofga profiles use", []string{"use", "prod"}},
		{"ofga profiles set", []string{"set", "store_id", "01ARZ3NDEKTSV4RRFFQ69G5FAZ"}},
		{"ofga profiles unset", []string{"unset", "store_id"}},
		{"ofga profiles cleanup-credentials", []string{"cleanup-credentials"}},
		{"ofga profiles cleanup-credentials", []string{"cleanup-credentials", "--purge", "--force"}},
		{"ofga profiles remove", []string{"remove", "staging", "--force"}},
	}
	for _, s := range steps {
		t.Run(s.path+" "+s.args[0], func(t *testing.T) { run(t, s.path, s.args...) })
	}
}
