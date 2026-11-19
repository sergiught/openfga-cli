package auth0

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auth0/go-auth0/v2/management/option"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

type recorder struct {
	mu   sync.Mutex
	reqs []*url.URL
}

func (r *recorder) add(u *url.URL) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.reqs = append(r.reqs, u)
}

func (r *recorder) find(path string) []url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []url.Values

	for _, u := range r.reqs {
		if u.Path == "/api/v2"+path {
			out = append(out, u.Query())
		}
	}

	return out
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.reqs)
}

// newProvider serves h as an Auth0 tenant and returns a provider reading it.
func newProvider(t *testing.T, h http.HandlerFunc, opts ...option.RequestOption) (*Provider, *recorder) {
	t.Helper()

	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.add(r.URL)
		h(w, r)
	}))
	t.Cleanup(srv.Close)

	opts = append([]option.RequestOption{option.WithInsecure()}, opts...)
	p, err := New(context.Background(), Config{Domain: srv.Listener.Addr().String(), Tenant: "acme"}, opts...)
	noErr(t, err)

	return p, rec
}

func noErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func isErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("error = nil, want an error")
	}
}

func equal(t *testing.T, want, got any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Map, reflect.String:
		return rv.Len() == 0
	default:
		return rv.IsZero()
	}
}

func notEmpty(t *testing.T, v any, msg ...any) {
	t.Helper()

	if isEmpty(v) {
		t.Errorf("got empty %v, want non-empty", msg)
	}
}

func requireNotEmpty(t *testing.T, v any) {
	t.Helper()

	if isEmpty(v) {
		t.Fatalf("got %+v, want non-empty", v)
	}
}

func contains(t *testing.T, s, sub string) {
	t.Helper()

	if !strings.Contains(s, sub) {
		t.Errorf("%q does not contain %q", s, sub)
	}
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func fixtures(routes map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/api/v2")
		if from := r.URL.Query().Get("from"); from != "" {
			key += "?from=" + from
		}

		body, ok := routes[key]
		if !ok {
			http.Error(w, "no fixture for "+key, http.StatusNotFound)

			return
		}

		writeJSON(w, body)
	}
}

func pinNow(t *testing.T) time.Time {
	t.Helper()

	pinned := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	prev := now
	now = func() time.Time { return pinned }

	t.Cleanup(func() { now = prev })

	return pinned
}

func TestTypes(t *testing.T) {
	t.Parallel()

	var names []string

	for _, typ := range Types() {
		names = append(names, typ.Name)

		notEmpty(t, typ.Mirrors, typ.Name)
		notEmpty(t, typ.Description, typ.Name)
		notEmpty(t, typ.Scopes, typ.Name)
	}

	equal(t, []string{
		"snapshot.connection", "snapshot.user", "snapshot.organization",
		"snapshot.organization.connection", "snapshot.organization.member",
		"snapshot.organization.member.role",
	}, names)
}

const orgsBody = `{"organizations":[{"id":"org_1","name":"acme-one"},{"id":"org_2","name":"acme-two"}]}`

var memberRoutes = map[string]string{
	"/organizations": orgsBody,
	"/organizations/org_1/members": `{"members":[
		{"user_id":"auth0|u1","email":"a@x.io","name":"A","picture":"p","roles":[{"id":"rol_1","name":"admin"},{"id":"rol_2","name":"dev"}]},
		{"user_id":"auth0|u2","email":"b@x.io","name":"B","picture":"p"}],"next":"tok"}`,
	"/organizations/org_1/members?from=tok": `{"members":[{"user_id":"auth0|u3","email":"c@x.io","name":"C","picture":"p"}]}`,
	"/organizations/org_2/members":          `{"members":[]}`,
}

