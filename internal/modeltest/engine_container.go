package modeltest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// containerStartupTimeout bounds how long NewContainerEngine waits for the
// server to report healthy. It defaults to a generous window (a first-run image
// pull can be slow) and can be overridden with OFGA_CONTAINER_STARTUP_TIMEOUT
// (any Go duration, e.g. "5m").
func containerStartupTimeout() time.Duration {
	const def = 120 * time.Second
	if v := os.Getenv("OFGA_CONTAINER_STARTUP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

// NewRemoteEngine builds an Engine that runs tests against an OpenFGA server
// already listening at addr (a gRPC "host:port"). The caller owns the server's
// lifecycle; Close only releases the connection.
func NewRemoteEngine(addr string) (Engine, error) {
	conn, err := dialGRPC(addr)
	if err != nil {
		return nil, err
	}
	return &engine{
		api:     openfgav1.NewOpenFGAServiceClient(conn),
		closeFn: conn.Close,
	}, nil
}

// NewContainerEngine starts an OpenFGA container from image, waits for it to be
// healthy, and runs tests against it over gRPC — so a model can be tested
// against a specific server version rather than the embedded one. Close
// terminates the container. Requires a running Docker daemon.
func NewContainerEngine(ctx context.Context, image string) (Engine, error) {
	req := testcontainers.ContainerRequest{
		Image:        image,
		Cmd:          []string{"run"},
		ExposedPorts: []string{"8080/tcp", "8081/tcp"}, // HTTP (health), gRPC
		WaitingFor: wait.ForHTTP("/healthz").
			WithPort("8080/tcp").
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(containerStartupTimeout()),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, fmt.Errorf("start openfga container %q (is Docker running?): %w", image, err)
	}

	endpoint, err := container.PortEndpoint(ctx, "8081/tcp", "")
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, fmt.Errorf("resolve container gRPC endpoint: %w", err)
	}

	conn, err := dialGRPC(endpoint)
	if err != nil {
		_ = container.Terminate(context.Background())
		return nil, err
	}

	return &engine{
		api: openfgav1.NewOpenFGAServiceClient(conn),
		closeFn: func() error {
			_ = conn.Close()
			return container.Terminate(context.Background())
		},
	}, nil
}

// maxGRPCRecvBytes lifts the default 4 MB gRPC receive limit. Expand,
// ListObjects and ListUsers responses are unbounded (unlike Read/Write, which
// are paged/chunked), so a relation with thousands of members or a large list
// result would otherwise fail with "message larger than max".
const maxGRPCRecvBytes = 64 * 1024 * 1024

func dialGRPC(target string) (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxGRPCRecvBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial openfga gRPC at %s: %w", target, err)
	}
	return conn, nil
}
