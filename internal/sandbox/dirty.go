package sandbox

import (
	"context"
	"sync/atomic"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/openfga/openfga/pkg/storage"
)

// dirtyDatastore records whether anything was written through it. It embeds the
// datastore interface so every read passes straight through, and overrides
// each mutating method of storage.OpenFGADatastore (v1.22.1): Write,
// WriteAuthorizationModel, CreateStore, DeleteStore, WriteAssertions. The flag
// is set only after the inner call succeeded, so a rejected write is not a
// change.
type dirtyDatastore struct {
	storage.OpenFGADatastore
	dirty atomic.Bool
}

func (d *dirtyDatastore) Write(ctx context.Context, store string, deletes storage.Deletes, writes storage.Writes, opts ...storage.TupleWriteOption) error {
	err := d.OpenFGADatastore.Write(ctx, store, deletes, writes, opts...)
	if err == nil {
		d.dirty.Store(true)
	}
	return err
}

func (d *dirtyDatastore) WriteAuthorizationModel(ctx context.Context, store string, model *openfgav1.AuthorizationModel) error {
	err := d.OpenFGADatastore.WriteAuthorizationModel(ctx, store, model)
	if err == nil {
		d.dirty.Store(true)
	}
	return err
}

func (d *dirtyDatastore) CreateStore(ctx context.Context, s *openfgav1.Store) (*openfgav1.Store, error) {
	created, err := d.OpenFGADatastore.CreateStore(ctx, s)
	if err == nil {
		d.dirty.Store(true)
	}
	return created, err
}

func (d *dirtyDatastore) DeleteStore(ctx context.Context, id string) error {
	err := d.OpenFGADatastore.DeleteStore(ctx, id)
	if err == nil {
		d.dirty.Store(true)
	}
	return err
}

func (d *dirtyDatastore) WriteAssertions(ctx context.Context, store, modelID string, assertions []*openfgav1.Assertion) error {
	err := d.OpenFGADatastore.WriteAssertions(ctx, store, modelID, assertions)
	if err == nil {
		d.dirty.Store(true)
	}
	return err
}
