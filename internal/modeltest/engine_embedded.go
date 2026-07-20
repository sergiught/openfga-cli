package modeltest

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/server"
	"github.com/openfga/openfga/pkg/storage/memory"
	"google.golang.org/grpc"
)

// defaultListMaxResults raises the embedded server's ListObjects/ListUsers
// result caps well above their stock 1000 so a list assertion over a legitimate
// (hand-authored, test-sized) fixture is answered in full rather than silently
// truncated — a truncated result would fail the assertion as if the model were
// wrong. A manifest `server:` option can override either cap.
const defaultListMaxResults uint32 = 1_000_000

// NewEmbeddedEngine builds an Engine backed by a single in-process OpenFGA
// server and in-memory datastore. serverOpts carries manifest `server:`
// options; an unknown key is an error. A nil/empty map uses the
// datastore/server defaults.
func NewEmbeddedEngine(serverOpts map[string]any) (Engine, error) {
	dsOpts, srvOpts, err := parseServerOpts(serverOpts)
	if err != nil {
		return nil, err
	}

	ds := memory.New(dsOpts...)
	// Raised list caps come first so a manifest `server:` override (appended
	// after) still wins.
	base := []server.OpenFGAServiceV1Option{
		server.WithDatastore(ds),
		server.WithListObjectsMaxResults(defaultListMaxResults),
		server.WithListUsersMaxResults(defaultListMaxResults),
	}
	srv, err := server.NewServerWithOpts(append(base, srvOpts...)...)
	if err != nil {
		return nil, fmt.Errorf("new embedded openfga server: %w", err)
	}

	return &engine{
		api:     serverAdapter{srv: srv},
		closeFn: func() error { srv.Close(); return nil },
	}, nil
}

// embeddedServerOf returns the in-process server backing e, if e is an embedded
// engine — so the seed path can expose it over HTTP via grpc-gateway. A
// container/remote engine has no local server and reports false.
func embeddedServerOf(e Engine) (*server.Server, bool) {
	eng, ok := e.(*engine)
	if !ok {
		return nil, false
	}
	sa, ok := eng.api.(serverAdapter)
	if !ok {
		return nil, false
	}
	return sa.srv, true
}

// serverAdapter adapts the in-process *server.Server (whose methods take no
// grpc.CallOption) to the fgaAPI the shared engine calls.
type serverAdapter struct{ srv *server.Server }

func (a serverAdapter) CreateStore(ctx context.Context, r *openfgav1.CreateStoreRequest, _ ...grpc.CallOption) (*openfgav1.CreateStoreResponse, error) {
	return a.srv.CreateStore(ctx, r)
}

func (a serverAdapter) DeleteStore(ctx context.Context, r *openfgav1.DeleteStoreRequest, _ ...grpc.CallOption) (*openfgav1.DeleteStoreResponse, error) {
	return a.srv.DeleteStore(ctx, r)
}

func (a serverAdapter) WriteAuthorizationModel(ctx context.Context, r *openfgav1.WriteAuthorizationModelRequest, _ ...grpc.CallOption) (*openfgav1.WriteAuthorizationModelResponse, error) {
	return a.srv.WriteAuthorizationModel(ctx, r)
}

func (a serverAdapter) Write(ctx context.Context, r *openfgav1.WriteRequest, _ ...grpc.CallOption) (*openfgav1.WriteResponse, error) {
	return a.srv.Write(ctx, r)
}

func (a serverAdapter) Check(ctx context.Context, r *openfgav1.CheckRequest, _ ...grpc.CallOption) (*openfgav1.CheckResponse, error) {
	return a.srv.Check(ctx, r)
}

func (a serverAdapter) ListObjects(ctx context.Context, r *openfgav1.ListObjectsRequest, _ ...grpc.CallOption) (*openfgav1.ListObjectsResponse, error) {
	return a.srv.ListObjects(ctx, r)
}

func (a serverAdapter) ListUsers(ctx context.Context, r *openfgav1.ListUsersRequest, _ ...grpc.CallOption) (*openfgav1.ListUsersResponse, error) {
	return a.srv.ListUsers(ctx, r)
}

func (a serverAdapter) Expand(ctx context.Context, r *openfgav1.ExpandRequest, _ ...grpc.CallOption) (*openfgav1.ExpandResponse, error) {
	return a.srv.Expand(ctx, r)
}

func (a serverAdapter) Read(ctx context.Context, r *openfgav1.ReadRequest, _ ...grpc.CallOption) (*openfgav1.ReadResponse, error) {
	return a.srv.Read(ctx, r)
}

// parseServerOpts translates the manifest `server:` map into memory datastore
// options and server options. Unknown keys are an error.
func parseServerOpts(opts map[string]any) ([]memory.StorageOption, []server.OpenFGAServiceV1Option, error) {
	var dsOpts []memory.StorageOption
	var srvOpts []server.OpenFGAServiceV1Option

	for key, val := range opts {
		switch key {
		case "max_types_per_authorization_model":
			n, err := toInt(key, val)
			if err != nil {
				return nil, nil, err
			}
			dsOpts = append(dsOpts, memory.WithMaxTypesPerAuthorizationModel(n))
		case "resolve_node_limit":
			n, err := toUint32(key, val)
			if err != nil {
				return nil, nil, err
			}
			srvOpts = append(srvOpts, server.WithResolveNodeLimit(n))
		case "resolve_node_breadth_limit":
			n, err := toUint32(key, val)
			if err != nil {
				return nil, nil, err
			}
			srvOpts = append(srvOpts, server.WithResolveNodeBreadthLimit(n))
		case "max_concurrent_reads_for_check":
			n, err := toUint32(key, val)
			if err != nil {
				return nil, nil, err
			}
			srvOpts = append(srvOpts, server.WithMaxConcurrentReadsForCheck(n))
		case "list_objects_max_results":
			n, err := toUint32(key, val)
			if err != nil {
				return nil, nil, err
			}
			srvOpts = append(srvOpts, server.WithListObjectsMaxResults(n))
		case "list_users_max_results":
			n, err := toUint32(key, val)
			if err != nil {
				return nil, nil, err
			}
			srvOpts = append(srvOpts, server.WithListUsersMaxResults(n))
		default:
			return nil, nil, fmt.Errorf("unknown server option %q", key)
		}
	}

	return dsOpts, srvOpts, nil
}

func toInt(key string, val any) (int, error) {
	switch v := val.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("server option %q: want a number, got %T", key, val)
	}
}

func toUint32(key string, val any) (uint32, error) {
	n, err := toInt(key, val)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, fmt.Errorf("server option %q: want a non-negative number, got %d", key, n)
	}
	return uint32(n), nil
}