// Not parallel: it pins the package clock.
func TestSnapshotMembersAndRoles(t *testing.T) {
	p, rec := newProvider(t, fixtures(memberRoutes))
	pinned := pinNow(t)

	var progress []string

	res, err := p.Snapshot(context.Background(),
		[]string{"snapshot.organization.member", "snapshot.organization.member.role"},
		func(msg string) { progress = append(progress, msg) })
	noErr(t, err)

	equal(t, []snapshot.Count{
		{Type: "snapshot.organization.member", N: 3},
		{Type: "snapshot.organization.member.role", N: 2},
	}, res.Counts)
	if len(res.Events) != 5 {
		t.Fatalf("len(res.Events) = %d, want 5", len(res.Events))
	}
	notEmpty(t, progress)

	ev := res.Events[0]
	equal(t, map[string]any{
		"specversion": "1.0",
		"type":        "snapshot.organization.member",
		"source":      "urn:auth0:" + res.Source,
		"id":          "snap_000001",
		"time":        pinned.Format(time.RFC3339),
		"a0tenant":    "acme",
		"subject":     "organization org_1 member auth0|u1",
		"data": map[string]any{
			"object": map[string]any{
				"organization": map[string]any{"id": "org_1", "name": "acme-one"},
				"user": map[string]any{
					"user_id": "auth0|u1", "email": "a@x.io", "name": "A", "picture": "p",
				},
			},
			"context": map[string]any{"tenant": map[string]any{"id": "acme"}},
		},
	}, ev)

	equal(t, "organization org_1 member auth0|u2", res.Events[1]["subject"])
	equal(t, "organization org_1 member auth0|u3", res.Events[2]["subject"])
	equal(t, "snap_000003", res.Events[2]["id"])

	role := res.Events[3]
	equal(t, "snapshot.organization.member.role", role["type"])
	equal(t, "snap_000004", role["id"])
	equal(t, "organization org_1 member auth0|u1 role rol_1", role["subject"])
	equal(t, map[string]any{
		"organization": map[string]any{"id": "org_1", "name": "acme-one"},
		"user":         map[string]any{"user_id": "auth0|u1"},
		"role":         map[string]any{"id": "rol_1", "name": "admin"},
	}, role["data"].(map[string]any)["object"])
	equal(t, "organization org_1 member auth0|u1 role rol_2", res.Events[4]["subject"])

	for _, q := range rec.find("/organizations/org_1/members") {
		equal(t, "user_id,email,name,picture,roles", q.Get("fields"))
		equal(t, "true", q.Get("include_fields"))
	}

	notEmpty(t, rec.find("/organizations"))
}

func TestSnapshotMembersOnlyOmitsFields(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, fixtures(memberRoutes))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.member"}, nil)
	noErr(t, err)
	if len(res.Events) != 3 {
		t.Errorf("len(res.Events) = %d, want 3", len(res.Events))
	}

	reqs := rec.find("/organizations/org_1/members")
	requireNotEmpty(t, reqs)

	for _, q := range reqs {
		if q.Has("fields") {
			t.Errorf("request %v sets fields, want none", q)
		}
	}
}

func TestSnapshotKeepsUnknownFields(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, fixtures(map[string]string{
		"/organizations": `{"organizations":[{"id":"org_1","name":"acme-one","x_new_field":{"a":1}}]}`,
	}))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	noErr(t, err)
	if len(res.Events) != 1 {
		t.Fatalf("len(res.Events) = %d, want 1", len(res.Events))
	}

	obj := res.Events[0]["data"].(map[string]any)["object"].(map[string]any)
	equal(t, map[string]any{"a": float64(1)}, obj["x_new_field"])
	equal(t, "organization org_1", res.Events[0]["subject"])
}

func TestSnapshotConnections(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, fixtures(map[string]string{
		"/connections":               `{"connections":[{"id":"con_1","name":"db","strategy":"auth0"},{"id":"con_2","name":"g","strategy":"google-oauth2"}]}`,
		"/connections/con_1/clients": `{"clients":[{"client_id":"c1"},{"client_id":"c2"}]}`,
		"/connections/con_2/clients": `{"clients":[]}`,
	}))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.connection"}, nil)
	noErr(t, err)
	if len(res.Events) != 2 {
		t.Fatalf("len(res.Events) = %d, want 2", len(res.Events))
	}

	first := res.Events[0]["data"].(map[string]any)["object"].(map[string]any)
	equal(t, "con_1", first["id"])
	equal(t, []any{"c1", "c2"}, first["enabled_clients"])
	equal(t, "connection con_1", res.Events[0]["subject"])

	second := res.Events[1]["data"].(map[string]any)["object"].(map[string]any)
	equal(t, []any{}, second["enabled_clients"])
}

