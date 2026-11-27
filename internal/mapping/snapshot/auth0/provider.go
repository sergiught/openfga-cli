package auth0

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/auth0/go-auth0/v2/management"
	"github.com/auth0/go-auth0/v2/management/client"
	"github.com/auth0/go-auth0/v2/management/core"
	"github.com/auth0/go-auth0/v2/management/option"
	"golang.org/x/sync/errgroup"

	"github.com/sergiught/openfga-cli/internal/mapping/snapshot"
)

// now is the clock, replaceable in tests.
var now = time.Now

// readWorkers is how many requests of one read run at the same time.
const readWorkers = 8

// Config says which tenant to read and how to sign in.
type Config struct {
	Domain, ClientID, ClientSecret string
	Tenant                         string // "" means the first label of Domain
}

// Provider reads an Auth0 tenant. It implements snapshot.Provider.
type Provider struct {
	m              *client.Management
	source, tenant string
	limits         *pacer
	wait           func(ctx context.Context, d time.Duration) error // between export job polls
	stall          time.Duration                                    // longest an export download may send no data

	mu  sync.Mutex
	cur *tally // the read in progress, or the last one; nil before any
}

var _ snapshot.Provider = (*Provider)(nil)

// New builds a provider. opts come after the defaults, so they can override
// them; tests pass option.WithInsecure(). With no ClientID no credentials are
// configured.
func New(ctx context.Context, cfg Config, opts ...option.RequestOption) (*Provider, error) {
	host := strings.TrimSuffix(strings.TrimSpace(cfg.Domain), "/")
	if i := strings.Index(host, "//"); i != -1 {
		host = host[i+2:]
	}

	if host == "" {
		return nil, errors.New("Auth0 domain is required")
	}

	tenant := cfg.Tenant
	if tenant == "" {
		tenant, _, _ = strings.Cut(host, ".")
	}

	var all []option.RequestOption
	if cfg.ClientID != "" {
		all = append(all, option.WithClientCredentials(ctx, cfg.ClientID, cfg.ClientSecret))
	}

	all = append(all, option.WithMaxAttempts(5))

	// This client replaces the one the SDK builds, which is why the pacer sets
	// the User-Agent. Like the SDK's default, http.DefaultClient, it has no
	// timeout and uses http.DefaultTransport.
	limits := newPacer(http.DefaultTransport)
	all = append(all, option.WithHTTPClient(&http.Client{Transport: limits}))
	all = append(all, opts...)

	m, err := client.New(host, all...)
	if err != nil {
		return nil, err
	}

	return &Provider{m: m, source: host, tenant: tenant, limits: limits, wait: sleepContext, stall: exportStallTimeout}, nil
}

// Name is the --provider value.
func (p *Provider) Name() string { return "auth0" }

// Label is the display name.
func (p *Provider) Label() string { return "Auth0" }

// Source is the tenant domain, the same value as Result.Source.
func (p *Provider) Source() string { return p.source }

// Types lists the snapshot event types this provider produces.
func (p *Provider) Types() []snapshot.Type { return Types() }

// tally counts the items a read has gathered per type. Every sub-read of one
// Snapshot shares it.
type tally struct {
	mu    sync.Mutex
	n     map[string]int
	want  map[string]bool
	since time.Time
}

func (t *tally) add(typ string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.n[typ]++
}

func (t *tally) reset(typ string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.n, typ)
}

// counts lists the requested types in Types() order.
func (t *tally) counts() []snapshot.Count {
	t.mu.Lock()
	defer t.mu.Unlock()

	var out []snapshot.Count

	for _, typ := range Types() {
		if t.want[typ.Name] {
			out = append(out, snapshot.Count{Type: typ.Name, N: t.n[typ.Name], Windowed: typ.Windowed && !t.since.IsZero()})
		}
	}

	return out
}

// Stats says what the read in progress, or the last one, has done so far.
func (p *Provider) Stats() snapshot.Stats {
	// Held throughout so a Snapshot starting meanwhile cannot pair the previous
	// read's counts with the new read's request totals.
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cur == nil {
		return snapshot.Stats{}
	}

	sent, limited, gap := p.limits.stats()

	return snapshot.Stats{Requests: sent, RateLimited: limited, Pace: gap, Counts: p.cur.counts()}
}

