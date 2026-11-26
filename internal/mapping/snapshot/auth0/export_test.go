package auth0

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/auth0/go-auth0/v2/management/option"

	"github.com/sergiught/openfga-cli/internal/clierr"
	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// exportFake is a tenant whose users list reports total users and whose export
// job goes through statuses, one per poll (the last repeats). The export file
// is served by a second server, the way Auth0 serves it from S3.
type exportFake struct {
	statuses []string // JSON bodies of the job, one per poll
	file     func(w http.ResponseWriter, r *http.Request)
	users    http.HandlerFunc // the users list; the default reports the total given to newExportProvider

	mu        sync.Mutex
	createdBy []string // request bodies of the job creations
	polls     int
	downloads atomic.Int64
	fileAuth  atomic.Value // the Authorization header of the last download
	apiAuth   atomic.Value // the Authorization header of the last job request
}

const exportQuery = "?X-Amz-Signature=secret123"

func newExportProvider(t *testing.T, total int, f *exportFake, opts ...option.RequestOption) (*Provider, *recorder) {
	t.Helper()

	dl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.downloads.Add(1)
		f.fileAuth.Store(r.Header.Get("Authorization"))
		f.file(w, r)
	}))
	t.Cleanup(dl.Close)

	location := dl.URL + "/export.gz" + exportQuery

	if len(f.statuses) == 0 {
		f.statuses = []string{`{"status":"completed","type":"users_export","id":"job_1","location":"` + location + `"}`}
	}

	for i := range f.statuses {
		f.statuses[i] = strings.ReplaceAll(f.statuses[i], "LOCATION", location)
	}

	opts = append([]option.RequestOption{option.WithToken("mgmt-token")}, opts...)

	return newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/users" && f.users != nil:
			f.users(w, r)
		case r.URL.Path == "/api/v2/users":
			usersPage(w, r, total, func(page int) []int { return []int{page} })
		case r.URL.Path == "/api/v2/jobs/users-exports" && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)

			f.mu.Lock()
			f.createdBy = append(f.createdBy, string(body))
			f.mu.Unlock()
			f.apiAuth.Store(r.Header.Get("Authorization"))
			writeJSON(w, `{"status":"pending","type":"users_export","id":"job_1"}`)
		case r.URL.Path == "/api/v2/jobs/job_1":
			f.mu.Lock()
			i := min(f.polls, len(f.statuses)-1)
			f.polls++
			f.mu.Unlock()
			writeJSON(w, f.statuses[i])
		default:
			http.NotFound(w, r)
		}
	}, opts...)
}

// gzipLines serves n users, one JSON object per line, as an export file.
func gzipLines(lines ...string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")

		zw := gzip.NewWriter(w)
		_, _ = zw.Write([]byte(strings.Join(lines, "\n") + "\n"))
		_ = zw.Close()
	}
}

// filled is lines plus filler users up to n, so a file matches the total the list reports.
func filled(n int, lines ...string) []string {
	for i := len(lines); i < n; i++ {
		lines = append(lines, exportUserLine(10000+i))
	}

	return lines
}

func exportUserLine(i int) string {
	return fmt.Sprintf(`{"user_id":"auth0|%04d","email":"u%d@example.com"}`, i, i)
}

func subjects(t *testing.T, events []map[string]any) []string {
	t.Helper()

	var out []string

	for _, ev := range events {
		s, _ := ev["subject"].(string)
		out = append(out, s)
	}

	return out
}

