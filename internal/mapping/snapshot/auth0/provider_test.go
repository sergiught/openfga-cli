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
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// paths returns the distinct request paths, sorted, without the API prefix.
func (r *recorder) paths() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []string

	for _, u := range r.reqs {
		if p := strings.TrimPrefix(u.Path, "/api/v2"); !slices.Contains(out, p) {
			out = append(out, p)
		}
	}

	slices.Sort(out)

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

	// Waits for a rate limit last no time, so tests do not sleep.
	// The pacer's clock jumps ahead by each wait instead.
	var skipped atomic.Int64

	p.limits.now = func() time.Time { return time.Now().Add(time.Duration(skipped.Load())) }
	p.limits.sleep = func(ctx context.Context, d time.Duration) error {
		skipped.Add(int64(d))

		return ctx.Err()
	}

	// The pause between export job polls lasts no time either.
	p.wait = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

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

func TestTypesWindowed(t *testing.T) {
	t.Parallel()

	for _, typ := range Types() {
		if want := typ.Name == "snapshot.user"; typ.Windowed != want {
			t.Errorf("%s Windowed = %v, want %v", typ.Name, typ.Windowed, want)
		}
	}
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
		snapshot.Request{Types: []string{"snapshot.organization.member", "snapshot.organization.member.role"}},
		func(pr snapshot.Progress) { progress = append(progress, pr.Message) })
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.member"}}, nil)
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.connection"}}, nil)
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

	// A page past the first is empty, as Auth0's is.
	p, rec := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if page := r.URL.Query().Get("page"); page != "" && page != "0" {
			writeJSON(w, `{"enabled_connections":[],"start":1,"limit":100,"total":1}`)

			return
		}

		routes(w, r)
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.connection"}}, nil)
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
	// The short first page is the last: no request for an empty second one.
	equal(t, 1, len(reqs))
}

// orgConnsHandler serves one organization whose connections list has total
// connections, pages(page) of them on each page; page numbers start at 0.
func orgConnsHandler(total any, pages func(page int) []int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/organizations" {
			writeJSON(w, `{"organizations":[{"id":"org_1","name":"acme-one"}]}`)

			return
		}

		page := 0
		_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)

		var conns []string

		for _, i := range pages(page) {
			conns = append(conns, fmt.Sprintf(`{"connection_id":"con_%03d","connection":{"name":"c%03d","strategy":"auth0"}}`, i, i))
		}

		totalField := ""
		if total != nil {
			totalField = fmt.Sprintf(`,"total":%v`, total)
		}

		writeJSON(w, fmt.Sprintf(`{"enabled_connections":[%s],"start":%d,"limit":100%s}`,
			strings.Join(conns, ","), page*100, totalField))
	}
}

func span(from, to int) []int {
	var out []int
	for i := from; i < to; i++ {
		out = append(out, i)
	}

	return out
}

func TestSnapshotOrganizationConnectionsFullPageReachingTheTotalIsOneRequest(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, orgConnsHandler(100, func(page int) []int { return span(0, 100) }))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.connection"}}, nil)
	noErr(t, err)
	equal(t, pageSize, len(res.Events))
	equal(t, 1, len(rec.find("/organizations/org_1/enabled_connections")))
}

func TestSnapshotOrganizationConnectionsShortPagesBeforeTheTotalAskForTheNext(t *testing.T) {
	t.Parallel()

	// 120 connections served 50 at a time: a short page is not the last.
	p, rec := newProvider(t, orgConnsHandler(120, func(page int) []int {
		return span(page*50, min(page*50+50, 120))
	}))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.connection"}}, nil)
	noErr(t, err)
	equal(t, 120, len(res.Events))
	equal(t, 3, len(rec.find("/organizations/org_1/enabled_connections")))
}

func TestSnapshotOrganizationConnectionsEmptyPageBeforeTheTotalFails(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, orgConnsHandler(120, func(page int) []int {
		if page == 0 {
			return span(0, 50)
		}

		return nil
	}))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.connection"}}, nil)
	isErr(t, err)
	equal(t, "Auth0 organization connections changed while the list was being read, "+
		"so a connection may have been skipped; run the command again", err.Error())

	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
}

func TestSnapshotOrganizationConnectionsWithoutATotalStopOnTheEmptyPage(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, orgConnsHandler(nil, func(page int) []int {
		switch page {
		case 0:
			return span(0, 100)
		case 1:
			return span(100, 130)
		}

		return nil
	}))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.connection"}}, nil)
	noErr(t, err)
	equal(t, 130, len(res.Events))
	equal(t, 3, len(rec.find("/organizations/org_1/enabled_connections")))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// usersPage serves page of a users list the way Auth0 does: ids picks the
