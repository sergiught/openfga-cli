package auth0

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
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

	return &Provider{m: m, source: host, tenant: tenant, limits: limits}, nil
}

// Name is the --provider value.
func (p *Provider) Name() string { return "auth0" }

// Label is the display name.
func (p *Provider) Label() string { return "Auth0" }

// Types lists the snapshot event types this provider produces.
func (p *Provider) Types() []snapshot.Type { return Types() }

// read is one Snapshot call's state.
type read struct {
	*Provider

	ctx      context.Context //nolint:containedctx // scoped to one call
	want     map[string]bool
	progress func(string)
	sayMu    *sync.Mutex // progress is called from several goroutines, one at a time
	events   map[string][]event
}

func (r *read) say(msg string) {
	if r.progress != nil {
		r.sayMu.Lock()
		defer r.sayMu.Unlock()

		r.progress(msg)
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
func (r *read) each(orgs []map[string]any, progress string, task func(sub *read, org map[string]any) error) error {
	subs := make([]*read, len(orgs))

	err := parallel(r.ctx, len(orgs), func(ctx context.Context, i int) error {
		sub := *r
		sub.ctx, sub.events = ctx, map[string][]event{}
		subs[i] = &sub

		return task(&sub, orgs[i])
	}, func(done int) {
		r.say(fmt.Sprintf(progress, done, len(orgs)))
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

func (r *read) add(e event) { r.events[e.typ] = append(r.events[e.typ], e) }

// Snapshot reads the given types completely or returns an error.
func (p *Provider) Snapshot(ctx context.Context, types []string, progress func(string)) (*snapshot.Result, error) {
	r := &read{Provider: p, ctx: ctx, want: map[string]bool{}, progress: progress, sayMu: &sync.Mutex{}, events: map[string][]event{}}

	known := map[string]bool{}
	for _, t := range Types() {
		known[t.Name] = true
	}

	for _, t := range types {
		if !known[t] {
			return nil, fmt.Errorf("unknown snapshot type %q", t)
		}

		r.want[t] = true
	}

	p.limits.begin(func() { r.say("waiting on Auth0's rate limit") })

	res := &snapshot.Result{Source: p.source, StartedAt: now()}

	steps := []struct {
		typ  string
		read func() error
	}{
		{typeConnection, r.connections},
		{typeUser, r.users},
	}

	for _, s := range steps {
		if r.want[s.typ] {
			if err := s.read(); err != nil {
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

		for _, e := range r.events[t.Name] {
			seq++
			res.Events = append(res.Events, p.envelope(e, seq))
		}

		res.Counts = append(res.Counts, snapshot.Count{Type: t.Name, N: len(r.events[t.Name])})
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
	page := first
	for {
		if err != nil {
			return readError(r.ctx, err, what, r.scopes(typ))
		}

		if ctxErr := r.ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		if len(page.Results) == 0 {
			return nil
		}

		if err := each(page, now()); err != nil {
			if errors.Is(err, errStopWalk) {
				return nil
			}

			return err
		}

		page, err = page.GetNextPage(r.ctx)
		if errors.Is(err, core.ErrNoPages) {
			return nil
		}
	}
}

func (r *read) connections() error {
	r.say("reading connections")

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
	r.say("reading users")

	// Sorting keeps a deletion during the read from shifting a user across a
	// page boundary; the total check below catches the deletion itself.
	perPage, totals, sort := pageSize, true, "user_id:1"
	page, err := r.m.Users.List(r.ctx, &management.ListUsersRequestParameters{PerPage: &perPage, IncludeTotals: &totals, Sort: &sort})

	seen := map[string]bool{}
	pages, lastTotal := 0, -1.0

	errDeleted := errors.New("Auth0 users were deleted while the list was being read, so a user may have been skipped; " +
		"run the command again")

	if err := walkPages(r, page, err, "users", typeUser, func(p *core.Page[*int, *management.UserResponseSchema, *management.ListUsersOffsetPaginatedResponseContent], at time.Time) error {
		total := p.Response.GetTotal()
		if total > maxUsers {
			return fmt.Errorf("Auth0 lists at most 1,000 users through this API and this tenant has %.0f; "+
				"tenants this large are not supported yet", total)
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
		if float64(pages*pageSize) >= total {
			return errStopWalk
		}

		return nil
	}); err != nil {
		return err
	}

	// A deletion that empties the last page ends the walk without the total
	// check ever seeing it, so compare what was read with what was reported.
	if float64(len(seen)) < math.Min(lastTotal, maxUsers) {
		return errDeleted
	}

	return nil
}

func (r *read) organizations() error {
	r.say("reading organizations")

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

		return nil
	})
	if err != nil {
		return err
	}

	if !r.want[typeOrganization] {
		delete(r.events, typeOrganization)
	}

	if r.want[typeOrgConnection] {
		err := r.each(orgs, "reading connections of %d/%d organizations", func(sub *read, org map[string]any) error {
			return sub.orgConnections(org)
		})
		if err != nil {
			return err
		}
	}

	if r.want[typeOrgMember] || r.want[typeOrgMemberRole] {
		err := r.each(orgs, "reading members of %d/%d organizations", func(sub *read, org map[string]any) error {
			return sub.orgMembers(org)
		})
		if err != nil {
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

	return walkPages(r, page, err, "organization connections", typeOrgConnection,
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

			// A short page is the last one; asking for the next would cost a request.
			// Auth0 fills every page but the last at per_page=100, as users() relies on.
			if len(p.Results) < pageSize {
				return errStopWalk
			}

			return nil
		})
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
