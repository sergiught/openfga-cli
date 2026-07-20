package modeltest

import "testing"

func TestLoadModelBuildsBothViews(t *testing.T) {
	lm, err := loadModel("testdata/docs/model.fga")
	if err != nil {
		t.Fatalf("loadModel: %v", err)
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