func TestExportLargeTenantUsesAJob(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := range 1500 {
		lines = append(lines, exportUserLine(i))
	}

	f := &exportFake{
		statuses: []string{
			`{"status":"pending","type":"users_export","id":"job_1"}`,
			`{"status":"processing","type":"users_export","id":"job_1","percentage_done":40}`,
			`{"status":"completed","type":"users_export","id":"job_1","location":"LOCATION"}`,
		},
		file: gzipLines(lines...),
	}
	p, rec := newExportProvider(t, 1500, f)

	var (
		said   []string
		waited []time.Duration
	)

	p.wait = func(_ context.Context, d time.Duration) error {
		waited = append(waited, d)

		return nil
	}

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		if pr.Message != "" {
			said = append(said, pr.Message)
		}
	})
	noErr(t, err)
	equal(t, 1500, len(res.Events))
	equal(t, "user auth0|0000", res.Events[0]["subject"])
	equal(t, "user auth0|1499", res.Events[1499]["subject"])
	equal(t, "snapshot.user", res.Events[0]["type"])

	// The list was asked for its first page only.
	equal(t, 1, len(rec.find("/users")))
	equal(t, 1, len(rec.find("/jobs/users-exports")))
	equal(t, 3, f.polls)
	equal(t, []time.Duration{2 * time.Second, 2 * time.Second}, waited)
	equal(t, int64(1), f.downloads.Load())

	var req struct {
		Format string `json:"format"`
		Fields []struct {
			Name string `json:"name"`
		} `json:"fields"`
	}

	noErr(t, json.Unmarshal([]byte(f.createdBy[0]), &req))
	equal(t, "json", req.Format)

	var names []string
	for _, fld := range req.Fields {
		names = append(names, fld.Name)
	}

	equal(t, []string{
		"user_id", "email", "email_verified", "username", "phone_number", "phone_verified", "created_at",
		"updated_at", "identities", "app_metadata", "user_metadata", "picture", "name", "nickname", "multifactor",
		"last_ip", "last_login", "last_password_reset", "logins_count", "blocked", "given_name", "family_name",
	}, names)

	// The job requests carry the token; the download, on another host, does not.
	equal(t, "Bearer mgmt-token", f.apiAuth.Load())
	equal(t, "", f.fileAuth.Load())

	equal(t, []string{
		"reading users", "exporting users", "exporting users: waiting for Auth0's job (0s)", "exporting users: 40%",
	}, said)
}

func TestExportedUserEqualsListedUser(t *testing.T) {
	t.Parallel()

	const identities = `[{"connection":"db","user_id":"anne","provider":"auth0","isSocial":false}]`

	// The same user: the export carries milliseconds, the list does not.
	listed := `{"user_id":"auth0|anne","email":"anne@example.com","email_verified":true,"logins_count":3,` +
		`"created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:06Z","identities":` + identities + `,` +
		`"app_metadata":{"role":"admin"},"user_metadata":{}}`
	exported := `{"user_id":"auth0|anne","email":"anne@example.com","email_verified":true,"logins_count":3,` +
		`"created_at":"2026-01-02T03:04:05.000Z","updated_at":"2026-01-02T03:04:06.000Z","identities":` + identities + `,` +
		`"app_metadata":{"role":"admin"},"user_metadata":{}}`

	// Through the list.
	pl, _ := newProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"start":0,"limit":100,"total":1,"users":[`+listed+`]}`)
	})
	resList, err := pl.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)

	// Through the export.
	f := &exportFake{file: gzipLines(filled(1500, exported)...)}
	pe, _ := newExportProvider(t, 1500, f)
	resExport, err := pe.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)

	equal(t, 1, len(resList.Events))
	equal(t, 1500, len(resExport.Events))
	equal(t, objectOf(resList.Events[0]), objectOf(resExport.Events[0]))
}

// An identity's profileData, which the list may leave out, survives the SDK type.
func TestExportedIdentityKeepsProfileData(t *testing.T) {
	t.Parallel()

	anne := `{"user_id":"auth0|anne","identities":[{"provider":"auth0","user_id":"anne",` +
		`"connection":"db","isSocial":false,"profileData":{"email":"anne@example.com"}}]}`
	f := &exportFake{file: gzipLines(filled(1500, anne)...)}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)

	ids, _ := objectOf(res.Events[0])["identities"].([]any)
	requireNotEmpty(t, ids)

	id, _ := ids[0].(map[string]any)
	equal(t, map[string]any{"email": "anne@example.com"}, id["profileData"])
}

func objectOf(ev map[string]any) map[string]any {
	data, _ := ev["data"].(map[string]any)
	obj, _ := data["object"].(map[string]any)

	return obj
}

func TestExportDuplicateUserIsOneEvent(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: gzipLines(filled(1501, exportUserLine(1), exportUserLine(2), exportUserLine(1))...)}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	// The repeated line is one event: 2 + 1,498 filler users reach the total (1,501 lines).
	equal(t, 1500, len(res.Events))
	equal(t, []string{"user auth0|0001", "user auth0|0002"}, subjects(t, res.Events)[:2])
}

func TestExportFailedJobIsReportedWithoutDownload(t *testing.T) {
	t.Parallel()

	f := &exportFake{
		statuses: []string{`{"status":"failed","type":"users_export","id":"job_1","status_details":"secret detail"}`},
		file:     gzipLines(),
	}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, `Auth0's user export job ended as "failed"`, err.Error())
	equal(t, int64(0), f.downloads.Load())
}