func TestSnapshotOrganizationConnections(t *testing.T) {
	t.Parallel()

	routes := fixtures(map[string]string{
		"/organizations": `{"organizations":[{"id":"org_1","name":"acme-one"}]}`,
		"/organizations/org_1/enabled_connections": `{"enabled_connections":[{
			"connection_id":"con_1","assign_membership_on_login":false,"show_as_button":true,
			"is_signup_enabled":false,"connection":{"name":"db","strategy":"auth0"}}],
			"start":0,"limit":100,"total":1}`,
	})

	// Offset paging ends on an empty page, as Auth0's does.
	p, rec := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if page := r.URL.Query().Get("page"); page != "" && page != "0" {
			writeJSON(w, `{"enabled_connections":[],"start":1,"limit":100,"total":1}`)

			return
		}

		routes(w, r)
	})

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.connection"}, nil)
	noErr(t, err)
	if len(res.Events) != 1 {
		t.Fatalf("len(res.Events) = %d, want 1", len(res.Events))
	}

	equal(t, "organization org_1 connection con_1", res.Events[0]["subject"])
	equal(t, map[string]any{
		"organization":               map[string]any{"id": "org_1", "name": "acme-one"},
		"connection":                 map[string]any{"id": "con_1", "name": "db", "strategy": "auth0"},
		"is_enabled":                 true,
		"assign_membership_on_login": false,
		"show_as_button":             true,
		"is_signup_enabled":          false,
	}, res.Events[0]["data"].(map[string]any)["object"])

	reqs := rec.find("/organizations/org_1/enabled_connections")
	requireNotEmpty(t, reqs)
	equal(t, "true", reqs[0].Get("include_totals"))
}

func usersHandler(total int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := 0
		_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)

		var users []string

		for i := page * pageSize; i < total && i < (page+1)*pageSize; i++ {
			users = append(users, fmt.Sprintf(`{"user_id":"auth0|%d"}`, i))
		}

		writeJSON(w, fmt.Sprintf(`{"start":%d,"limit":%d,"total":%d,"users":[%s]}`,
			page*pageSize, pageSize, total, strings.Join(users, ",")))
	}
}

func TestSnapshotUsersCap(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, usersHandler(1001))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	contains(t, err.Error(), "at most 1,000 users")
	contains(t, err.Error(), "1001")

	p, _ = newProvider(t, usersHandler(150))

	res, err = p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	noErr(t, err)
	if len(res.Events) != 150 {
		t.Errorf("len(res.Events) = %d, want 150", len(res.Events))
	}
	equal(t, "user auth0|149", res.Events[149]["subject"])
	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 150}}, res.Counts)
}

func TestSnapshotRetriesRateLimit(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex

	calls := 0

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		if n == 1 {
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(time.Now().Add(time.Second).Unix()))
			w.WriteHeader(http.StatusTooManyRequests)

			return
		}

		writeJSON(w, orgsBody)
	})

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	noErr(t, err)
	if len(res.Events) != 2 {
		t.Errorf("len(res.Events) = %d, want 2", len(res.Events))
	}
}

func TestSnapshotRateLimitExhausted(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, `{"message":"slow down"}`)
	}, option.WithMaxAttempts(1))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, clierr.KindRateLimited, clierr.Classify(err))
}

func TestSnapshotForbiddenNamesScope(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/members") {
			w.WriteHeader(http.StatusForbidden)
			writeJSON(w, `{"message":"Insufficient scope"}`)

			return
		}

		writeJSON(w, orgsBody)
	})

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.member"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, clierr.KindForbidden, clierr.Classify(err))
	contains(t, err.Error(), "Insufficient scope")
	contains(t, clierr.NewEnvelope(err).Error.Hint, "read:organization_members")
}

func TestSnapshotUnauthorized(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, `{"message":"bad token"}`)
	})

	_, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	isErr(t, err)
	equal(t, clierr.KindUnauthenticated, clierr.Classify(err))
	contains(t, err.Error(), "Auth0 rejected the credentials: bad token")
}

