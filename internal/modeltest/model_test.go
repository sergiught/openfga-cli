package modeltest

import (
	"errors"
	"testing"
)

func TestLoadModelBuildsBothViews(t *testing.T) {
	lm, err := LoadModel("testdata/docs/model.fga")
	if err != nil {
		t.Fatalf("LoadModel: %v", err)
	}

	if got := lm.Proto.GetSchemaVersion(); got != "1.1" {
		t.Fatalf("Proto.GetSchemaVersion() = %q, want %q", got, "1.1")
	}
	if got := len(lm.Proto.GetTypeDefinitions()); got != 3 {
		t.Fatalf("len(Proto.GetTypeDefinitions()) = %d, want 3", got)
	}
	if got := len(lm.SDK.TypeDefinitions); got != 3 {
		t.Fatalf("len(SDK.TypeDefinitions) = %d, want 3", got)
	}
}

func TestRejectModular(t *testing.T) {
	if err := RejectModular("x/fga.mod"); !errors.Is(err, ErrModularModel) {
		t.Fatalf("RejectModular(fga.mod) = %v, want ErrModularModel", err)
	}
	if err := RejectModular("model.fga"); err != nil {
		t.Fatalf("RejectModular(model.fga) = %v, want nil", err)
	}
}
