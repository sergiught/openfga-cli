package auth0

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// recipeSample reads the snapshot sample the built-in recipes ship for typ.
func recipeSample(t *testing.T, typ string) map[string]any {
	t.Helper()

	raw, err := os.ReadFile("../../auth0/samples/" + typ + ".json")
	noErr(t, err)

	var ev map[string]any
	noErr(t, json.Unmarshal(raw, &ev))

	return ev
}

func sampleObject(ev map[string]any) map[string]any {
	return ev["data"].(map[string]any)["object"].(map[string]any)
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	noErr(t, err)

	return string(b)
}

func without(m map[string]any, keys ...string) map[string]any {
	out := map[string]any{}

	for k, v := range m {
		out[k] = v
	}

	for _, k := range keys {
		delete(out, k)
	}

	return out
}

// TestRecipeSamplesMatchWhatProviderEmits feeds each shipped snapshot sample's
// payload to the provider as the Auth0 API answer and requires the event it
// builds to equal the sample, so a sample can neither lack a field the provider
// emits nor carry one it does not.
func TestRecipeSamplesMatchWhatProviderEmits(t *testing.T) {
	t.Parallel()

	conn := sampleObject(recipeSample(t, typeConnection))
	user := sampleObject(recipeSample(t, typeUser))
	org := sampleObject(recipeSample(t, typeOrganization))
	orgConn := sampleObject(recipeSample(t, typeOrgConnection))
	member := sampleObject(recipeSample(t, typeOrgMember))
	role := sampleObject(recipeSample(t, typeOrgMemberRole))

	orgID := org["id"].(string)
	connID := conn["id"].(string)
	memberUser := member["user"].(map[string]any)

	var clients []string
	for _, c := range conn["enabled_clients"].([]any) {
		clients = append(clients, `{"client_id":"`+c.(string)+`"}`)
	}

	rawOrgConn := without(orgConn, "organization", "connection", "is_enabled")
	rawOrgConn["connection_id"] = connID
	rawOrgConn["connection"] = without(orgConn["connection"].(map[string]any), "id")

	apiMember := without(memberUser)
	apiMember["roles"] = []any{role["role"]}

	routes := fixtures(map[string]string{
		"/connections":                         `{"connections":[` + jsonOf(t, without(conn, "enabled_clients")) + `]}`,
		"/connections/" + connID + "/clients":  `{"clients":[` + strings.Join(clients, ",") + `]}`,
		"/organizations":                       `{"organizations":[` + jsonOf(t, org) + `]}`,
		"/organizations/" + orgID + "/members": `{"members":[` + jsonOf(t, apiMember) + `]}`,
	})

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/users"):
			if page := r.URL.Query().Get("page"); page != "" && page != "0" {
				writeJSON(w, `{"start":1,"limit":100,"total":1,"users":[]}`)

				return
			}

			writeJSON(w, `{"start":0,"limit":100,"total":1,"users":[`+jsonOf(t, user)+`]}`)
		case strings.HasSuffix(r.URL.Path, "/enabled_connections"):
			if page := r.URL.Query().Get("page"); page != "" && page != "0" {
				writeJSON(w, `{"enabled_connections":[],"start":1,"limit":100,"total":1}`)

				return
			}

			writeJSON(w, `{"enabled_connections":[`+jsonOf(t, rawOrgConn)+`],"start":0,"limit":100,"total":1}`)
		default:
			routes(w, r)
		}
	})

	types := make([]string, 0, len(Types()))
	for _, typ := range Types() {
		types = append(types, typ.Name)
	}

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: types}, nil)
	noErr(t, err)

	got := map[string]map[string]any{}
	for _, ev := range res.Events {
		got[ev["type"].(string)] = ev
	}

	if len(got) != len(types) {
		t.Fatalf("provider emitted %d types, want %d", len(got), len(types))
	}

	for _, typ := range types {
		t.Run(typ, func(t *testing.T) {
			sample, ev := recipeSample(t, typ), got[typ]

			equal(t, jsonKeys(sample), jsonKeys(ev))
			equal(t, jsonKeys(sample["data"].(map[string]any)), jsonKeys(ev["data"].(map[string]any)))
			equal(t, sample["subject"], ev["subject"])
			equal(t, "snap_000001", sample["id"])
			equal(t, sampleObject(sample), sampleObject(ev))
		})
	}
}

func jsonKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	slices.Sort(out)

	return out
}
