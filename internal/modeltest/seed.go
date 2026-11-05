package modeltest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// Seed boots the embedded OpenFGA engine as a real localhost HTTP server
// serving a single test's world: it resolves the test selected by sel, loads
// its model, resolves its fixtures, seeds a fresh store on the engine's
// server, and exposes that same server over HTTP via the in-process
// grpc-gateway.
//
// It returns the HTTP endpoint (http://127.0.0.1:<port>), the seeded store and
// model IDs, and an idempotent stop func that shuts the HTTP server down and,
// when Seed created the engine, closes it. Seed never writes any config or
// profile — it only starts an in-memory server.
//
// sel selects one test by "<workspace-relative-file>/<test-name>" or bare test name (same
// matching as --run). When it matches more than one test, the first in
// workspace order is served.
func Seed(ctx context.Context, ws *Workspace, sel string, opts Options) (endpoint, storeID, modelID string, stop func(), err error) {
	tasks, _ := matchTasks(ws, sel)
	if len(tasks) == 0 {
		return "", "", "", nil, fmt.Errorf("no tests matched %q", sel)
	}
	tk := tasks[0]

	modelPath, err := resolveModelPath(ws, tk.tf)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}
	lm, err := LoadModel(modelPath)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}

	tuples, err := resolveFixtures(ws, tk.tf, tk.test, opts.Dedupe, nil)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}
	protoTuples, err := toProtoTuples(tuples)
	if err != nil {
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}

	// Reuse a caller-provided embedded engine so seeding and serving share one
	// store; otherwise build our own and own its lifecycle. Seeding needs the
	// in-process server (to expose over HTTP), so a container/remote engine
	// can't be reused — we build our own embedded one.
	setupEngine := opts.Engine
	_, reused := embeddedServerOf(opts.Engine)
	ownEngine := !reused
	if ownEngine {
		var serverOpts map[string]any
		if ws.Manifest != nil {
			serverOpts = ws.Manifest.Server
		}
		built, buildErr := NewEmbeddedEngine(serverOpts)
		if buildErr != nil {
			return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, buildErr)
		}
		setupEngine = built
	}

	closeEngine := func() {
		if ownEngine {
			_ = setupEngine.Close()
		}
	}

	storeID, modelID, err = setupEngine.Setup(ctx, lm.Proto, protoTuples)
	if err != nil {
		closeEngine()
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}

	endpoint, stopHTTP, err := ServeHTTP(ctx, setupEngine)
	if err != nil {
		closeEngine()
		return "", "", "", nil, fmt.Errorf("seed %s: %w", tk.name, err)
	}

	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() {
			stopHTTP()
			closeEngine()
		})
	}

	return endpoint, storeID, modelID, stop, nil
}

// ServeHTTP exposes the in-process server behind eng over HTTP on a
// loopback-only ephemeral port via grpc-gateway (no gRPC listener, no network
// hop between the gateway and the server). It errors for engines without a
// local server (container/remote). stop shuts the HTTP server down and is
// idempotent; it does NOT close eng, which the caller still owns.
func ServeHTTP(ctx context.Context, eng Engine) (endpoint string, stop func(), err error) {
	srv, ok := embeddedServerOf(eng)
	if !ok {
		return "", nil, fmt.Errorf("serve http: engine %T has no in-process server", eng)
	}
	mux := runtime.NewServeMux()
	if err = openfgav1.RegisterOpenFGAServiceHandlerServer(ctx, mux, srv); err != nil {
		return "", nil, fmt.Errorf("register http handlers: %w", err)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}

	// ReadHeaderTimeout bounds how long a connection can hold the server open
	// while dribbling out request headers. The listener is loopback-only, so
	// this is defense in depth rather than an exposure fix; the timeout covers
	// only the header phase, so it can't cut a slow in-flight query short.
	httpSrv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = httpSrv.Serve(lis) }()

	var stopOnce sync.Once
	stop = func() {
		stopOnce.Do(func() { _ = httpSrv.Shutdown(context.Background()) })
	}
	return "http://" + lis.Addr().String(), stop, nil
}
