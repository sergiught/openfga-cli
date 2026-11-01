// Package optimize analyses authorization models and proposes
// equivalence-preserving rewrites.
package optimize

import (
	"context"
	"fmt"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/language/pkg/go/transformer"
	"github.com/openfga/openfga/pkg/typesystem"
	"google.golang.org/protobuf/proto"
)

// Model wraps the proto with lookups the rewrite rules need.
type Model struct{ Proto *openfgav1.AuthorizationModel }

// NewModel validates p and wraps it.
func NewModel(p *openfgav1.AuthorizationModel) (*Model, error) {
	if _, err := typesystem.NewAndValidate(context.Background(), p); err != nil {
		return nil, fmt.Errorf("invalid authorization model: %w", err)
	}
	return &Model{Proto: p}, nil
}

// Relation returns the rewrite of typ#rel.
func (m *Model) Relation(typ, rel string) (*openfgav1.Userset, bool) {
	for _, td := range m.Proto.GetTypeDefinitions() {
		if td.GetType() != typ {
			continue
		}
		us, ok := td.GetRelations()[rel]
		return us, ok
	}
	return nil, false
}

// DirectTypes returns the directly related user types of typ#rel.
func (m *Model) DirectTypes(typ, rel string) []*openfgav1.RelationReference {
	for _, td := range m.Proto.GetTypeDefinitions() {
		if td.GetType() != typ {
			continue
		}
		return td.GetMetadata().GetRelations()[rel].GetDirectlyRelatedUserTypes()
	}
	return nil
}

// Clone returns a deep copy.
func (m *Model) Clone() *Model {
	return &Model{Proto: proto.Clone(m.Proto).(*openfgav1.AuthorizationModel)}
}

// DSL renders the model as DSL.
func (m *Model) DSL() (string, error) {
	return transformer.TransformJSONProtoToDSL(m.Proto)
}
