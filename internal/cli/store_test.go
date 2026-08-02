package cli

import (
	"io"
	"strings"
	"testing"

	"charm.land/log/v2"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
)

const validStoreID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"

func newTestCLI(t *testing.T, p config.Profile) *CLI {
	t.Helper()
	cfg := config.New()
	cfg.Set("default", p)
	return New(log.New(io.Discard), cfg, "test")
}

// A malformed store ID must not break commands that never touch a store. The
// SDK validates at client construction, so passing it through made even
// `ofga stores list` fail on a stale OPENFGA_STORE_ID.
func TestClientToleratesAMalformedStoreID(t *testing.T) {
	c := newTestCLI(t, config.Profile{APIURL: "http://localhost:8080", StoreID: "not-a-ulid"})
	if _, err := c.Client(); err != nil {
		t.Fatalf("Client() = %v, want a usable client for store-independent commands", err)
	}
}

// Commands that do need a store must reject it, as a usage error naming the
// layer the value came from.
func TestClientWithStoreRejectsAMalformedStoreID(t *testing.T) {
	t.Setenv("OPENFGA_STORE_ID", "not-a-ulid")
	c := newTestCLI(t, config.Profile{APIURL: "http://localhost:8080"})

	_, _, err := c.ClientWithStore()
	if err == nil {
		t.Fatal("a malformed store ID should fail a store-scoped command")
	}
	if got := clierr.Code(err); got != clierr.CodeUsage {
		t.Errorf("exit code = %d, want usage (%d)", got, clierr.CodeUsage)
	}
	if !strings.Contains(err.Error(), "OPENFGA_STORE_ID") {
		t.Errorf("error = %q, want it to name the source of the value", err)
	}
}

func TestClientWithStoreAcceptsAValidStoreID(t *testing.T) {
	c := newTestCLI(t, config.Profile{APIURL: "http://localhost:8080", StoreID: validStoreID})
	if _, _, err := c.ClientWithStore(); err != nil {
		t.Fatalf("ClientWithStore() = %v, want success for a valid ULID", err)
	}
}
