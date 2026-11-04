package optimize

import (
	"os"
	"slices"
	"testing"

	"github.com/sergiught/openfga-cli/internal/modeltest"
)

func TestUnexercised(t *testing.T) {
	data, err := os.ReadFile("testdata/cond_tupleset.fga")
	if err != nil {
		t.Fatal(err)
	}
	lm, err := modeltest.LoadModelBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModel(lm.Proto)
	if err != nil {
		t.Fatal(err)
	}
	applied := []Finding{{Type: "doc", Relation: "viewer"}, {Type: "folder", Relation: "admin"}, {Type: "doc", Relation: "viewer"}}
	got := Unexercised(m, applied, []string{"doc#parent: conditioned-only restriction folder (condition active)"})
	if !slices.Equal(got, []string{"doc#viewer"}) {
		t.Errorf("got %v", got)
	}
}