func TestExportDownloadRefusedHidesTheURL(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, "downloading Auth0's user export failed: HTTP 403", err.Error())
	notContains(t, err.Error(), "secret123")
	notContains(t, err.Error(), "export.gz")
}

func TestExportDownloadUnreachableHidesTheURL(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: gzipLines()}

	// The first status closes the download server, so the download cannot connect.
	dl := httptest.NewServer(http.NotFoundHandler())
	location := dl.URL + "/export.gz" + exportQuery
	dl.Close()

	f.statuses = []string{`{"status":"completed","type":"users_export","id":"job_1","location":"` + location + `"}`}
	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/users":
			usersPage(w, r, 1500, func(page int) []int { return []int{page} })
		case "/api/v2/jobs/users-exports":
			writeJSON(w, `{"status":"pending","type":"users_export","id":"job_1"}`)
		default:
			writeJSON(w, f.statuses[0])
		}
	})

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	contains(t, err.Error(), "downloading Auth0's user export failed: ")
	notContains(t, err.Error(), "secret123")
	notContains(t, err.Error(), "export.gz")
	notContains(t, err.Error(), dl.URL)
}

func TestExportCancelledWhileWaitingReturnsPromptly(t *testing.T) {
	t.Parallel()

	f := &exportFake{
		statuses: []string{`{"status":"processing","type":"users_export","id":"job_1","percentage_done":10}`},
		file:     gzipLines(),
	}
	p, _ := newExportProvider(t, 1500, f)
	// A wait that only the context can end.
	p.wait = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
			return errors.New("the context did not end the wait")
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		if strings.HasPrefix(pr.Message, "exporting users:") {
			cancel()
		}
	})

	isErr(t, err)
	equal(t, context.Canceled, err)
}

func TestExportCreateForbiddenNamesScope(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/users" {
			usersPage(w, r, 1500, func(page int) []int { return []int{page} })

			return
		}

		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, `{"message":"Insufficient scope"}`)
	})

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	contains(t, err.Error(), "users export")
	contains(t, errHint(err), "read:users")
}

func TestExportNotUsedBelowTheLimit(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, usersHandler(999))

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	equal(t, 999, len(res.Events))
	equal(t, 0, len(rec.find("/jobs/users-exports")))
}

// Auth0 reports a total of 1,000 for any tenant with that many users or more,
// so a total at the limit cannot be told from a larger one.
func TestExportUsedAtTheLimit(t *testing.T) {
	t.Parallel()

	for _, users := range []int{1000, 1205} {
		f := &exportFake{file: gzipLines(filled(users)...)}
		p, rec := newExportProvider(t, users, f)

		res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
		noErr(t, err)
		equal(t, users, len(res.Events))
		equal(t, 1, len(rec.find("/users")))
		equal(t, 1, len(rec.find("/jobs/users-exports")))
	}
}

// TestLiveExportEqualsList reads a real tenant's users through the list and,
// with the threshold lowered, through the export job; the two must agree, apart
// from the profileData an export may add to an identity.
// It is skipped without AUTH0_DOMAIN. It changes a package variable, so it does
// not run in parallel.
func TestLiveExportEqualsList(t *testing.T) {
	domain, id, secret := os.Getenv("AUTH0_DOMAIN"), os.Getenv("AUTH0_CLIENT_ID"), os.Getenv("AUTH0_CLIENT_SECRET")
	if domain == "" {
		t.Skip("AUTH0_DOMAIN is not set")
	}

	ctx := context.Background()

	p, err := New(ctx, Config{Domain: domain, ClientID: id, ClientSecret: secret})
	noErr(t, err)

	listed, err := p.Snapshot(ctx, snapshot.Request{Types: []string{typeUser}}, nil)
	noErr(t, err)

	old := maxUsers
	maxUsers = 0

	defer func() { maxUsers = old }()

	exported, err := p.Snapshot(ctx, snapshot.Request{Types: []string{typeUser}}, nil)
	noErr(t, err)

	byID := func(events []map[string]any) map[string]map[string]any {
		out := map[string]map[string]any{}

		for _, ev := range events {
			obj := objectOf(ev)
			uid, _ := obj["user_id"].(string)

			// The export may add profileData to an identity: a known difference.
			ids, _ := obj["identities"].([]any)
			for _, i := range ids {
				if m, ok := i.(map[string]any); ok {
					delete(m, "profileData")
				}
			}

			out[uid] = obj
		}

		return out
	}

	l, e := byID(listed.Events), byID(exported.Events)
	t.Logf("listed %d users, exported %d users", len(l), len(e))

	if len(l) != len(e) {
		t.Errorf("listed %d users, exported %d", len(l), len(e))
	}

	for uid, lo := range l {
		eo, ok := e[uid]
		if !ok {
			t.Errorf("a listed user is missing from the export (id has %d chars)", len(uid))

			continue
		}

		if reflect.DeepEqual(lo, eo) {
			continue
		}

		var diff []string

		for k := range lo {
			if !reflect.DeepEqual(lo[k], eo[k]) {
				diff = append(diff, k)
			}
		}

		for k := range eo {
			if _, ok := lo[k]; !ok {
				diff = append(diff, k)
			}
		}

		sort.Strings(diff)
		t.Errorf("a user differs in the keys %v", diff)
	}
}