// read is one Snapshot call's state.
type read struct {
	*Provider

	ctx      context.Context //nolint:containedctx // scoped to one call
	want     map[string]bool
	since    time.Time
	progress func(snapshot.Progress)
	sayMu    *sync.Mutex // progress is called from several goroutines, one at a time
	events   map[string][]event
	tally    *tally                // shared by every sub-read
	out      *sink                 // where events go instead of events, nil when Result.Events is wanted
	done     func(key string) bool // units an earlier run finished; only read with out.save
	remember func(key, value string) error
	recall   func(key string) string
}

// exportJobKey is the work file value that holds the id of the users export job.
// Only the id is kept: the job's download location is a signed URL.
const exportJobKey = "auth0-users-export-job"

// errLocationGone marks a download the host refused as an expired location.
// Its text is the HTTP status only, never the URL.
type errLocationGone struct{ status int }

func (e errLocationGone) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

// flushEvery is how many events one Emit call is given at most.
const flushEvery = 1000

// sink hands events to Request.Emit as they become final. Only the goroutine
// that runs the read, or the one organization task holding each's lock,
// calls it, so Emit is never called twice at once.
type sink struct {
	emit  func(events []map[string]any) error
	save  func(u snapshot.Unit, events []map[string]any, finished bool) error // instead of emit
	seq   int                                                                 // events given so far, the last one's number
	count map[string]int                                                      // events given so far, per type
}

// flush gives the events held for typ to Emit, in order, and forgets them.
// With no Emit it keeps them for Result.Events.
func (r *read) flush(typ string) error {
	return r.flushUnit(typ, true)
}

// flushUnit is flush; with finished false a unit that goes to Save is not
// closed, because more of its events are still to come.
func (r *read) flushUnit(typ string, finished bool) error {
	if r.out == nil {
		return nil
	}

	events := r.events[typ]
	delete(r.events, typ)

	if r.out.save != nil {
		// Only these types are units of their own; the others go with their organization.
		switch typ {
		case typeConnection, typeUser, typeOrganization:
			if !r.want[typ] {
				return nil
			}

			return r.saveUnit(snapshot.Unit{Key: unitKeys[typ]}, map[string][]event{typ: events}, finished)
		}

		return nil
	}

	return r.give(typ, events)
}

// unitKeys are the keys of the units that are one type each.
var unitKeys = map[string]string{typeConnection: "connections", typeUser: "users", typeOrganization: "organizations"}

// saveUnit hands the events of one unit to Save, flushEvery at a time, then
// says the unit is complete when finished. Types go in Types() order, each type's events in
// the order they arrived.
func (r *read) saveUnit(u snapshot.Unit, byType map[string][]event, finished bool) error {
	for _, t := range Types() {
		events := byType[t.Name]

		for len(events) > 0 {
			n := min(len(events), flushEvery)
			batch := make([]map[string]any, n)

			for i, e := range events[:n] {
				r.out.seq++
				batch[i] = r.envelope(e, r.out.seq)
			}

			r.out.count[t.Name] += n
			events = events[n:]

			if err := r.out.save(u, batch, false); err != nil {
				return err
			}
		}
	}

	if !finished {
		return nil
	}

	return r.out.save(u, nil, true)
}

// give wraps events of typ and hands them to Emit, flushEvery at a time.
func (r *read) give(typ string, events []event) error {
	for len(events) > 0 {
		n := min(len(events), flushEvery)
		batch := make([]map[string]any, n)

		for i, e := range events[:n] {
			r.out.seq++
			batch[i] = r.envelope(e, r.out.seq)
		}

		r.out.count[typ] += n
		events = events[n:]

		if err := r.out.emit(batch); err != nil {
			return err
		}
	}

	return nil
}

// isDone says whether an earlier run finished the unit. Only a read that saves
// by unit asks.
func (r *read) isDone(key string) bool {
	return r.out != nil && r.out.save != nil && r.done != nil && r.done(key)
}

func (r *read) say(msg string) {
	r.report(snapshot.Progress{Message: msg})
}

func (r *read) report(pr snapshot.Progress) {
	if r.progress != nil {
		r.sayMu.Lock()
		defer r.sayMu.Unlock()

		r.progress(pr)
	}
}