// users on the page, pages that start at or past 1,000 are refused, pages
// past the total are empty and the total reported is never above 1,000.
func usersPage(w http.ResponseWriter, r *http.Request, total int, ids func(page int) []int) {
	page := 0
	_, _ = fmt.Sscan(r.URL.Query().Get("page"), &page)

	if page*pageSize >= maxUsers {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, `{"message":"You can only page through the first 1000 records."}`)

		return
	}

	var users []string

	if page*pageSize < total {
		for _, i := range ids(page) {
			users = append(users, fmt.Sprintf(`{"user_id":"auth0|%04d"}`, i))
		}
	}

	writeJSON(w, fmt.Sprintf(`{"start":%d,"limit":%d,"total":%d,"users":[%s]}`,
		page*pageSize, pageSize, min(total, maxUsers), strings.Join(users, ",")))
}

func usersHandler(total int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		usersPage(w, r, total, func(page int) []int {
			var ids []int

			for i := page * pageSize; i < total && i < (page+1)*pageSize; i++ {
				ids = append(ids, i)
			}

			return ids
		})
	}
}

func TestSnapshotUsersList(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, usersHandler(150))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	if len(res.Events) != 150 {
		t.Errorf("len(res.Events) = %d, want 150", len(res.Events))
	}
	equal(t, "user auth0|0149", res.Events[149]["subject"])
	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 150}}, res.Counts)
}

func TestSnapshotUsersStopAtTotal(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ total, requests int }{{999, 10}, {901, 10}, {900, 9}, {5, 1}} {
		p, rec := newProvider(t, usersHandler(tc.total))

		res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
		noErr(t, err)
		if len(res.Events) != tc.total {
			t.Errorf("total %d: len(res.Events) = %d", tc.total, len(res.Events))
		}

		reqs := rec.find("/users")
		if len(reqs) != tc.requests {
			t.Errorf("total %d: %d list requests, want %d", tc.total, len(reqs), tc.requests)
		}

		for _, q := range reqs {
			equal(t, "user_id:1", q.Get("sort"))
		}
	}
}

func TestSnapshotUsersDeletedDuringRead(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		total := 250
		if r.URL.Query().Get("page") != "0" {
			total = 249
		}

		usersPage(w, r, total, func(page int) []int {
			var ids []int

			for i := page * pageSize; i < total && i < (page+1)*pageSize; i++ {
				ids = append(ids, i)
			}

			return ids
		})
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, "Auth0 users were deleted while the list was being read, so a user may have been skipped; "+
		"run the command again", err.Error())
}

func TestSnapshotUsersDeletedEmptiesLastPage(t *testing.T) {
	t.Parallel()

	// Page 0 reports 101 users; one of its users is then deleted, so the last
	// user moves onto page 0's offsets and page 1 comes back empty.
	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		total := 101
		if r.URL.Query().Get("page") != "0" {
			total = 100
		}

		usersPage(w, r, total, func(page int) []int { return seq(0, 100) })
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	contains(t, err.Error(), "users were deleted while the list was being read")
}

func TestSnapshotUsersGrowingTotalIsRead(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		total := 200
		if r.URL.Query().Get("page") != "0" {
			total = 230
		}

		usersPage(w, r, total, func(page int) []int {
			var ids []int

			for i := page * pageSize; i < total && i < (page+1)*pageSize; i++ {
				ids = append(ids, i)
			}

			return ids
		})
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	if len(res.Events) != 230 {
		t.Errorf("len(res.Events) = %d, want 230", len(res.Events))
	}
}

func TestSnapshotUsersRepeatedUserIsOneEvent(t *testing.T) {
	t.Parallel()

	// A signup before the read of page 1 shifts user 99 onto it as well.
	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		usersPage(w, r, 150, func(page int) []int {
			if page == 0 {
				return seq(0, 100)
			}

			return append([]int{99}, seq(100, 150)...)
		})
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	if len(res.Events) != 150 {
		t.Errorf("len(res.Events) = %d, want 150", len(res.Events))
	}
	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 150}}, res.Counts)
}

func seq(from, to int) []int {
	var out []int

	for i := from; i < to; i++ {
		out = append(out, i)
	}

	return out
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

	var said []string

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, func(pr snapshot.Progress) {
		if pr.Message != "" {
			said = append(said, pr.Message)
		}
	})
	noErr(t, err)
	if len(res.Events) != 2 {
		t.Errorf("len(res.Events) = %d, want 2", len(res.Events))
	}
	equal(t, 1, res.RateLimited)
	contains(t, strings.Join(said, "\n"), "waiting on Auth0's rate limit")
}

