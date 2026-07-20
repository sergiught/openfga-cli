package modeltest

import (
	"context"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
)

// Resolver answers the read queries the trace narrator and coverage need to
// explain how a verdict was reached: the Check itself plus the raw
// Expand/Read/ReadConditions primitives. It is the narrow interface trace()
// depends on, kept separate from the full Engine so the "explain needs deeper
// access than running a test" seam is explicit.
type Resolver interface {
	Check(ctx context.Context, s Scope, r CheckReq) (bool, error)
	// Expand returns the userset (resolution) tree for object#relation, used
	// by the narrator to explain how a verdict was reached.
	Expand(ctx context.Context, s Scope, object, relation string) (*openfgav1.UsersetTree, error)
	// Read returns the "user" side of the tuples matching object#relation. It
	// backs the narrator's tuple-to-userset resolution.
	Read(ctx context.Context, s Scope, object, relation string) ([]string, error)
	// ReadConditions returns, for object#relation, the ABAC condition name(s)
	// attached to each user's direct tuple(s), keyed by user string (a user with
	// no condition is absent from the map). It backs per-condition coverage
	// attribution so a condition:<name>=true/=false branch is only credited when
	// that specific condition was exercised.
	ReadConditions(ctx context.Context, s Scope, object, relation string) (map[string][]string, error)
}

// Engine runs a test suite against an authorization model and tuple set: it
// sets up a fresh store, answers Check/ListObjects/ListUsers assertions, cleans
// up, and (via the embedded Resolver) supplies the read primitives the
// narrator/coverage need.
type Engine interface {
	Resolver
	Setup(ctx context.Context, model *openfgav1.AuthorizationModel, tuples []*openfgav1.TupleKey) (storeID, modelID string, err error)
	ListObjects(ctx context.Context, s Scope, r ListObjectsReq) ([]string, error)
	ListUsers(ctx context.Context, s Scope, r ListUsersReq) ([]string, error)
	// DeleteStore removes a store created by Setup. The runner calls it after
	// each test so a large suite's memory doesn't grow with the sum of every
	// test's stores; it is best-effort (a cleanup failure never fails a test).
	DeleteStore(ctx context.Context, storeID string) error
	Close() error
}

// Scope identifies the store and authorization model a query runs against.
type Scope struct {
	StoreID string
	ModelID string
}

// CheckReq is a single Check query.
type CheckReq struct {
	User             string
	Relation         string
	Object           string
	Context          map[string]any
	ContextualTuples []*openfgav1.TupleKey
}

// ListObjectsReq is a single ListObjects query.
type ListObjectsReq struct {
	User     string
	Relation string
	Type     string
	Context  map[string]any
}

// ListUsersReq is a single ListUsers query.
type ListUsersReq struct {
	Object   string
	Relation string
	Filters  []ListUsersFilter
	Context  map[string]any
}