// parallel runs task for 0..n-1, at most readWorkers at a time, and calls
// finished with the number of tasks done so far after each one, never
// concurrently and never going backwards. The first error wins and stops the
// rest; a cancelled ctx gives its own error. Each task gets its own context
// and must keep its results to itself.
func parallel(ctx context.Context, n int, task func(ctx context.Context, i int) error, finished func(done int)) error {
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(readWorkers)

	var (
		mu   sync.Mutex
		done int
	)

	for i := 0; i < n && gctx.Err() == nil; i++ {
		g.Go(func() error {
			if err := task(gctx, i); err != nil {
				return err
			}

			mu.Lock()
			defer mu.Unlock()

			done++
			if finished != nil {
				finished(done)
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return err
	}

	return ctx.Err()
}

// each runs task once per organization in parallel. A task adds its events to
// the read it is given; they are merged in organization order afterwards, so
// the result is the same as reading one organization after the other.
// With Emit, the events of the types in stream go to Emit instead, as soon as
// every organization before theirs has finished, and are not kept.
// With Save, each organization's events of every type go to Save as its task
// finishes, as the unit kind/<id>, and are not kept; a unit Done reports true
// is not read, and never saved again.
func (r *read) each(orgs []map[string]any, kind, step, progress string, stream []string, task func(sub *read, org map[string]any) error) error {
	subs := make([]*read, len(orgs))
	finished := make([]bool, len(orgs))

	var (
		mu     sync.Mutex // one task at a time hands events over
		next   int        // the first organization not handed over
		failed error      // what Emit returned; nothing is handed over after it
	)

	err := parallel(r.ctx, len(orgs), func(ctx context.Context, i int) error {
		sub := *r
		sub.ctx, sub.events = ctx, map[string][]event{}
		subs[i] = &sub

		id, _ := orgs[i]["id"].(string)
		unit := snapshot.Unit{Key: kind + "/" + id, Order: i}

		if r.isDone(unit.Key) {
			return nil
		}

		if err := task(&sub, orgs[i]); err != nil {
			return err
		}

		if r.out == nil {
			return nil
		}

		mu.Lock()
		defer mu.Unlock()

		if r.out.save != nil {
			if failed != nil {
				return failed
			}

			if failed = r.saveUnit(unit, sub.events, true); failed != nil {
				return failed
			}

			sub.events = nil

			return nil
		}

		if failed != nil {
			return failed
		}

		finished[i] = true

		for ; next < len(orgs) && finished[next]; next++ {
			for _, typ := range stream {
				if failed = r.give(typ, subs[next].events[typ]); failed != nil {
					return failed
				}

				delete(subs[next].events, typ)
			}
		}

		return nil
	}, func(done int) {
		r.report(snapshot.Progress{Message: fmt.Sprintf(progress, done, len(orgs)), Step: step, Done: done, Total: len(orgs)})
	})
	if err != nil {
		return err
	}

	for _, sub := range subs {
		for typ, events := range sub.events {
			r.events[typ] = append(r.events[typ], events...)
		}
	}

	return nil
}

func (r *read) add(e event) {
	r.events[e.typ] = append(r.events[e.typ], e)
	r.tally.add(e.typ)
}

// Snapshot reads the given types completely or returns an error.
func (p *Provider) Snapshot(ctx context.Context, req snapshot.Request, progress func(snapshot.Progress)) (*snapshot.Result, error) {
	r := &read{Provider: p, ctx: ctx, want: map[string]bool{}, since: req.Since, progress: progress, sayMu: &sync.Mutex{}, events: map[string][]event{}}
	if req.Emit != nil {
		r.out = &sink{emit: req.Emit, count: map[string]int{}}
	}

	if req.Save != nil {
		r.out = &sink{save: req.Save, count: map[string]int{}}
		r.done = req.Done
		r.remember, r.recall = req.Remember, req.Recall
	}
	r.tally = &tally{n: map[string]int{}, want: r.want, since: req.Since}

	known := map[string]bool{}
	for _, t := range Types() {
		known[t.Name] = true
	}

	for _, t := range req.Types {
		if !known[t] {
			return nil, fmt.Errorf("unknown snapshot type %q", t)
		}

		r.want[t] = true
	}

	// Stats takes p.mu too, so it sees the new tally together with the new
	// pacer counts, never one read's tally next to the other's requests.
	p.mu.Lock()
	p.limits.begin(func() { r.say("waiting on Auth0's rate limit") })
	p.cur = r.tally
	p.mu.Unlock()

	res := &snapshot.Result{Source: p.source, StartedAt: now()}

	steps := []struct {
		typ  string
		read func() error
	}{
		{typeConnection, r.connections},
		{typeUser, r.users},
	}

	for _, s := range steps {
		if r.want[s.typ] && !r.isDone(unitKeys[s.typ]) {
			if err := s.read(); err != nil {
				return nil, err
			}

			if err := r.flush(s.typ); err != nil {
				return nil, err
			}
		}
	}

	if r.want[typeOrganization] || r.want[typeOrgConnection] || r.want[typeOrgMember] || r.want[typeOrgMemberRole] {
		if err := r.organizations(); err != nil {
			return nil, err
		}
	}

	seq := 0

	for _, t := range Types() {
		if !r.want[t.Name] {
			continue
		}

		n := len(r.events[t.Name])
		if r.out != nil {
			n = r.out.count[t.Name]
		}

		for _, e := range r.events[t.Name] {
			seq++
			res.Events = append(res.Events, p.envelope(e, seq))
		}

		res.Counts = append(res.Counts, snapshot.Count{Type: t.Name, N: n, Windowed: t.Windowed && !r.since.IsZero()})
	}

	res.FinishedAt = now()
	res.RateLimited = p.limits.count()

	return res, nil
}

func (r *read) scopes(typ string) []string {
	for _, t := range Types() {
		if t.Name == typ {
			return t.Scopes
		}
	}

	return nil
}

// errStopWalk, returned by a walkPages callback, ends the walk without error.
var errStopWalk = errors.New("stop walking pages")

// walk calls each with every page of results, stamped with the time the page
// arrived, until pages run out. what and typ describe the read for errors.
func walk[C comparable, T, R any](
	r *read, first *core.Page[C, T, R], err error, what, typ string, each func(items []T, at time.Time) error,
) error {
	return walkPages(r, first, err, what, typ, func(p *core.Page[C, T, R], at time.Time) error {
		return each(p.Results, at)
	})
}

// walkPages is walk for callers that need the whole page, such as its reported
// total. each may return errStopWalk to stop before the pages run out.
func walkPages[C comparable, T, R any](
	r *read, first *core.Page[C, T, R], err error, what, typ string, each func(p *core.Page[C, T, R], at time.Time) error,
) error {
	page, seen := first, map[string]bool{}
	for {
		if err != nil {
			return readError(r.ctx, err, what, r.scopes(typ))
		}

		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		// The SDK ends a checkpoint list only on a nil cursor. An empty cursor would
		// be requested as the first page again, so it ends the list here, and a
		// cursor that repeats would never end it.
		cursor, isCheckpoint := "", false
		if c, ok := any(page.RawResponse.Next).(*string); ok && c != nil {
			cursor, isCheckpoint = *c, true
		}

		if seen[cursor] {
			return fmt.Errorf("Auth0 returned the same page of %s twice, so the list cannot be read to its end; "+
				"run the command again", what)
		}

		if cursor != "" {
			seen[cursor] = true
		}

		// A checkpoint page may be empty and still lead to more results.
		if len(page.Results) == 0 && cursor == "" {
			return nil
		}

		if len(page.Results) > 0 {
			if err := each(page, now()); err != nil {
				if errors.Is(err, errStopWalk) {
					return nil
				}

				return err
			}
		}

		if isCheckpoint && cursor == "" {
			return nil
		}

		page, err = page.GetNextPage(r.ctx)
		if errors.Is(err, core.ErrNoPages) {
			return nil
		}
	}
}

func (r *read) connections() error {
	r.report(snapshot.Progress{Message: "reading connections", Step: "connections"})

	take := pageSize
	page, err := r.m.Connections.List(r.ctx, &management.ListConnectionsQueryParameters{Take: &take})

	return walk(r, page, err, "connections", typeConnection, func(items []*management.ConnectionForList, at time.Time) error {
		objs := make([]map[string]any, len(items))

		err := parallel(r.ctx, len(items), func(ctx context.Context, i int) error {
			c := items[i]

			obj, err := toMap(c)
			if err != nil {
				return err
			}

			sub := *r
			sub.ctx = ctx

			clients := []any{}
			cp, err := sub.m.Connections.Clients.Get(ctx, c.GetID(), &management.GetConnectionEnabledClientsRequestParameters{Take: &take})

			if err := walk(&sub, cp, err, "connection clients", typeConnection,
				func(cs []*management.ConnectionEnabledClient, _ time.Time) error {
					for _, cl := range cs {
						clients = append(clients, cl.GetClientID())
					}

					return nil
				}); err != nil {
				return err
			}

			obj["enabled_clients"] = clients
			objs[i] = obj

			return nil
		}, nil)
		if err != nil {
			return err
		}

		for i, c := range items {
			r.add(event{typ: typeConnection, subject: "connection " + c.GetID(), object: objs[i], at: at})
		}

		return nil
	})
}

func (r *read) users() error {
	// Sorting keeps a deletion during the read from shifting a user across a
	// page boundary; the total check below catches the deletion itself.
	perPage, totals, sort := pageSize, true, "user_id:1"
	params := &management.ListUsersRequestParameters{PerPage: &perPage, IncludeTotals: &totals, Sort: &sort}

	windowed := !r.since.IsZero()
	if windowed {
		r.report(snapshot.Progress{
			Message: "reading users changed since " + r.since.UTC().Format("2006-01-02 15:04:05") + " UTC", Step: "users",
		})

		q, engine := "updated_at:["+r.since.UTC().Format("2006-01-02T15:04:05.000Z")+" TO *]", management.SearchEngineVersionsEnumV3
		params.Q, params.SearchEngine = &q, &engine
	} else {
		r.report(snapshot.Progress{Message: "reading users", Step: "users"})
	}

	page, err := r.m.Users.List(r.ctx, params)

	seen := map[string]bool{}
	pages, lastTotal, exportFloor := 0, -1.0, -1

	errDeleted := errors.New("Auth0 users were deleted while the list was being read, so a user may have been skipped; " +
		"run the command again")

	if err := walkPages(r, page, err, "users", typeUser, func(p *core.Page[*int, *management.UserResponseSchema, *management.ListUsersOffsetPaginatedResponseContent], at time.Time) error {
		if p.Response.Total == nil {
			return errors.New("Auth0's user list reported no total, so the read cannot tell whether it is complete; " +
				"run the command again")
		}

		total := p.Response.GetTotal()
		// The list serves no more than maxUsers users and reports no higher a total,
		// so a total at the limit means that many or more. Auth0's export job has no such limit.
		if total >= float64(maxUsers) {
			// The export job has no query, so a search that matches too much cannot fall back to it.
			if windowed {
				return fmt.Errorf("Auth0's user search returns at most 1,000 users and at least that many changed since %s; "+
					"use a later --since or run without it", r.since.UTC().Format(time.RFC3339))
			}

			exportFloor = int(total)

			return errStopWalk
		}

		if total < lastTotal {
			return errDeleted
		}

		lastTotal = total

		for _, u := range p.Results {
			if seen[u.GetUserID()] {
				continue
			}

			seen[u.GetUserID()] = true

			obj, err := toMap(u)
			if err != nil {
				return err
			}

			r.add(event{typ: typeUser, subject: "user " + u.GetUserID(), object: obj, at: at})
		}

		// Auth0 refuses pages past 1,000 users and the SDK would otherwise ask
		// for one more page to find the end.
		pages++
		r.report(snapshot.Progress{Step: "users", Done: len(seen), Total: int(total)})

		if float64(pages*pageSize) >= total {
			return errStopWalk
		}

		return nil
	}); err != nil {
		return err
	}

	if exportFloor >= 0 {
		return r.exportUsers(exportFloor)
	}

	// A deletion that empties the last page ends the walk without the total
	// check ever seeing it, so compare what was read with what was reported.
	if float64(len(seen)) < lastTotal {
		return errDeleted
	}

	return nil
}

// exportUserFields are the fields the export job is asked for: the same keys the
// users list returns. Without them the export has a default subset and flattens
// identities.
var exportUserFields = []string{
	"user_id", "email", "email_verified", "username", "phone_number", "phone_verified", "created_at",
	"updated_at", "identities", "app_metadata", "user_metadata", "picture", "name", "nickname", "multifactor",
	"last_ip", "last_login", "last_password_reset", "logins_count", "blocked", "given_name", "family_name",
}

// exportJobStep and exportDownloadStep name the two stretches of the export in progress reports.
const (
	exportJobStep      = "users (export job)"
	exportDownloadStep = "users (export download)"
)

// exportReportEvery is how many decoded users pass between progress reports of the download.
const exportReportEvery = 1000

// exportPollEvery is how long to wait between polls of the export job.
const exportPollEvery = 2 * time.Second

// exportJobTimeout is how long the export job may stay pending or processing.
const exportJobTimeout = 30 * time.Minute

// exportStallTimeout is how long an export download may go without data.
const exportStallTimeout = 60 * time.Second

// exportUsers reads every user through an export job, for tenants the list
// cannot serve. atLeast is the total the list reported: the tenant has that
// many users or more.
func (r *read) exportUsers(atLeast int) error {
	r.report(snapshot.Progress{Message: "exporting users", Step: exportJobStep})

	// The total may have reached the limit on a later page of the list; the
	// export replaces what the pages gathered.
	delete(r.events, typeUser)
	r.tally.reset(typeUser)

	format := management.JobFileFormatEnumJSON

	fields := make([]*management.CreateExportUsersFields, len(exportUserFields))
	for i, name := range exportUserFields {
		fields[i] = &management.CreateExportUsersFields{Name: name}
	}

	// A job an earlier run started is read again, unless it is gone, failed,
	// or its download location expired.
	if r.recall != nil {
		if id := r.recall(exportJobKey); id != "" {
			restart, err := r.awaitExport(id, atLeast, true)
			if !restart {
				return err
			}
		}
	}

	job, err := r.m.Jobs.UsersExports.Create(r.ctx, &management.CreateExportUsersRequestContent{Format: &format, Fields: fields})
	if err != nil {
		return readError(r.ctx, err, "users export", r.scopes(typeUser))
	}

	if r.remember != nil {
		if err := r.remember(exportJobKey, job.GetID()); err != nil {
			return fmt.Errorf("saving the export job to the work file: %w", err)
		}
	}

	_, err = r.awaitExport(job.GetID(), atLeast, false)

	return err
}

// awaitExport waits for the export job id and reads its file. With resumed set,
// restart is true when the job cannot be used any more (it is gone, failed, or
// its location expired) and a new one should be created.
func (r *read) awaitExport(id string, atLeast int, resumed bool) (restart bool, err error) {
	// For a resumed job the 30 minutes start again here.
	created, lastPercent := r.limits.now(), -1

	for {
		got, err := r.m.Jobs.Get(r.ctx, id)
		if err != nil {
			var notFound *management.NotFoundError
			if resumed && errors.As(err, &notFound) {
				return true, nil
			}

			return false, readError(r.ctx, err, "users export", r.scopes(typeUser))
		}

		switch got.GetStatus() {
		case "completed":
			n, err := r.downloadUsers(got.GetLocation(), now())
			if err != nil {
				var gone errLocationGone
				if resumed && errors.As(err, &gone) {
					return true, nil
				}

				return false, err
			}

			// drift --prune deletes what was not read, so a short file must not pass.
			if n < atLeast {
				short := fmt.Errorf("Auth0's user export has %d users where the list reported %d or more; "+
					"users may have been deleted meanwhile, run the command again", n, atLeast)

				// Forget the job so the next run creates a new one instead of
				// failing on this file again.
				if r.remember != nil {
					if err := r.remember(exportJobKey, ""); err != nil {
						return false, errors.Join(short, fmt.Errorf("forgetting the export job in the work file: %w", err))
					}
				}

				return false, short
			}

			return false, nil
		case "pending", "processing":
			if r.limits.now().Sub(created) >= exportJobTimeout {
				return false, fmt.Errorf("Auth0's user export job was still %s after %s; run the command again", got.GetStatus(), exportJobTimeout)
			}

			switch pct := got.GetPercentageDone(); {
			case got.PercentageDone == nil:
				r.report(snapshot.Progress{
					Message: fmt.Sprintf("exporting users: waiting for Auth0's job (%ds)", int(r.limits.now().Sub(created).Seconds())),
					Step:    exportJobStep,
				})
			case pct != lastPercent:
				lastPercent = pct

				r.report(snapshot.Progress{Message: fmt.Sprintf("exporting users: %d%%", pct), Step: exportJobStep, Done: pct, Total: 100})
			}
		default:
			// An unknown status restarts on resume.
			if resumed {
				return true, nil
			}

			return false, fmt.Errorf("Auth0's user export job ended as %q", got.GetStatus())
		}

		if err := r.wait(r.ctx, exportPollEvery); err != nil {
			return false, err
		}
	}
}

// errLine is what a line of the export that does not decode as a user reports.
var errLine = errors.New("a line is not a JSON user")

// streamReader remembers the first error of the stream other than its end, to
// tell a failing stream from a line that does not decode.
type streamReader struct {
	r   io.Reader
	err error
}

func (s *streamReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && s.err == nil {
		s.err = err
	}

	return n, err
}

// progressReader restarts the stall timer whenever data arrives.
type progressReader struct {
	r     io.Reader
	timer *time.Timer
	every time.Duration
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.timer.Reset(p.every)
	}

	return n, err
}

// downloadUsers streams the export file, one JSON user per line, into events.
// The location is a signed URL on another host: it is fetched without the
// Management client and never appears in an error.
func (r *read) downloadUsers(location string, at time.Time) (int, error) {
	// A download that sends no data for the stall limit is cancelled. There is
	// no limit on the whole download: a large tenant's takes minutes.
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()

	var stalled atomic.Bool

	timer := time.AfterFunc(r.stall, func() {
		stalled.Store(true)
		cancel()
	})
	defer timer.Stop()

	fail := func(err error) (int, error) {
		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return 0, ctxErr
		}

		if stalled.Load() {
			err = fmt.Errorf("no data for %s", r.stall)
		}

		return 0, fmt.Errorf("downloading Auth0's user export failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return fail(errors.New("the job's location is not a URL"))
	}

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		// Do wraps failures in a *url.Error, whose text is the signed URL.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}

		return fail(err)
	}
	defer func() { _ = resp.Body.Close() }()

	timer.Reset(r.stall)

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusForbidden, http.StatusNotFound, http.StatusGone:
			return fail(errLocationGone{resp.StatusCode})
		}

		return fail(fmt.Errorf("HTTP %d", resp.StatusCode))
	}

	// The file is gzip-compressed, unless the host serves it with Content-Encoding
	// gzip (Go's transport has then decompressed it) or uncompressed.
	body := bufio.NewReader(&progressReader{r: resp.Body, timer: timer, every: r.stall})

	var file io.Reader = body

	magic, err := body.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return fail(err)
	}

	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return fail(err)
		}

		file = gz
	}

	zr := &streamReader{r: file}

	seen := map[string]bool{}
	dec := json.NewDecoder(zr)

	for {
		var u management.UserResponseSchema

		if err := dec.Decode(&u); err != nil {
			if errors.Is(err, io.EOF) {
				r.report(snapshot.Progress{Step: exportDownloadStep, Done: len(seen)})

				return len(seen), nil
			}

			// An error of the stream (gzip, the body, the context) is reported as it
			// is. Any other is a decoding error, and the SDK's date parser quotes the
			// raw value, so none of them may reach the error text.
			if zr.err != nil {
				return fail(zr.err)
			}

			return fail(errLine)
		}

		if u.GetUserID() == "" {
			return fail(errLine)
		}

		if seen[u.GetUserID()] {
			continue
		}

		seen[u.GetUserID()] = true

		obj, err := toMap(&u)
		if err != nil {
			return 0, err
		}

		r.add(event{typ: typeUser, subject: "user " + u.GetUserID(), object: obj, at: at})

		// Nothing after this point restarts the download: a failure from here on
		// fails the whole read, which discards what Emit was given.
		if len(r.events[typeUser]) >= flushEvery {
			if err := r.flushUnit(typeUser, false); err != nil {
				return 0, err
			}
		}

		if len(seen)%exportReportEvery == 0 {
			r.report(snapshot.Progress{Step: exportDownloadStep, Done: len(seen)})
		}
	}
}