func TestSnapshotRateLimitExhausted(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		writeJSON(w, `{"message":"slow down"}`)
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, clierr.KindRateLimited, clierr.Classify(err))
	equal(t, "Auth0 kept rate-limiting the read (10 answers of HTTP 429 in a row for one request); "+
		"wait a minute and run it again", err.Error())
	equal(t, int64(10), hits.Load()) // the SDK does not start over
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.member"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, clierr.KindForbidden, clierr.Classify(err))
	contains(t, err.Error(), "Insufficient scope")
	contains(t, clierr.NewEnvelope(err).Error.Hint, "read:organization_members")
}

func TestSnapshotForbiddenRolesNamesRoleScope(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/members") {
			w.WriteHeader(http.StatusForbidden)
			writeJSON(w, `{"message":"Insufficient scope"}`)

			return
		}

		writeJSON(w, orgsBody)
	})

	_, err := p.Snapshot(context.Background(),
		snapshot.Request{Types: []string{"snapshot.organization.member", "snapshot.organization.member.role"}}, nil)
	isErr(t, err)
	equal(t, clierr.KindForbidden, clierr.Classify(err))
	contains(t, clierr.NewEnvelope(err).Error.Hint, "read:organization_member_roles")
}

func TestSnapshotSeveralTypesCountsInDeclaredOrder(t *testing.T) {
	t.Parallel()

	routes := map[string]string{
		"/connections": `{"connections":[]}`,
		"/users":       `{"start":0,"limit":100,"total":2,"users":[{"user_id":"auth0|1"},{"user_id":"auth0|2"}]}`,
	}
	for k, v := range memberRoutes {
		routes[k] = v
	}

	p, _ := newProvider(t, fixtures(routes))

	// Requested out of order: counts follow the provider's order, and a type
	// with no events is still counted.
	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{
		"snapshot.organization.member", "snapshot.user", "snapshot.connection",
	}}, nil)
	noErr(t, err)
	equal(t, []snapshot.Count{
		{Type: "snapshot.connection", N: 0},
		{Type: "snapshot.user", N: 2},
		{Type: "snapshot.organization.member", N: 3},
	}, res.Counts)
	if len(res.Events) != 5 {
		t.Fatalf("len(res.Events) = %d, want 5", len(res.Events))
	}
	equal(t, "snapshot.user", res.Events[0]["type"])
	equal(t, "snapshot.organization.member", res.Events[2]["type"])
}

// A type no rule asks for costs no request: each type reads its own endpoints,
// plus the organization list when it is read per organization.
func TestSnapshotReadsOnlyTheEndpointsOfTheRequestedTypes(t *testing.T) {
	t.Parallel()

	routes := fixtures(map[string]string{
		"/connections":               `{"connections":[{"id":"con_1","name":"db","strategy":"auth0"}]}`,
		"/connections/con_1/clients": `{"clients":[{"client_id":"c1"}]}`,
		"/organizations":             `{"organizations":[{"id":"org_1","name":"acme-one"}]}`,
		"/organizations/org_1/enabled_connections": `{"enabled_connections":[{"connection_id":"con_1",
			"connection":{"name":"db","strategy":"auth0"}}],"start":0,"limit":100,"total":1}`,
		"/organizations/org_1/members": `{"members":[{"user_id":"auth0|u1","roles":[{"id":"rol_1","name":"admin"}]}]}`,
	})
	handler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/users" {
			usersHandler(2)(w, r)

			return
		}

		routes(w, r)
	}

	orgs, members := "/organizations", "/organizations/org_1/members"
	want := map[string][]string{
		"snapshot.connection":               {"/connections", "/connections/con_1/clients"},
		"snapshot.user":                     {"/users"},
		"snapshot.organization":             {orgs},
		"snapshot.organization.connection":  {orgs, "/organizations/org_1/enabled_connections"},
		"snapshot.organization.member":      {orgs, members},
		"snapshot.organization.member.role": {orgs, members},
	}

	for _, typ := range Types() {
		t.Run(typ.Name, func(t *testing.T) {
			t.Parallel()

			p, rec := newProvider(t, handler)

			res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{typ.Name}}, nil)
			noErr(t, err)
			requireNotEmpty(t, res.Events)
			equal(t, want[typ.Name], rec.paths())
		})
	}
}

func TestSnapshotMembersWithoutOrganizations(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, fixtures(memberRoutes))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.member.role"}}, nil)
	noErr(t, err)
	equal(t, []snapshot.Count{{Type: "snapshot.organization.member.role", N: 2}}, res.Counts)

	for _, ev := range res.Events {
		equal(t, "snapshot.organization.member.role", ev["type"])
	}
}

func TestSnapshotUnauthorized(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, `{"message":"bad token"}`)
	})

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization.member"}}, nil)
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

	res, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.organization.member"}}, nil)
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

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.group"}}, nil)
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

	_, err = p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
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

	_, err = p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
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

	res, err := p.Snapshot(ctx, snapshot.Request{Types: names}, nil)
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
				t.Errorf("connection event %v has no enabled_clients", ev["subject"])
			}
		}
	}
}

