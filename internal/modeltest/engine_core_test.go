package modeltest

import (
	"context"
	"errors"
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"google.golang.org/grpc"
)

type setupFailureAPI struct {
	fgaAPI
	deleted []string
}

func (a *setupFailureAPI) CreateStore(context.Context, *openfgav1.CreateStoreRequest, ...grpc.CallOption) (*openfgav1.CreateStoreResponse, error) {
	return &openfgav1.CreateStoreResponse{Id: "leak-me-not"}, nil
}

func (a *setupFailureAPI) WriteAuthorizationModel(context.Context, *openfgav1.WriteAuthorizationModelRequest, ...grpc.CallOption) (*openfgav1.WriteAuthorizationModelResponse, error) {
	return nil, errors.New("model rejected")
}

func (a *setupFailureAPI) DeleteStore(_ context.Context, req *openfgav1.DeleteStoreRequest, _ ...grpc.CallOption) (*openfgav1.DeleteStoreResponse, error) {
	a.deleted = append(a.deleted, req.GetStoreId())
	return &openfgav1.DeleteStoreResponse{}, nil
}

func TestSetupCleansStoreAfterModelWriteFailure(t *testing.T) {
	api := &setupFailureAPI{}
	eng := &engine{api: api}

	if _, _, err := eng.Setup(t.Context(), &openfgav1.AuthorizationModel{}, nil); err == nil {
		t.Fatal("Setup() error = nil, want model write failure")
	}
	if len(api.deleted) != 1 || api.deleted[0] != "leak-me-not" {
		t.Fatalf("deleted stores = %v, want [leak-me-not]", api.deleted)
	}
}
