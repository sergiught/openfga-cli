package store

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sergiught/go-openfga/openfga"
	"github.com/sergiught/openfga-cli/internal/configtest"
	"github.com/sergiught/openfga-cli/internal/outputschema/outputschematest"
)

// TestSchemasValidateRealOutput runs each store command with --json and checks
// the document against the published schema.
func TestSchemasValidateRealOutput(t *testing.T) {
	configtest.Isolate(t)
	st := openfga.Store{
		ID:        "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Name:      "acme",
		CreatedAt: time.Unix(1600000000, 0).UTC(),
		UpdatedAt: time.Unix(1600000000, 0).UTC(),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(st)
		case r.URL.Path == "/stores":
			_ = json.NewEncoder(w).Encode(openfga.ListStoresResponse{Stores: []openfga.Store{st}})
		default:
			_ = json.NewEncoder(w).Encode(st)
		}
	}))
	defer srv.Close()

	cases := []struct {
		path string
		args []string
		sub  string
	}{
		{"ofga stores create", []string{"acme"}, "create"},
		{"ofga stores create", []string{"acme", "--use"}, "create"},
		{"ofga stores create", []string{"acme", "--dry-run"}, "create"},
		{"ofga stores list", nil, "list"},
		{"ofga stores get", []string{st.ID}, "get"},
		{"ofga stores delete", []string{st.ID, "--force"}, "delete"},
		{"ofga stores delete", []string{st.ID, "--dry-run"}, "delete"},
	}
	for _, tc := range cases {
		t.Run(tc.path+" "+strings.Join(tc.args, " "), func(t *testing.T) {
			a := newStoreCLI(t, srv.URL)
			a.JSON = true
			c := New(a)
			cmd := c.createCmd()
			switch tc.sub {
			case "list":
				cmd = c.listCmd()
			case "get":
				cmd = c.getCmd()
			case "delete":
				cmd = c.deleteCmd()
			}
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			outputschematest.Validate(t, tc.path, out.Bytes())
		})
	}
}
