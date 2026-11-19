package auth0

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auth0/go-auth0/v2/management/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	require.NoError(t, err)

	return p, rec
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

		assert.NotEmpty(t, typ.Mirrors, typ.Name)
		assert.NotEmpty(t, typ.Description, typ.Name)
		assert.NotEmpty(t, typ.Scopes, typ.Name)
	}

	assert.Equal(t, []string{
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
	require.NoError(t, err)

	assert.Equal(t, []snapshot.Count{
		{Type: "snapshot.organization.member", N: 3},
		{Type: "snapshot.organization.member.role", N: 2},
	}, res.Counts)
	require.Len(t, res.Events, 5)
	assert.NotEmpty(t, progress)

	ev := res.Events[0]
	assert.Equal(t, map[string]any{
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

	assert.Equal(t, "organization org_1 member auth0|u2", res.Events[1]["subject"])
	assert.Equal(t, "organization org_1 member auth0|u3", res.Events[2]["subject"])
	assert.Equal(t, "snap_000003", res.Events[2]["id"])

	role := res.Events[3]
	assert.Equal(t, "snapshot.organization.member.role", role["type"])
	assert.Equal(t, "snap_000004", role["id"])
	assert.Equal(t, "organization org_1 member auth0|u1 role rol_1", role["subject"])
	assert.Equal(t, map[string]any{
		"organization": map[string]any{"id": "org_1", "name": "acme-one"},
		"user":         map[string]any{"user_id": "auth0|u1"},
		"role":         map[string]any{"id": "rol_1", "name": "admin"},
	}, role["data"].(map[string]any)["object"])
	assert.Equal(t, "organization org_1 member auth0|u1 role rol_2", res.Events[4]["subject"])

	for _, q := range rec.find("/organizations/org_1/members") {
		assert.Equal(t, "user_id,email,name,picture,roles", q.Get("fields"))
		assert.Equal(t, "true", q.Get("include_fields"))
	}

	assert.NotEmpty(t, rec.find("/organizations"))
}

func TestSnapshotMembersOnlyOmitsFields(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, fixtures(memberRoutes))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization.member"}, nil)
	require.NoError(t, err)
	assert.Len(t, res.Events, 3)

	reqs := rec.find("/organizations/org_1/members")
	require.NotEmpty(t, reqs)

	for _, q := range reqs {
		assert.False(t, q.Has("fields"))
	}
}

func TestSnapshotKeepsUnknownFields(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, fixtures(map[string]string{
		"/organizations": `{"organizations":[{"id":"org_1","name":"acme-one","x_new_field":{"a":1}}]}`,
	}))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)

	obj := res.Events[0]["data"].(map[string]any)["object"].(map[string]any)
	assert.Equal(t, map[string]any{"a": float64(1)}, obj["x_new_field"])
	assert.Equal(t, "organization org_1", res.Events[0]["subject"])
}

func TestSnapshotConnections(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, fixtures(map[string]string{
		"/connections":               `{"connections":[{"id":"con_1","name":"db","strategy":"auth0"},{"id":"con_2","name":"g","strategy":"google-oauth2"}]}`,
		"/connections/con_1/clients": `{"clients":[{"client_id":"c1"},{"client_id":"c2"}]}`,
		"/connections/con_2/clients": `{"clients":[]}`,
	}))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.connection"}, nil)
	require.NoError(t, err)
	require.Len(t, res.Events, 2)

	first := res.Events[0]["data"].(map[string]any)["object"].(map[string]any)
	assert.Equal(t, "con_1", first["id"])
	assert.Equal(t, []any{"c1", "c2"}, first["enabled_clients"])
	assert.Equal(t, "connection con_1", res.Events[0]["subject"])

	second := res.Events[1]["data"].(map[string]any)["object"].(map[string]any)
	assert.Equal(t, []any{}, second["enabled_clients"])
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
	require.NoError(t, err)
	require.Len(t, res.Events, 1)

	assert.Equal(t, "organization org_1 connection con_1", res.Events[0]["subject"])
	assert.Equal(t, map[string]any{
		"organization":               map[string]any{"id": "org_1", "name": "acme-one"},
		"connection":                 map[string]any{"id": "con_1", "name": "db", "strategy": "auth0"},
		"is_enabled":                 true,
		"assign_membership_on_login": false,
		"show_as_button":             true,
		"is_signup_enabled":          false,
	}, res.Events[0]["data"].(map[string]any)["object"])

	reqs := rec.find("/organizations/org_1/enabled_connections")
	require.NotEmpty(t, reqs)
	assert.Equal(t, "true", reqs[0].Get("include_totals"))
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
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "at most 1,000 users")
	assert.Contains(t, err.Error(), "1001")

	p, _ = newProvider(t, usersHandler(150))

	res, err = p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	require.NoError(t, err)
	assert.Len(t, res.Events, 150)
	assert.Equal(t, "user auth0|149", res.Events[149]["subject"])
	assert.Equal(t, []snapshot.Count{{Type: "snapshot.user", N: 150}}, res.Counts)
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
	require.NoError(t, err)
	assert.Len(t, res.Events, 2)
}