func (r *read) organizations() error {
	r.report(snapshot.Progress{Message: "reading organizations", Step: "organizations"})

	take := pageSize
	page, err := r.m.Organizations.List(r.ctx, &management.ListOrganizationsRequestParameters{Take: &take})

	var orgs []map[string]any

	err = walk(r, page, err, "organizations", typeOrganization, func(items []*management.Organization, at time.Time) error {
		for _, o := range items {
			obj, err := toMap(o)
			if err != nil {
				return err
			}

			orgs = append(orgs, obj)

			r.add(event{typ: typeOrganization, subject: "organization " + o.GetID(), object: obj, at: at})
		}

		r.report(snapshot.Progress{Step: "organizations", Done: len(orgs)})

		return nil
	})
	if err != nil {
		return err
	}

	if !r.want[typeOrganization] {
		delete(r.events, typeOrganization)
	}

	if r.isDone("organizations") {
		delete(r.events, typeOrganization)
	} else if err := r.flush(typeOrganization); err != nil {
		return err
	}

	if r.want[typeOrgConnection] {
		err := r.each(orgs, "org-connections", "connections of organizations", "reading connections of %d/%d organizations", []string{typeOrgConnection}, func(sub *read, org map[string]any) error {
			return sub.orgConnections(org)
		})
		if err != nil {
			return err
		}
	}

	if r.want[typeOrgMember] || r.want[typeOrgMemberRole] {
		err := r.each(orgs, "org-members", "members of organizations", "reading members of %d/%d organizations", []string{typeOrgMember}, func(sub *read, org map[string]any) error {
			return sub.orgMembers(org)
		})
		if err != nil {
			return err
		}

		// Roles are read with the members but come after all of them.
		if err := r.flush(typeOrgMemberRole); err != nil {
			return err
		}
	}

	return nil
}