func notContains(t *testing.T, s, sub string) {
	t.Helper()

	if strings.Contains(s, sub) {
		t.Errorf("%q contains %q", s, sub)
	}
}

func errHint(err error) string { return clierr.NewEnvelope(err).Error.Hint }

// gzipped compresses lines the way an export file is.
func gzipped(lines ...string) []byte {
	var b bytes.Buffer

	zw := gzip.NewWriter(&b)
	_, _ = zw.Write([]byte(strings.Join(lines, "\n") + "\n"))
	_ = zw.Close()

	return b.Bytes()
}

func serveBytes(b []byte) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) }
}

func TestExportShorterThanTheListedTotalFails(t *testing.T) {
	t.Parallel()

	for name, lines := range map[string][]string{"below the listed total": filled(999), "empty": nil} {
		f := &exportFake{file: gzipLines(lines...)}
		p, _ := newExportProvider(t, 1500, f)

		res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
		isErr(t, err)

		if res != nil {
			t.Errorf("%s: result = %+v, want nil", name, res)
		}

		want := fmt.Sprintf("Auth0's user export has %d users where the list reported 1000 or more; "+
			"users may have been deleted meanwhile, run the command again", len(lines))
		equal(t, want, err.Error())
	}
}

func TestExportLongerThanTheListedTotalPasses(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: gzipLines(filled(1501)...)}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	equal(t, 1501, len(res.Events))
}

func TestExportCutMidFileFails(t *testing.T) {
	t.Parallel()

	whole := gzipped(filled(1500)...)
	f := &exportFake{file: serveBytes(whole[:len(whole)/2])}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, "downloading Auth0's user export failed: unexpected EOF", err.Error())

	if res != nil {
		t.Errorf("result = %+v, want nil", res)
	}
}

func TestExportBadLineFailsWithoutQuotingIt(t *testing.T) {
	t.Parallel()

	lines := filled(1500)
	lines[700] = "ZQXLINE not json"
	f := &exportFake{file: gzipLines(lines...)}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	contains(t, err.Error(), "downloading Auth0's user export failed")

	for _, part := range []string{"ZQX", "not json", "'Z'"} {
		notContains(t, err.Error(), part)
	}
}

func TestExportPollFailureNamesTheExport(t *testing.T) {
	t.Parallel()

	p, _ := newProvider(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/users":
			usersPage(w, r, 1500, func(page int) []int { return []int{page} })
		case "/api/v2/jobs/users-exports":
			writeJSON(w, `{"status":"pending","type":"users_export","id":"job_1"}`)
		default:
			w.WriteHeader(http.StatusInternalServerError)
			writeJSON(w, `{"message":"boom"}`)
		}
	}, option.WithMaxAttempts(1))

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	contains(t, err.Error(), "users export")
}

func TestExportCancelledWhileDownloadingReturnsTheContextError(t *testing.T) {
	t.Parallel()

	var (
		reading = make(chan struct{})
		closed  = make(chan struct{})
	)

	whole := gzipped(filled(1500)...)
	f := &exportFake{file: func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(whole[:len(whole)/2])
		w.(http.Flusher).Flush()
		close(reading)

		<-r.Context().Done() // the client closed the body
		close(closed)
	}}
	p, _ := newExportProvider(t, 1500, f)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-reading
		cancel()
	}()

	_, err := p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, context.Canceled, err)
	notContains(t, err.Error(), "secret123")

	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the download body was not closed")
	}
}

