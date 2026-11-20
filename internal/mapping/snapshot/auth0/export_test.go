package auth0

import (
	"compress/gzip"
	"context"
	"encoding/json"
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
)

// exportFake is a tenant whose users list reports total users and whose export
// job goes through statuses, one per poll (the last repeats). The export file
// is served by a second server, the way Auth0 serves it from S3.
type exportFake struct {
	statuses []string // JSON bodies of the job, one per poll
	file     func(w http.ResponseWriter, r *http.Request)

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

	res, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, func(s string) { said = append(said, s) })
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

	equal(t, []string{"reading users", "exporting 1500 users", "exporting users: 40%"}, said)
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
	resList, err := pl.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	noErr(t, err)

	// Through the export.
	f := &exportFake{file: gzipLines(exported)}
	pe, _ := newExportProvider(t, 1500, f)
	resExport, err := pe.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	noErr(t, err)

	equal(t, 1, len(resList.Events))
	equal(t, 1, len(resExport.Events))
	equal(t, objectOf(resList.Events[0]), objectOf(resExport.Events[0]))
}

// An identity's profileData, which the list may leave out, survives the SDK type.
func TestExportedIdentityKeepsProfileData(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: gzipLines(`{"user_id":"auth0|anne","identities":[{"provider":"auth0","user_id":"anne",` +
		`"connection":"db","isSocial":false,"profileData":{"email":"anne@example.com"}}]}`)}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
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

	f := &exportFake{file: gzipLines(exportUserLine(1), exportUserLine(2), exportUserLine(1))}
	p, _ := newExportProvider(t, 1500, f)

	res, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	noErr(t, err)
	equal(t, []string{"user auth0|0001", "user auth0|0002"}, subjects(t, res.Events))
}

func TestExportFailedJobIsReportedWithoutDownload(t *testing.T) {
	t.Parallel()

	f := &exportFake{
		statuses: []string{`{"status":"failed","type":"users_export","id":"job_1","status_details":"secret detail"}`},
		file:     gzipLines(),
	}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	isErr(t, err)
	equal(t, `Auth0's user export job ended as "failed"`, err.Error())
	equal(t, int64(0), f.downloads.Load())
}

func TestExportDownloadRefusedHidesTheURL(t *testing.T) {
	t.Parallel()

	f := &exportFake{file: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }}
	p, _ := newExportProvider(t, 1500, f)

	_, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
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

	_, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
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
	p.wait = sleepContext // the real wait, which a cancelled context must cut short

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	_, err := p.Snapshot(ctx, []string{"snapshot.user"}, func(s string) {
		if strings.HasPrefix(s, "exporting users:") {
			cancel()
		}
	})

	isErr(t, err)
	equal(t, context.Canceled, err)

	if d := time.Since(start); d >= exportPollEvery {
		t.Errorf("returned after %v, want before the %v wait ends", d, exportPollEvery)
	}
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

	_, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	isErr(t, err)
	contains(t, err.Error(), "users export")
	contains(t, errHint(err), "read:users")
}

func TestExportNotUsedAtTheLimit(t *testing.T) {
	t.Parallel()

	p, rec := newProvider(t, usersHandler(1000))

	res, err := p.Snapshot(context.Background(), []string{"snapshot.user"}, nil)
	noErr(t, err)
	equal(t, 1000, len(res.Events))
	equal(t, 0, len(rec.find("/jobs/users-exports")))
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

	listed, err := p.Snapshot(ctx, []string{typeUser}, nil)
	noErr(t, err)

	old := maxUsers
	maxUsers = 0

	defer func() { maxUsers = old }()

	exported, err := p.Snapshot(ctx, []string{typeUser}, nil)
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
