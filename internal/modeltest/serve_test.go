package modeltest

import (
	"testing"

	openfgav1 "github.com/openfga/api/proto/openfga/v1"
	"github.com/sergiught/go-openfga/openfga"
)

func TestServeHTTP_ServesStores(t *testing.T) {
	eng, err := NewEmbeddedEngine(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	lm, err := LoadModel("testdata/docs/model.fga")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := SetupStore(t.Context(), eng, "demo", lm.Proto, nil); err != nil {
		t.Fatalf("SetupStore: %v", err)
	}

	endpoint, stop, err := ServeHTTP(t.Context(), eng)
	if err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	cl, err := openfga.NewClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cl.Stores.List(t.Context(), nil)
	if err != nil {
		t.Fatalf("list stores: %v", err)
	}
	found := false
	for _, s := range resp.Stores {
		found = found || s.Name == "demo"
	}
	if !found {
		t.Fatalf("store demo not listed: %+v", resp.Stores)
	}
	stop()
	stop() // idempotent
}

type foreignEngine struct{ Engine }

func TestServeHTTP_RejectsNonEmbedded(t *testing.T) {
	if _, _, err := ServeHTTP(t.Context(), foreignEngine{}); err == nil {
		t.Fatal("ServeHTTP on non-embedded engine: want error")
	}
	if _, _, err := SetupStore(t.Context(), foreignEngine{}, "x", &openfgav1.AuthorizationModel{}, nil); err == nil {
		t.Fatal("SetupStore on foreign engine: want error")
	}
}