func TestSnapshotUsersSinceSearchesByUpdatedAt(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, usersHandler(3))
	since := time.Date(2026, 11, 24, 9, 0, 0, 123456789, time.FixedZone("x", 3600))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}, Since: since}, nil)
	noErr(t, err)

	reqs := rec.find("/users")
	if len(reqs) == 0 {
		t.Fatal("no /users request")
	}

	for _, q := range reqs {
		equal(t, "updated_at:[2026-11-24T08:00:00.123Z TO *]", q.Get("q"))
		equal(t, "v3", q.Get("search_engine"))
		equal(t, "user_id:1", q.Get("sort"))
	}

	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 3, Windowed: true}}, res.Counts)
}

func TestSnapshotUsersWithoutSinceSendsNoQuery(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, usersHandler(3))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)

	for _, q := range rec.find("/users") {
		if q.Has("q") || q.Has("search_engine") {
			t.Errorf("query = %v, want no q or search_engine", q)
		}
	}

	equal(t, []snapshot.Count{{Type: "snapshot.user", N: 3}}, res.Counts)
}

func TestSnapshotUsersSinceAboveTheSearchLimitFails(t *testing.T) {
	t.Parallel()

	// Auth0 reports 1,000 for a search that matches that many users or more.
	p, rec := newProvider(t, usersHandler(1205))
	since := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}, Since: since}, nil)
	isErr(t, err)

	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}

	equal(t, "Auth0's user search returns at most 1,000 users and at least that many changed since 2026-11-24T09:00:00Z; "+
		"use a later --since or run without it", err.Error())

	if slices.Contains(rec.paths(), "/jobs/users-exports") {
		t.Errorf("paths = %v, want no export job", rec.paths())
	}
}

func TestSnapshotSinceLeavesOtherTypesWhole(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, fixtures(map[string]string{"/organizations": orgsBody}))
	since := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}, Since: since}, nil)
	noErr(t, err)

	reqs := rec.find("/organizations")
	if len(reqs) == 0 {
		t.Fatal("no /organizations request")
	}

	for _, q := range reqs {
		if q.Has("q") {
			t.Errorf("query = %v, want no q", q)
		}
	}

	equal(t, []snapshot.Count{{Type: "snapshot.organization", N: 2}}, res.Counts)
}

func TestSnapshotOrganizationsFollowAnEmptyPageWithACursor(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("from") {
		case "":
			writeJSON(w, `{"organizations":[],"next":"c1"}`)
		case "c1":
			writeJSON(w, `{"organizations":[{"id":"org_1","name":"acme-one"}]}`)
		default:
			http.Error(w, "unexpected cursor", http.StatusNotFound)
		}
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
	noErr(t, err)
	equal(t, []snapshot.Count{{Type: "snapshot.organization", N: 1}}, res.Counts)
}

func TestSnapshotEmptyStringCursorEndsTheList(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"organizations":[{"id":"org_1","name":"acme-one"}],"next":""}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
	noErr(t, err)
	equal(t, []snapshot.Count{{Type: "snapshot.organization", N: 1}}, res.Counts)
	equal(t, 1, len(rec.find("/organizations")))
}

func TestSnapshotRepeatedCursorFails(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"organizations":[{"id":"org_1","name":"acme-one"}],"next":"c1"}`)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	contains(t, err.Error(), "same page of organizations twice")
	if n := len(rec.find("/organizations")); n > 2 {
		t.Errorf("%d requests to /organizations, want at most 2", n)
	}
}

func TestSnapshotUsersWithoutATotalFails(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		users := make([]string, pageSize)
		for i := range users {
			users[i] = fmt.Sprintf(`{"user_id":"auth0|%04d"}`, i)
		}

		writeJSON(w, fmt.Sprintf(`{"start":0,"limit":%d,"users":[%s]}`, pageSize, strings.Join(users, ",")))
	})

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	equal(t, "Auth0's user list reported no total, so the read cannot tell whether it is complete; "+
		"run the command again", err.Error())
	equal(t, 1, len(rec.find("/users")))
}

func TestSnapshotCursorCycleFails(t *testing.T) {
	t.Parallel()

	next := map[string]string{"": "c1", "c1": "c2", "c2": "c1"}
	p, rec := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, fmt.Sprintf(`{"organizations":[{"id":"org_1","name":"acme-one"}],"next":%q}`,
			next[r.URL.Query().Get("from")]))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.organization"}}, nil)
	isErr(t, err)
	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
	contains(t, err.Error(), "same page of organizations twice")
	if n := len(rec.find("/organizations")); n > 3 {
		t.Errorf("%d requests to /organizations, want at most 3", n)
	}
}
