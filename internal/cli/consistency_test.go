package cli

import (
	"strings"
	"testing"

	"github.com/spf13/pflag"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/clierr"
)

func TestConsistencyOptionUnsetYieldsNoOption(t *testing.T) {
	for _, v := range []string{"", "  "} {
		opts, err := ConsistencyOption(v)
		if err != nil {
			t.Fatalf("ConsistencyOption(%q) = %v", v, err)
		}
		if opts != nil {
			t.Fatalf("ConsistencyOption(%q) returned %d option(s), want none so the client default applies", v, len(opts))
		}
	}
}

func TestConsistencyOptionAcceptsEveryValueCaseInsensitively(t *testing.T) {
	for _, want := range consistencyValues {
		for _, in := range []string{string(want), strings.ToLower(string(want))} {
			opts, err := ConsistencyOption(in)
			if err != nil {
				t.Fatalf("ConsistencyOption(%q) = %v", in, err)
			}
			if len(opts) != 1 {
				t.Fatalf("ConsistencyOption(%q) returned %d option(s), want 1", in, len(opts))
			}
		}
	}
}

func TestConsistencyOptionRejectsUnknownValue(t *testing.T) {
	opts, err := ConsistencyOption("EVENTUAL")
	if opts != nil {
		t.Fatalf("ConsistencyOption returned an option for an unknown value")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Fatalf("exit code = %d, want usage; err=%v", got, err)
	}
	for _, want := range consistencyValues {
		if !strings.Contains(err.Error(), string(want)) {
			t.Errorf("error %q does not list %s", err, want)
		}
	}
}

func TestRegisterConsistencyFlagDocumentsTheDefault(t *testing.T) {
	var target string
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RegisterConsistencyFlag(fs, &target)
	f := fs.Lookup("consistency")
	if f == nil {
		t.Fatal("--consistency not registered")
	}
	if !strings.Contains(f.Usage, string(openfga.ConsistencyHigherConsistency)) {
		t.Errorf("usage %q does not name the default", f.Usage)
	}
}
