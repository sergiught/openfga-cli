package model

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/log/v2"
	"github.com/sergiught/go-openfga/openfga"

	"github.com/sergiught/openfga-cli/internal/cli"
	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/config"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/modeldiff"
	"github.com/sergiught/openfga-cli/internal/modeltest"
)

const (
	diffTestModelID = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	diffTestDSL     = "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [user]\n"
	diffTestJSON    = `{"schema_version":"1.1","type_definitions":[{"type":"user"},{"type":"doc","relations":{"viewer":{"this":{}}},"metadata":{"relations":{"viewer":{"directly_related_user_types":[{"type":"user"}]}}}}]}`
)

func diffCommand(t *testing.T, apiURL string) *Command {
	t.Helper()
	configtest.Isolate(t)
	cfg := config.New()
	cfg.Set("default", config.Profile{APIURL: apiURL, StoreID: diffTestModelID})
	return New(cli.New(log.New(io.Discard), cfg, "test"))
}

func writeDiffFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// storeServer serves diffTestJSON as the store's only model and records
// request paths.
func storeServer(t *testing.T, paths *[]string, empty bool) string {
	t.Helper()
	var m openfga.AuthorizationModel
	if err := json.Unmarshal([]byte(diffTestJSON), &m); err != nil {
		t.Fatal(err)
	}
	m.ID = diffTestModelID
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if paths != nil {
			*paths = append(*paths, r.URL.Path)
		}
		if strings.HasSuffix(r.URL.Path, "/authorization-models") {
			resp := openfga.ListAuthorizationModelsResponse{}
			if !empty {
				resp.AuthorizationModels = []openfga.AuthorizationModel{m}
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_model": m})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestResolveDiffArgs(t *testing.T) {
	configtest.Isolate(t)
	if o, n, err := resolveDiffArgs([]string{"a.fga"}); err != nil || o != implicitStoreArg || n != "a.fga" {
		t.Fatalf("one arg = (%q, %q, %v)", o, n, err)
	}
	if o, n, err := resolveDiffArgs([]string{"a", "b"}); err != nil || o != "a" || n != "b" {
		t.Fatalf("two args = (%q, %q, %v)", o, n, err)
	}
	if _, _, err := resolveDiffArgs([]string{"-", "-"}); clierr.Code(err) != clierr.CodeUsage {
		t.Fatalf("both stdin: code = %v, err = %v", clierr.Code(err), err)
	}
}

func TestLoadDiffSideFile(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	for name, body := range map[string]string{"model.fga": diffTestDSL, "model.json": diffTestJSON} {
		p := writeDiffFile(t, name, body)
		got, err := c.loadDiffSide(t.Context(), nil, "old", p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Label != p || got.File != p || got.Model == nil {
			t.Fatalf("%s: side = %+v", name, got)
		}
	}
}

func TestLoadDiffSideStdin(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	got, err := c.loadDiffSide(t.Context(), strings.NewReader(diffTestDSL), "old", "-")
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "stdin" || got.File != "" || got.Model == nil {
		t.Fatalf("side = %+v", got)
	}
}

func TestLoadDiffSideModular(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	p := writeDiffFile(t, "fga.mod", "schema: '1.2'\n")
	_, err := c.loadDiffSide(t.Context(), nil, "new", p)
	if clierr.Code(err) != clierr.CodeUsage || !errors.Is(err, modeltest.ErrModularModel) {
		t.Fatalf("code = %v, err = %v", clierr.Code(err), err)
	}
}

func TestLoadDiffSideUnknownArg(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	_, err := c.loadDiffSide(t.Context(), nil, "new", "nope")
	if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), `not a file, "store", or a model ID`) {
		t.Fatalf("code = %v, err = %v", clierr.Code(err), err)
	}
}

func TestLoadDiffSideInvalidModel(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	bad := "model\n  schema 1.1\ntype user\ntype doc\n  relations\n    define viewer: [user]\n    define editor: missing\n"
	p := writeDiffFile(t, "bad.fga", bad)
	_, err := c.loadDiffSide(t.Context(), nil, "new", p)
	if clierr.Code(err) != clierr.CodeUsage || !strings.HasPrefix(err.Error(), "new model:") {
		t.Fatalf("code = %v, err = %v", clierr.Code(err), err)
	}
}

func TestLoadDiffSideStoreLatest(t *testing.T) {
	c := diffCommand(t, storeServer(t, nil, false))
	got, err := c.loadDiffSide(t.Context(), nil, "old", "store")
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "store latest "+diffTestModelID || got.File != "" || got.Model == nil {
		t.Fatalf("side = %+v", got)
	}
}

func TestLoadDiffSideStoreByID(t *testing.T) {
	var paths []string
	c := diffCommand(t, storeServer(t, &paths, false))
	got, err := c.loadDiffSide(t.Context(), nil, "old", diffTestModelID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Label != "store model "+diffTestModelID {
		t.Fatalf("label = %q", got.Label)
	}
	if len(paths) != 1 || !strings.HasSuffix(paths[0], "/authorization-models/"+diffTestModelID) {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestLoadDiffSideEmptyStore(t *testing.T) {
	c := diffCommand(t, storeServer(t, nil, true))
	_, err := c.loadDiffSide(t.Context(), nil, "old", "store")
	if err == nil || !strings.Contains(err.Error(), "has no authorization model") {
		t.Fatalf("err = %v", err)
	}
	if clierr.Code(err) == clierr.CodeUsage {
		t.Fatalf("empty store must not be a usage error: %v", err)
	}
}

func TestDiffFileVsStoreSameModel(t *testing.T) {
	c := diffCommand(t, storeServer(t, nil, false))
	p := writeDiffFile(t, "model.fga", diffTestDSL)
	oldSide, err := c.loadDiffSide(t.Context(), nil, "old", p)
	if err != nil {
		t.Fatal(err)
	}
	newSide, err := c.loadDiffSide(t.Context(), nil, "new", "store")
	if err != nil {
		t.Fatal(err)
	}
	if !modeldiff.Diff(oldSide.Model, newSide.Model).Empty() {
		t.Fatal("the same model from a file and from the store must diff empty")
	}
}

func TestLoadDiffSideMissingPathSaysNoSuchFile(t *testing.T) {
	c := diffCommand(t, "http://127.0.0.1:1")
	for _, arg := range []string{"nope.fga", "nope.json", "dir/nope"} {
		_, err := c.loadDiffSide(t.Context(), nil, "new", arg)
		if clierr.Code(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "no such file") {
			t.Errorf("%s: code = %v, err = %v", arg, clierr.Code(err), err)
		}
	}
	_, err := c.loadDiffSide(t.Context(), nil, "new", "nope")
	if err == nil || strings.Contains(err.Error(), "no such file") {
		t.Errorf("a bare word should keep the keyword/ID hint: %v", err)
	}
}