func TestSnapshotFailsWhole(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/organizations":
			writeJSON(w, orgsBody)
		case r.URL.Query().Get("from") == "tok":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			writeJSON(w, memberRoutes["/organizations/org_1/members"])
		}
	}, option.WithMaxAttempts(1))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.member"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
}

func TestSnapshotCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex

	calls := 0

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()

		if n == 2 {
			cancel()
		}

		if r.URL.Path == "/api/v2/organizations" {
			writeJSON(w, orgsBody)

			return
		}

		writeJSON(w, memberRoutes["/organizations/org_1/members"])
	})

	res, err := p.Snapshot(ctx, []string{"snapshot.organization.member"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestSnapshotUnknownTypeFails(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, fixtures(nil))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.group"}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	contains(t, err.Error(), "snapshot.group")
	if n := rec.count(); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

func TestNewNormalisesDomain(t *testing.T) {
	t.Parallel()

	p, err := New(context.Background(), Config{Domain: "https://acme.us.auth0.com/", ClientID: "id", ClientSecret: "s"})
	noErr(t, err)

	equal(t, "auth0", p.Name())
	equal(t, "Auth0", p.Label())
	equal(t, Types(), p.Types())
	equal(t, "acme.us.auth0.com", p.source)
	equal(t, "acme", p.tenant)
}

func TestErrorsCarryNoSecret(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-value"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, `{"error":"access_denied","error_description":"Unauthorized"}`)
	}))
	t.Cleanup(srv.Close)

	p, err := New(context.Background(), Config{
		Domain: srv.Listener.Addr().String(), ClientID: "id", ClientSecret: secret,
	}, option.WithInsecure())
	noErr(t, err)

	_, err = p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	isErr(t, err)
	if strings.Contains(err.Error(), secret) {
		t.Error("error contains the client secret")
	}
	equal(t, clierr.KindUnauthenticated, clierr.Classify(err))
}

func TestSnapshotNetworkFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()

	p, err := New(context.Background(), Config{Domain: addr, Tenant: "acme"}, option.WithInsecure(), option.WithMaxAttempts(1))
	noErr(t, err)

	_, err = p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	isErr(t, err)
	equal(t, clierr.KindNetwork, clierr.Classify(err))
	equal(t, clierr.CodeNetwork, clierr.Code(err))
}

func TestLiveSnapshot(t *testing.T) {
	domain, id, secret := os.Getenv("AUTH0_DOMAIN"), os.Getenv("AUTH0_CLIENT_ID"), os.Getenv("AUTH0_CLIENT_SECRET")
	if domain == "" || id == "" || secret == "" {
		t.Skip("AUTH0_DOMAIN, AUTH0_CLIENT_ID and AUTH0_CLIENT_SECRET are not all set")
	}

	ctx := context.Background()

	p, err := New(ctx, Config{Domain: domain, ClientID: id, ClientSecret: secret})
	noErr(t, err)

	var names []string
	for _, typ := range Types() {
		names = append(names, typ.Name)
	}

	res, err := p.Snapshot(ctx, names, nil)
	noErr(t, err)

	counts := map[string]int{}

	for _, c := range res.Counts {
		counts[c.Type] = c.N
		t.Logf("%s: %d", c.Type, c.N)
	}

	if counts[typeOrganization] < 2 {
		t.Errorf("counts[%s] = %d, want >= 2", typeOrganization, counts[typeOrganization])
	}
	if counts[typeOrgMember] < 4 {
		t.Errorf("counts[%s] = %d, want >= 4", typeOrgMember, counts[typeOrgMember])
	}
	if counts[typeOrgMemberRole] < 4 {
		t.Errorf("counts[%s] = %d, want >= 4", typeOrgMemberRole, counts[typeOrgMemberRole])
	}

	for _, ev := range res.Events {
		notEmpty(t, ev["type"])
		notEmpty(t, ev["subject"])

		data, _ := ev["data"].(map[string]any)
		obj, ok := data["object"].(map[string]any)
		if !ok {
			t.Fatalf("event %v has no data.object", ev["type"])
		}

		if ev["type"] == typeConnection {
			if _, ok := obj["enabled_clients"]; !ok {
				t.Errorf("connection event %v has no enabled_clients", obj)
			}
		}
	}
}
