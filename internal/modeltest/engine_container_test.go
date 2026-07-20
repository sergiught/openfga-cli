package modeltest

import (
	"context"
	"os"
	"testing"
)

// TestContainerEngineRunsAgainstRealServer runs the docs workspace against a
// real OpenFGA container to verify the gRPC-backed engine behaves like the
// embedded one. It's gated behind OFGA_DOCKER_TESTS (needs a Docker daemon) so
// the normal `go test` run stays hermetic and fast.
func TestContainerEngineRunsAgainstRealServer(t *testing.T) {
	if os.Getenv("OFGA_DOCKER_TESTS") == "" {
		t.Skip("set OFGA_DOCKER_TESTS=1 to run the container-engine integration test (requires Docker)")
	}
	image := os.Getenv("OFGA_DOCKER_IMAGE")
	if image == "" {
		image = "openfga/openfga:v1.8.0"
	}

	ctx := context.Background()
	eng, err := NewContainerEngine(ctx, image)
	if err != nil {
		t.Fatalf("start container engine: %v", err)
	}
	defer eng.Close()

	ws, err := LoadWorkspace("testdata/docs")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, ws, Options{Engine: eng, Coverage: true})
	if err != nil {
		t.Fatalf("Run against container: %v", err)
	}
	if res.Summary.Failed != 0 {
		t.Fatalf("expected all docs tests to pass against %s, got %+v", image, res.Summary)
	}
	if res.Coverage == nil || res.Coverage.Percent == 0 {
		t.Fatalf("expected coverage (needs Expand/Read over gRPC), got %+v", res.Coverage)
	}
}
