package modeltest

import (
	"context"
	"fmt"
	"time"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/tuple"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// fgaAPI is the subset of the OpenFGA gRPC service the runner uses. The gRPC
// client (openfgav1.OpenFGAServiceClient) satisfies it directly; the in-process
// server is adapted to it (see serverAdapter). Sharing this one interface lets
// the embedded and container engines run identical request code — a test must
// pass or fail the same way regardless of which server backs it.
type fgaAPI interface {
	CreateStore(context.Context, *openfgav1.CreateStoreRequest, ...grpc.CallOption) (*openfgav1.CreateStoreResponse, error)
	DeleteStore(context.Context, *openfgav1.DeleteStoreRequest, ...grpc.CallOption) (*openfgav1.DeleteStoreResponse, error)
	WriteAuthorizationModel(context.Context, *openfgav1.WriteAuthorizationModelRequest, ...grpc.CallOption) (*openfgav1.WriteAuthorizationModelResponse, error)
	Write(context.Context, *openfgav1.WriteRequest, ...grpc.CallOption) (*openfgav1.WriteResponse, error)
	Check(context.Context, *openfgav1.CheckRequest, ...grpc.CallOption) (*openfgav1.CheckResponse, error)
	ListObjects(context.Context, *openfgav1.ListObjectsRequest, ...grpc.CallOption) (*openfgav1.ListObjectsResponse, error)
	ListUsers(context.Context, *openfgav1.ListUsersRequest, ...grpc.CallOption) (*openfgav1.ListUsersResponse, error)
	Expand(context.Context, *openfgav1.ExpandRequest, ...grpc.CallOption) (*openfgav1.ExpandResponse, error)
	Read(context.Context, *openfgav1.ReadRequest, ...grpc.CallOption) (*openfgav1.ReadResponse, error)
}

// engine is the shared Engine implementation over an fgaAPI. NewEmbeddedEngine
// backs it with an in-process server; NewContainerEngine/NewRemoteEngine back it
// with a gRPC client to a real OpenFGA. closeFn releases whatever the engine
// owns (server, gRPC connection, container).
type engine struct {
	api     fgaAPI
	closeFn func() error
}

// writeChunkSize caps the number of tuple keys sent in a single Write call.
const writeChunkSize = 40

// readPageSize bounds the tuples fetched per Read page; readTuples follows the
// continuation token across pages, so this caps memory per request, not the
// total number of tuples resolved.
const readPageSize = 100

// rpcMessage unwraps a gRPC status error to its bare human message, dropping the
// "rpc error: code = ... desc = " envelope — noise for a user whose real mistake
// is in their model or fixtures, not in any RPC transport.
type friendlyRPCError struct {
	message string
	cause   error
}

func (e *friendlyRPCError) Error() string { return e.message }
func (e *friendlyRPCError) Unwrap() error { return e.cause }

func rpcMessage(err error) error {
	if st, ok := status.FromError(err); ok {
		return &friendlyRPCError{message: st.Message(), cause: err}
	}
	return err
}

func (e *engine) Setup(ctx context.Context, model *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey) (string, string, error) {
	storeResp, err := e.api.CreateStore(ctx, &openfgav1.CreateStoreRequest{Name: "modeltest"})
	if err != nil {
		return "", "", fmt.Errorf("create store: %w", rpcMessage(err))
	}
	storeID := storeResp.GetId()
	setupComplete := false
	defer func() {
		if setupComplete {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = e.api.DeleteStore(cleanupCtx, &openfgav1.DeleteStoreRequest{StoreId: storeID})
	}()

	modelResp, err := e.api.WriteAuthorizationModel(ctx, &openfgav1.WriteAuthorizationModelRequest{
		StoreId:         storeID,
		TypeDefinitions: model.GetTypeDefinitions(),
		SchemaVersion:   model.GetSchemaVersion(),
		Conditions:      model.GetConditions(),
	})
	if err != nil {
		return "", "", fmt.Errorf("write authorization model: %w", rpcMessage(err))
	}
	modelID := modelResp.GetAuthorizationModelId()

	for start := 0; start < len(tuples); start += writeChunkSize {
		end := start + writeChunkSize
		if end > len(tuples) {
			end = len(tuples)
		}
		_, err := e.api.Write(ctx, &openfgav1.WriteRequest{
			StoreId:              storeID,
			AuthorizationModelId: modelID,
			Writes:               &openfgav1.WriteRequestWrites{TupleKeys: tuples[start:end]},
		})
		if err != nil {
			return "", "", fmt.Errorf("write tuples: %w", rpcMessage(err))
		}
	}

	setupComplete = true
	return storeID, modelID, nil
}

func (e *engine) DeleteStore(ctx context.Context, storeID string) error {
	if storeID == "" {
		return nil
	}
	if _, err := e.api.DeleteStore(ctx, &openfgav1.DeleteStoreRequest{StoreId: storeID}); err != nil {
		return fmt.Errorf("delete store: %w", rpcMessage(err))
	}
	return nil
}

func (e *engine) Check(ctx context.Context, s Scope, r CheckReq) (bool, error) {
	ctxStruct, err := toStruct(r.Context)
	if err != nil {
		return false, err
	}

	req := &openfgav1.CheckRequest{
		StoreId:              s.StoreID,
		AuthorizationModelId: s.ModelID,
		TupleKey: &openfgav1.CheckRequestTupleKey{
			User:     r.User,
			Relation: r.Relation,
			Object:   r.Object,
		},
		Context: ctxStruct,
	}
	if len(r.ContextualTuples) > 0 {
		req.ContextualTuples = &openfgav1.ContextualTupleKeys{TupleKeys: r.ContextualTuples}
	}

	resp, err := e.api.Check(ctx, req)
	if err != nil {
		return false, fmt.Errorf("check: %w", rpcMessage(err))
	}
	return resp.GetAllowed(), nil
}

func (e *engine) ListObjects(ctx context.Context, s Scope, r ListObjectsReq) ([]string, error) {
	ctxStruct, err := toStruct(r.Context)
	if err != nil {
		return nil, err
	}

	resp, err := e.api.ListObjects(ctx, &openfgav1.ListObjectsRequest{
		StoreId:              s.StoreID,
		AuthorizationModelId: s.ModelID,
		Type:                 r.Type,
		Relation:             r.Relation,
		User:                 r.User,
		Context:              ctxStruct,
	})
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", rpcMessage(err))
	}
	return resp.GetObjects(), nil
}

func (e *engine) ListUsers(ctx context.Context, s Scope, r ListUsersReq) ([]string, error) {
	ctxStruct, err := toStruct(r.Context)
	if err != nil {
		return nil, err
	}

	objType, objID := tuple.SplitObject(r.Object)

	filters := make([]*openfgav1.UserTypeFilter, 0, len(r.Filters))
	for _, f := range r.Filters {
		filters = append(filters, &openfgav1.UserTypeFilter{Type: f.Type, Relation: f.Relation})
	}

	resp, err := e.api.ListUsers(ctx, &openfgav1.ListUsersRequest{
		StoreId:              s.StoreID,
		AuthorizationModelId: s.ModelID,
		Object:               &openfgav1.Object{Type: objType, Id: objID},
		Relation:             r.Relation,
		UserFilters:          filters,
		Context:              ctxStruct,
	})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", rpcMessage(err))
	}

	users := make([]string, 0, len(resp.GetUsers()))
	for _, u := range resp.GetUsers() {
		users = append(users, tuple.UserProtoToString(u))
	}
	return users, nil
}