func TestExportReplacesUsersTheListGathered(t *testing.T) {
	t.Parallel()

	// The list says 999 on the first page and 1,000 on the second.
	f := &exportFake{
		file: gzipLines(filled(1001)...),
		users: func(w http.ResponseWriter, r *http.Request) {
			total := 999
			if r.URL.Query().Get("page") == "1" {
				total = 1000
			}

			usersPage(w, r, total, func(page int) []int {
				var ids []int
				for i := page * pageSize; i < (page+1)*pageSize; i++ {
					ids = append(ids, i)
				}

				return ids
			})
		},
	}
	p, _ := newExportProvider(t, 0, f)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	equal(t, 1001, len(res.Events))

	// The tally was reset when the export replaced what the list gathered.
	equal(t, res.Counts, p.Stats().Counts)

	seen := map[string]bool{}
	for _, s := range subjects(t, res.Events) {
		if seen[s] {
			t.Errorf("%s appears twice", s)
		}

		seen[s] = true
	}
}

func TestExportProgressWhileWaiting(t *testing.T) {
	t.Parallel()

	job := func(extra string) string {
		return `{"status":"processing","type":"users_export","id":"job_1"` + extra + `}`
	}
	f := &exportFake{
		statuses: []string{
			job(""), job(""), job(`,"percentage_done":10`), job(`,"percentage_done":10`), job(`,"percentage_done":20`),
			`{"status":"completed","type":"users_export","id":"job_1","location":"LOCATION"}`,
		},
		file: gzipLines(filled(1500)...),
	}
	p, _ := newExportProvider(t, 1500, f)

	// The provider's clock moves two seconds for each wait.
	var (
		mu      sync.Mutex
		elapsed time.Duration
	)

	base := time.Now()
	p.limits.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return base.Add(elapsed)
	}
	p.wait = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()

		elapsed += d

		return ctx.Err()
	}

	var said []string

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		if pr.Message != "" {
			said = append(said, pr.Message)
		}
	})
	noErr(t, err)
	equal(t, []string{
		"reading users",
		"exporting users",
		"exporting users: waiting for Auth0's job (0s)",
		"exporting users: waiting for Auth0's job (2s)",
		"exporting users: 10%",
		"exporting users: 20%",
	}, said)
}

func TestExportBadDateDoesNotQuoteItsValue(t *testing.T) {
	t.Parallel()

	lines := filled(1500)
	lines[10] = `{"user_id":"auth0|bad","created_at":"ZQXDATE"}`
	f := &exportFake{file: gzipLines(lines...)}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, "downloading Auth0's user export failed: a line is not a JSON user", err.Error())
	notContains(t, err.Error(), "ZQX")
}

func TestExportLineWithoutUserIDFails(t *testing.T) {
	t.Parallel()

	lines := filled(1500)
	lines[10] = `{}`
	f := &exportFake{file: gzipLines(lines...)}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	equal(t, "downloading Auth0's user export failed: a line is not a JSON user", err.Error())
}

// jumpingClock makes the provider's clock move by each wait, and counts the waits.
func jumpingClock(p *Provider, step time.Duration) *atomic.Int64 {
	var (
		mu      sync.Mutex
		elapsed time.Duration
		waits   atomic.Int64
	)

	base := time.Now()
	p.limits.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		return base.Add(elapsed)
	}
	p.wait = func(ctx context.Context, _ time.Duration) error {
		mu.Lock()
		defer mu.Unlock()

		elapsed += step
		waits.Add(1)

		return ctx.Err()
	}

	return &waits
}

func TestExportJobThatNeverFinishesTimesOut(t *testing.T) {
	t.Parallel()

	f := &exportFake{
		statuses: []string{`{"status":"processing","type":"users_export","id":"job_1"}`},
		file:     gzipLines(filled(1500)...),
	}
	p, _ := newExportProvider(t, 1500, f)
	waits := jumpingClock(p, time.Minute)

	var users int

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		if pr.Step == exportDownloadStep {
			users++
		}
	})
	isErr(t, err)
	equal(t, "Auth0's user export job was still processing after 30m0s; run the command again", err.Error())
	equal(t, int64(30), waits.Load())
	equal(t, int64(0), f.downloads.Load())
	equal(t, 0, users)
}

