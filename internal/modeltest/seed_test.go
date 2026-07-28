package modeltest

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/configtest"
)

// TestSeedServesCheckableStore boots a live embedded server for one test's
// world and asserts an SDK Check answers TRUE over HTTP, without writing any
// config to the isolated config path.
func TestSeedServesCheckableStore(t *testing.T) {
	cfgPath := configtest.Isolate(t)

	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	ctx := context.Background()
	endpoint, storeID, modelID, stop, err := Seed(ctx, ws, "documents/owner-is-viewer", Options{})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	defer stop()

	if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
		t.Fatalf("endpoint = %q, want http://127.0.0.1:<port>", endpoint)
	}
	if storeID == "" || modelID == "" {
		t.Fatalf("storeID=%q modelID=%q, want both non-empty", storeID, modelID)
	}

	cl, err := openfga.NewClient(endpoint, openfga.WithStoreID(storeID), openfga.WithAuthorizationModelID(modelID))
	if err != nil {
		t.Fatalf("new sdk client: %v", err)
	}

	resp, err := cl.Relationships.Check(ctx, &openfga.CheckRequest{
		TupleKey: openfga.CheckRequestTupleKey{User: "user:anne", Relation: "viewer", Object: "document:1"},
	})
	if err != nil {
		t.Fatalf("check over http: %v", err)
	}
	if !resp.Allowed {
		t.Fatalf("check allowed = false, want true (anne is owner, so viewer)")
	}

	// The seed path must never write config.
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("seed wrote config at %s, want none (stat err = %v)", cfgPath, err)
	}
}

// TestSeedNoMatch reports a clear error when the selector matches nothing.
func TestSeedNoMatch(t *testing.T) {
	configtest.Isolate(t)

	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatalf("load workspace: %v", err)
	}

	_, _, _, stop, err := Seed(context.Background(), ws, "does-not-exist", Options{})
	if stop != nil {
		stop()
	}
	if err == nil {
		t.Fatal("Seed with unmatched selector: got nil error, want a no-match error")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("error %q does not mention the selector", err)
	}
}
