package modeltest

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	transformer "github.com/openfga/language/pkg/go/transformer"
	"github.com/sergiught/go-openfga/openfga"
	protojson "google.golang.org/protobuf/encoding/protojson"
)

// LoadedModel holds an authorization model decoded into both the proto view
// (for the embedded engine) and the SDK view (for the narrator), built from
// a single transformer pass so both stay in sync.
type LoadedModel struct {
	Proto *openfgav1.AuthorizationModel
	SDK   *openfga.AuthorizationModel
}

// loadModel reads the authorization model at path (DSL or JSON) and decodes
// it into both a proto.AuthorizationModel and an openfga.AuthorizationModel.
func loadModel(path string) (*LoadedModel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lm, err := LoadModelBytes(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return lm, nil
}

// LoadModelBytes decodes an authorization model from raw bytes (DSL or JSON),
// autodetecting the format. It backs loadModel and lets callers load a model
// that isn't on disk (e.g. one fetched from a git ref for coverage diffing).
func LoadModelBytes(data []byte) (*LoadedModel, error) {
	jsonStr := string(data)
	// JSON models start with '{'; anything else is treated as DSL.
	if !strings.HasPrefix(strings.TrimSpace(jsonStr), "{") {
		transformed, err := transformer.TransformDSLToJSON(jsonStr)
		if err != nil {
			return nil, fmt.Errorf("transform model DSL: %w", err)
		}
		jsonStr = transformed
	}

	proto := &openfgav1.AuthorizationModel{}
	if err := protojson.Unmarshal([]byte(jsonStr), proto); err != nil {
		return nil, fmt.Errorf("decode proto model: %w", err)
	}

	sdk := &openfga.AuthorizationModel{}
	if err := json.Unmarshal([]byte(jsonStr), sdk); err != nil {
		return nil, fmt.Errorf("decode sdk model: %w", err)
	}

	return &LoadedModel{Proto: proto, SDK: sdk}, nil
}