func TestExportJobFinishingAt29MinutesStillSucceeds(t *testing.T) {
	t.Parallel()

	statuses := make([]string, 0, 30)
	for range 29 {
		statuses = append(statuses, `{"status":"pending","type":"users_export","id":"job_1"}`)
	}

	statuses = append(statuses, `{"status":"completed","type":"users_export","id":"job_1","location":"LOCATION"}`)

	f := &exportFake{statuses: statuses, file: gzipLines(filled(1500)...)}
	p, _ := newExportProvider(t, 1500, f)
	jumpingClock(p, time.Minute)

	res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	noErr(t, err)
	equal(t, 1500, len(res.Events))
}

// downloadProvider is an export provider whose stall limit is short.
func downloadProvider(t *testing.T, f *exportFake) (*Provider, *atomic.Int64) {
	t.Helper()

	p, _ := newExportProvider(t, 1500, f)
	p.stall = 300 * time.Millisecond

	var users atomic.Int64

	return p, &users
}

func readUsers(ctx context.Context, p *Provider, users *atomic.Int64) (*snapshot.Result, error) {
	return p.Snapshot(ctx, snapshot.Request{Types: []string{"snapshot.user"}}, func(pr snapshot.Progress) {
		if pr.Step == exportDownloadStep {
			users.Add(1)
		}
	})
}

func assertStalled(t *testing.T, err error, took time.Duration, users *atomic.Int64) {
	t.Helper()

	isErr(t, err)
	contains(t, err.Error(), "downloading Auth0's user export failed: no data for 300ms")
	notContains(t, err.Error(), "127.0.0.1")
	notContains(t, err.Error(), "secret123")

	// Generous: the point is that the read ends, and a loaded runner is slow.
	if took > 10*time.Second {
		t.Errorf("read took %s, want it to end after the stall limit", took)
	}

	equal(t, int64(0), users.Load())
}

func TestExportDownloadThatStopsHalfwayEnds(t *testing.T) {
	t.Parallel()

	whole := gzipped(filled(1500)...)
	f := &exportFake{file: func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(whole[:len(whole)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}}
	p, users := downloadProvider(t, f)

	start := time.Now()
	_, err := readUsers(context.Background(), p, users)
	assertStalled(t, err, time.Since(start), users)
}

func TestExportDownloadThatNeverAnswersEnds(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }}
	p, users := downloadProvider(t, f)

	start := time.Now()
	_, err := readUsers(context.Background(), p, users)
	assertStalled(t, err, time.Since(start), users)
}

func TestExportSlowButSteadyDownloadSucceeds(t *testing.T) {
	t.Parallel()

	whole := gzipped(filled(1500)...)
	f := &exportFake{file: func(w http.ResponseWriter, _ *http.Request) {
		const chunk = 100

		// Takes longer than the stall limit in all, never that long without data.
		for i := 0; i < len(whole); i += chunk {
			_, _ = w.Write(whole[i:min(i+chunk, len(whole))])
			w.(http.Flusher).Flush()
			time.Sleep(10 * time.Millisecond)
		}
	}}
	p, users := downloadProvider(t, f)
	p.stall = 400 * time.Millisecond

	start := time.Now()
	res, err := readUsers(context.Background(), p, users)
	noErr(t, err)
	equal(t, 1500, len(res.Events))

	if time.Since(start) <= p.stall {
		t.Fatalf("download took %s, want longer than the stall limit %s", time.Since(start), p.stall)
	}
}

func TestExportReadWhetherOrNotItArrivesCompressed(t *testing.T) {
	t.Parallel()

	plain := []byte(strings.Join(filled(1500), "\n") + "\n")
	cases := map[string]func(w http.ResponseWriter, r *http.Request){
		// Go's transport decompresses the body, so the client sees plain lines.
		"content-encoding gzip": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")

			zw := gzip.NewWriter(w)
			_, _ = zw.Write(plain)
			_ = zw.Close()
		},
		"plain body": serveBytes(plain),
	}

	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			p, _ := newExportProvider(t, 1500, &exportFake{file: file})

			res, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
			noErr(t, err)
			equal(t, 1500, len(res.Events))
		})
	}
}

func TestExportHTMLBodyFailsAsNotJSON(t *testing.T) {
	t.Parallel()

	p, _ := newExportProvider(t, 1500, &exportFake{file: serveBytes([]byte("<html><body>Access Denied</body></html>"))})

	_, err := p.Snapshot(context.Background(), snapshot.Request{Types: []string{"snapshot.user"}}, nil)
	isErr(t, err)
	contains(t, err.Error(), "a line is not a JSON user")
	notContains(t, err.Error(), "secret123")
	notContains(t, err.Error(), "Access Denied")
}