func (r *read) orgConnections(org map[string]any) error {
	id, _ := org["id"].(string)
	perPage, totals := pageSize, true
	page, err := r.m.Organizations.EnabledConnections.List(r.ctx, id,
		&management.ListOrganizationConnectionsRequestParameters{PerPage: &perPage, IncludeTotals: &totals})

	read, total := 0.0, 0.0

	if err := walkPages(r, page, err, "organization connections", typeOrgConnection,
		func(p *core.Page[*int, *management.OrganizationConnection, *management.ListOrganizationConnectionsOffsetPaginatedResponseContent], at time.Time) error {
			for _, c := range p.Results {
				raw, err := toMap(c)
				if err != nil {
					return err
				}

				r.add(event{
					typ: typeOrgConnection, subject: fmt.Sprintf("organization %s connection %s", id, c.GetConnectionID()),
					object: orgConnectionObject(org, raw), at: at,
				})
			}

			// Stop when the total is reached. A short page that falls short of it is
			// not the end; an empty one is, and is caught below. With no total, the
			// empty page is the only end.
			read += float64(len(p.Results))
			total = p.Response.GetTotal()

			if total > 0 && read >= total {
				return errStopWalk
			}

			return nil
		}); err != nil {
		return err
	}

	// Pages ran out before the total: something changed, or Auth0 skipped some.
	if read < total {
		return errors.New("Auth0 organization connections changed while the list was being read, " +
			"so a connection may have been skipped; run the command again")
	}

	return nil
}