func (e *engine) Expand(ctx context.Context, s Scope, object, relation string) (*openfgav1.UsersetTree, error) {
	resp, err := e.api.Expand(ctx, &openfgav1.ExpandRequest{
		StoreId:              s.StoreID,
		AuthorizationModelId: s.ModelID,
		TupleKey: &openfgav1.ExpandRequestTupleKey{
			Object:   object,
			Relation: relation,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("expand: %w", rpcMessage(err))
	}
	return resp.GetTree(), nil
}

func (e *engine) Read(ctx context.Context, s Scope, object, relation string) ([]string, error) {
	tuples, err := e.readTuples(ctx, s, object, relation)
	if err != nil {
		return nil, err
	}
	users := make([]string, 0, len(tuples))
	for _, tp := range tuples {
		users = append(users, tp.GetKey().GetUser())
	}
	return users, nil
}

func (e *engine) ReadConditions(ctx context.Context, s Scope, object, relation string) (map[string][]string, error) {
	tuples, err := e.readTuples(ctx, s, object, relation)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, tp := range tuples {
		if name := tp.GetKey().GetCondition().GetName(); name != "" {
			user := tp.GetKey().GetUser()
			out[user] = append(out[user], name)
		}
	}
	return out, nil
}

// readTuples pages through every tuple matching object#relation. It follows the
// continuation token so a relation with more than readPageSize direct tuples is
// resolved in full rather than silently truncated at the first page.
func (e *engine) readTuples(ctx context.Context, s Scope, object, relation string) ([]*openfgav1.Tuple, error) {
	var (
		tuples []*openfgav1.Tuple
		token  string
	)
	for {
		resp, err := e.api.Read(ctx, &openfgav1.ReadRequest{
			StoreId: s.StoreID,
			TupleKey: &openfgav1.ReadRequestTupleKey{
				Object:   object,
				Relation: relation,
			},
			PageSize:          wrapperspb.Int32(readPageSize),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("read: %w", rpcMessage(err))
		}
		tuples = append(tuples, resp.GetTuples()...)
		token = resp.GetContinuationToken()
		if token == "" {
			break
		}
	}
	return tuples, nil
}

func (e *engine) Close() error {
	if e.closeFn != nil {
		return e.closeFn()
	}
	return nil
}

// toStruct converts a manifest context map into a structpb.Struct, or nil when
// m is empty (nil or zero-length), so callers uniformly send no context struct
// rather than an empty one.
func toStruct(m map[string]any) (*structpb.Struct, error) {
	if len(m) == 0 {
		return nil, nil
	}
	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("build context struct: %w", err)
	}
	return s, nil
}