func TestSnapshotRateLimitExhausted(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, `{"message":"slow down"}`)
	}, option.WithMaxAttempts(1))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Equal(t, clierr.KindRateLimited, clierr.Classify(err))
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
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Equal(t, clierr.KindForbidden, clierr.Classify(err))
	assert.Contains(t, err.Error(), "Insufficient scope")
	assert.Contains(t, clierr.NewEnvelope(err).Error.Hint, "read:organization_members")
}

func TestSnapshotUnauthorized(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, `{"message":"bad token"}`)
	})

	_, err := p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	require.Error(t, err)
	assert.Equal(t, clierr.KindUnauthenticated, clierr.Classify(err))
	assert.Contains(t, err.Error(), "Auth0 rejected the credentials: bad token")
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
	require.Error(t, err)
	assert.Nil(t, res)
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
	require.Error(t, err)
	assert.Nil(t, res)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestSnapshotUnknownTypeFails(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, fixtures(nil))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.group"}, nil)
	require.Error(t, err)
	assert.Nil(t, res)
	assert.Contains(t, err.Error(), "snapshot.group")
	assert.Zero(t, rec.count())
}

func TestNewNormalisesDomain(t *testing.T) {
	t.Parallel()

	p, err := New(context.Background(), Config{Domain: "https://acme.us.auth0.com/", ClientID: "id", ClientSecret: "s"})
	require.NoError(t, err)

	assert.Equal(t, "auth0", p.Name())
	assert.Equal(t, "Auth0", p.Label())
	assert.Equal(t, Types(), p.Types())
	assert.Equal(t, "acme.us.auth0.com", p.source)
	assert.Equal(t, "acme", p.tenant)
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
	require.NoError(t, err)

	_, err = p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), secret)
	assert.Equal(t, clierr.KindUnauthenticated, clierr.Classify(err))
}

func TestSnapshotNetworkFailure(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.Listener.Addr().String()
	srv.Close()

	p, err := New(context.Background(), Config{Domain: addr, Tenant: "acme"}, option.WithInsecure(), option.WithMaxAttempts(1))
	require.NoError(t, err)

	_, err = p.Snapshot(context.Background(), []string{"snapshot.organization"}, nil)
	require.Error(t, err)
	assert.Equal(t, clierr.KindNetwork, clierr.Classify(err))
	assert.Equal(t, clierr.CodeNetwork, clierr.Code(err))
}

func TestLiveSnapshot(t *testing.T) {
	domain, id, secret := os.Getenv("AUTH0_DOMAIN"), os.Getenv("AUTH0_CLIENT_ID"), os.Getenv("AUTH0_CLIENT_SECRET")
	if domain == "" || id == "" || secret == "" {
		t.Skip("AUTH0_DOMAIN, AUTH0_CLIENT_ID and AUTH0_CLIENT_SECRET are not all set")
	}

	ctx := context.Background()

	p, err := New(ctx, Config{Domain: domain, ClientID: id, ClientSecret: secret})
	require.NoError(t, err)

	var names []string
	for _, typ := range Types() {
		names = append(names, typ.Name)
	}

	res, err := p.Snapshot(ctx, names, nil)
	require.NoError(t, err)

	counts := map[string]int{}

	for _, c := range res.Counts {
		counts[c.Type] = c.N
		t.Logf("%s: %d", c.Type, c.N)
	}

	assert.GreaterOrEqual(t, counts[typeOrganization], 2)
	assert.GreaterOrEqual(t, counts[typeOrgMember], 4)
	assert.GreaterOrEqual(t, counts[typeOrgMemberRole], 4)

	for _, ev := range res.Events {
		assert.NotEmpty(t, ev["type"])
		assert.NotEmpty(t, ev["subject"])

		data, _ := ev["data"].(map[string]any)
		obj, ok := data["object"].(map[string]any)
		require.True(t, ok, "event %v has no data.object", ev["type"])

		if ev["type"] == typeConnection {
			assert.Contains(t, obj, "enabled_clients")
		}
	}
}