func (r *read) orgMembers(org map[string]any) error {
	id, _ := org["id"].(string)
	take := pageSize
	params := &management.ListOrganizationMembersRequestParameters{Take: &take}

	if r.want[typeOrgMemberRole] {
		fields, include := "user_id,email,name,picture,roles", true
		params.Fields, params.IncludeFields = &fields, &include
	}

	page, err := r.m.Organizations.Members.List(r.ctx, id, params)

	// Asking for roles needs the role scope on this same read.
	scopesOf := typeOrgMember
	if r.want[typeOrgMemberRole] {
		scopesOf = typeOrgMemberRole
	}

	return walk(r, page, err, "organization members", scopesOf,
		func(items []*management.OrganizationMember, at time.Time) error {
			for _, m := range items {
				user, err := toMap(m)
				if err != nil {
					return err
				}

				roles, _ := user["roles"].([]any)
				delete(user, "roles")

				subject := fmt.Sprintf("organization %s member %s", id, m.GetUserID())

				if r.want[typeOrgMember] {
					r.add(event{typ: typeOrgMember, subject: subject, at: at,
						object: map[string]any{"organization": orgRef(org), "user": user}})
				}

				if !r.want[typeOrgMemberRole] {
					continue
				}

				for _, role := range roles {
					rm, _ := role.(map[string]any)
					r.add(event{typ: typeOrgMemberRole, subject: fmt.Sprintf("%s role %v", subject, rm["id"]), at: at,
						object: map[string]any{
							"organization": orgRef(org), "user": map[string]any{"user_id": m.GetUserID()}, "role": rm,
						}})
				}
			}

			return nil
		})
}
