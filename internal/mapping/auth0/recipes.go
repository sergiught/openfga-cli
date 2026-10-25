package auth0

import "github.com/sergiught/openfga-cli/internal/mapping"

// Recipe is what the workspace knows about mapping one event: the rule it
// pre-loads, the model that rule assumes, and the sentences explaining the
// pairing. A recipe is teaching material first and a starting point second —
// the user edits it after loading, so it must be correct rather than clever.
//
// Every event has a Recipe. An event that implies no relationship change has
// one whose Rule is empty and whose Explain says why: "no mapping" is a lesson,
// not a missing entry.
type Recipe struct {
	Explain string
	// Note is the short reason a recipe maps nothing, for the event picker's
	// row. Mapped recipes leave it empty: their row counts what the rule
	// produces, which is the more useful thing to know about them.
	//
	// Short is a hard constraint, not a preference: the row is the event type
	// plus this, and the longest type is organization.connection.updated, which
	// leaves a third of a narrow picker. A note that has to be truncated to fit
	// cannot do the explaining it is here for.
	//
	// It exists because the count alone cannot say whose fault an empty one is.
	// A row reading "user.created · no tuples" beside a status bar reading "4
	// types" was read as the loaded model coming up short, which sent the user
	// looking for the missing type. Nothing is missing — the event carries no
	// relationship — and the row now says so before it is picked rather than on
	// the screen after.
	Note string
	// Warn is a gap this recipe leaves, shown beside the rule rather than
	// buried in Explain. These are the failures a user discovers in
	// production: an event Auth0 never sends, a tuple the rule cannot reach,
	// or a case it deliberately skips. A recipe with no such gap leaves it
	// empty.
	Warn     string
	Rule     mapping.Rule
	Requires []mapping.Requirement
}

// Maps reports whether this recipe produces anything. It is the single test the
// recipe screen branches on, so the two empty classes cannot drift apart from
// the events that belong to them.
func (r Recipe) Maps() bool {
	if r.Rule.Iterator != nil && len(r.Rule.Iterator.Tuples) > 0 {
		return true
	}
	return len(r.Rule.Tuples) > 0 || len(r.Rule.Filters) > 0
}

// Shared DSL fragments, so each type is spelled once. These are shown on the
// recipe screen, never parsed, so a fragment may name a userset whose type is
// declared in another fragment — model.fga is where they all have to agree.
const (
	dslUser        = "type user"
	dslConnection  = "type connection"
	dslOrgMember   = "type organization\n  relations\n    define member: [user, group#member]"
	dslOrgRole     = "type role\n  relations\n    define assignee: [user]"
	dslOrgConn     = "type organization\n  relations\n    define connection: [connection]"
	dslGroupMember = "type group\n  relations\n    define member: [user, group#member, connection#identity]"
	dslGroupConn   = "type group\n  relations\n    define connection: [connection]"
	dslConnIdent   = "type connection\n  relations\n    define identity: [user]"
	dslConnClient  = "type connection\n  relations\n    define enabled_client: [client]"
	dslOrgTenant   = "type organization\n  relations\n    define tenant: [tenant]"
	dslConnTenant  = "type connection\n  relations\n    define tenant: [tenant]"
	dslClient      = "type client"
	dslTenant      = "type tenant"
	dslTenantMem   = "type tenant\n  relations\n    define member: [user]"
	dslPlanAssign  = "type plan\n  relations\n    define assignee: [user]"
	dslOrgBare     = "type organization"
	dslGroupBare   = "type group"
	dslRoleBare    = "type role"
)

// tmplOrgID and friends are payload paths, verified against the embedded
// samples. Ids are used for identity, never display names: names change.
const (
	tmplOrgID   = "organization:{{ input.data.object.organization.id }}"
	tmplOrgUser = "user:{{ fga_escape(input.data.object.user.user_id) }}"
	tmplGroupID = "group:{{ input.data.object.group.id }}"
	tmplConnID  = "connection:{{ input.data.object.connection.id }}"

	// The user events key on the top-level user_id — "auth0|507f…" — which is
	// the only id every other event agrees with. identities[].user_id is the
	// provider's own subject, carries no provider prefix, and the schema types
	// it as string *or* integer, so it can arrive as a JSON number.
	tmplUserID = "user:{{ fga_escape(input.data.object.user_id) }}"

	// One role object per role per organization. fga_escape leaves | alone,
	// which is what makes a compound key like this legal in the first place.
	tmplRoleID = "role:{{ input.data.object.organization.id }}|{{ input.data.object.role.id }}"

	// The creation events name their own object at the root of the payload, and
	// a group names its connection as a flat connection_id rather than the
	// nested object the organization events use.
	tmplNewGroup = "group:{{ input.data.object.id }}"
	tmplGroupCon = "connection:{{ input.data.object.connection_id }}"
	tmplGroupOrg = "organization:{{ input.data.object.organization_id }}"
	tmplNewOrg   = "organization:{{ input.data.object.id }}"
	tmplNewConn  = "connection:{{ input.data.object.id }}"

	// A group's own members, as a set. This is the user side of a tuple, not the
	// object side: it says "everyone in this group", which is how an
	// organization-scoped group joins an organization in one tuple.
	tmplGroupSet = "group:{{ input.data.object.id }}#member"

	// A group member is a user or a whole connection, told apart by member_type.
	tmplGroupMem = "user:{{ fga_escape(input.data.object.member.id) }}"
	tmplMemConn  = "connection:{{ input.data.object.member.connection_id }}#identity"

	// The tenant is on the envelope, not in data. data.context is optional on
	// every event type in Auth0's schema, while a0tenant is required on all of
	// them — reading the tenant from context is how a rule that works in testing
	// fails in production.
	tmplTenant = "tenant:{{ fga_escape(input.a0tenant) }}"

	// app_metadata is where Auth0 lets you keep facts of your own on a user, and
	// a plan or tier is the one most tenants put there. It is the only thing a
	// creation event says about what the new user may do, as opposed to who they
	// are — so it is the one worth a tuple.
	tmplPlan = "plan:{{ fga_escape(input.data.object.app_metadata.plan) }}"
)

// Gates on the tagged unions in the payloads. Auth0 discriminates a group by
// what it is scoped to and a group member by what kind of thing it is, and a
// rule that reads one branch's field without checking the tag fails outright on
// the others: the field is simply absent, and an expression over a missing path
// is an error, not a blank.
const (
	gateScopeConn = `input.data.object.type == "connection"`
	// The move recipe reads connection_id off both objects, so both have to be
	// connection-scoped before either is touched. Without this a group rescoped
	// from an organization fails the event on a nil previous_object.connection_id
	// rather than being skipped.
	gatePrevConn   = `input.data.previous_object.type == "connection"`
	gateScopeOrg   = `input.data.object.type == "organization"`
	gateMemberUser = `input.data.object.member.member_type == "user"`
	gateMemberConn = `input.data.object.member.member_type == "connection"`

	// app_metadata is optional, and so is anything in it. `in` is the presence
	// test this dialect has — CEL's has() macro is not registered — and it reads
	// false rather than failing when app_metadata is missing altogether, so one
	// clause covers both the empty map and the absent one.
	gatePlan = `"plan" in input.data.object.app_metadata`

	// is_enabled says whether a connection grants anything, and Auth0 omits it
	// from some organization-connection payloads. Reading a missing path is an
	// error rather than a blank, so the presence test comes first wherever the
	// flag can be absent.
	gateEnabled    = `input.data.object.is_enabled`
	gateHasEnabled = `"is_enabled" in input.data.object`
)

func when(typ string) string { return `input.type == "` + typ + `"` }

// membership builds one of the single-tuple relationship recipes. write says
// whether the event grants a relationship or removes it: granting needs no
// action at all, removing sets "delete" once at the rule level rather than on
// the tuple. Either level is legal; mapper rejects a rule that sets both.
func membership(typ, explain, user, relation, object string, write bool, reqs []mapping.Requirement) Recipe {
	r := Recipe{Explain: explain, Requires: reqs}
	r.Rule = mapping.Rule{
		Name:   typ,
		When:   when(typ),
		Tuples: []mapping.Tuple{{User: user, Relation: relation, Object: object}},
	}
	if !write {
		r.Rule.Action = "delete"
	}
	return r
}

// union builds a recipe whose tuples are the branches of a tagged union, each
// gated by its own when. Exactly one fires per event; the others are skipped
// rather than failed, which is the whole reason the gate is on the tuple and
// not on the rule.
func union(typ, explain string, tuples []mapping.Tuple, write bool, reqs []mapping.Requirement) Recipe {
	r := Recipe{Explain: explain, Requires: reqs}
	r.Rule = mapping.Rule{Name: typ, When: when(typ), Tuples: tuples}
	if !write {
		r.Rule.Action = "delete"
	}
	return r
}

// cleanup builds one of the four deletion recipes. A tuple filter's object is
// mandatory — mapper rejects a blank one — while user and relation are
// optional. So the filter that names an object in full is the one that is
// doomed to the one it names; the ones that pin a user instead leave the object
// a bare type prefix, which is what "every organization" looks like.
func cleanup(typ, explain string, filters []mapping.TupleFilter, reqs []mapping.Requirement) Recipe {
	return Recipe{
		Explain: explain,
		Rule: mapping.Rule{
			Name:    typ,
			When:    when(typ),
			Filters: filters,
		},
		Requires: reqs,
	}
}

// lifecycleWarnings are the gaps a recipe leaves: tuples it cannot keep current
// or cannot remove, because Auth0 sends no event for them, mapper's limits stop
// the rule reaching them, or a key differs between events. They live in one
// table rather than in each recipe's prose so the recipe screen can show them
// apart from the explanation — and when Auth0 starts emitting a missing event,
// the fix is to delete a line here.
var lifecycleWarnings = map[string]string{
	"user.created": "Changes after sign-up are missed. A plan change arrives as user.updated, whose " +
		"recipe maps identities, not plans: add a rule that deletes the old plan tuple and writes the " +
		"new one. And identities are mapped only by user.updated, so a new user has no identity tuple " +
		"until one reports a change to them.",

	"user.updated": "Identities name their connection (Username-Password-Authentication), while every " +
		"other recipe uses its id (con_…). So these tuples don't link up with the rest: " +
		"group.member.added's connection:con_…#identity matches no one, and connection.deleted doesn't " +
		"remove them. Use one key everywhere.\n\n" +
		"If identities becomes empty the rule is skipped: a filter with nothing to keep fails the event.",

	"user.deleted": "mapper allows three filters per rule, so a user's role, plan and group tuples stay. " +
		"Roles and plans grant nothing alone (admin needs member; can_export needs can_edit). Groups may " +
		"key members by email rather than user_id (see group.member.added). To remove plans, and groups " +
		"if yours use the user_id, add a second rule with the same when and filters on \"plan:\" and " +
		"\"group:\".",

	"organization.deleted": "Two kinds of tuple stay. Role assignments (role:org_…|rol_…) can't be " +
		"matched, because a filter can't match part of an id; they grant nothing once the memberships " +
		"are gone. Tuples where the organization is the user, such as a document's organization, need " +
		"a filter for each of your types, e.g. {user: organization:…, object: \"document:\"}.",

	"organization.member.deleted": "Auth0 sends no organization.member.role.deleted when someone leaves, " +
		"and no filter can reach their role tuples: a filter can't match part of an id like " +
		"org_…|rol_…. They grant nothing, since admin requires member, but stay in the store until you " +
		"clean them up.",

	"group.deleted": "Tuples that give the group access elsewhere stay: a document shared with " +
		"group:…#member, or a group nested in another group (no Auth0 event writes those, but the model " +
		"allows them). mapper allows one more filter, for one of them.",

	"group.member.added": "member.id is the id the group knows the member by, often an email (as in this " +
		"sample) rather than the auth0|… user_id the other recipes use. Then one person has two ids in " +
		"your store; map both to one.",

	"connection.deleted": "Two kinds of tuple stay. Group members written as connection:con_…#identity " +
		"would need a fourth filter, and mapper allows three. Identity tuples from user.updated use the " +
		"connection's name, not its id, so no filter here matches them.",

	"connection.updated": "If the list becomes empty the rule is skipped, so the last application's " +
		"tuple stays: a filter with nothing to keep fails the event.",
}

func recipeFor(typ string) Recipe {
	r := recipeBody(typ)
	r.Warn = lifecycleWarnings[typ]
	return r
}

func recipeBody(typ string) Recipe {
	switch typ {

	// --- relationship recipes ---

	case "organization.member.added":
		return membership(typ,
			"A user joined an organization. This writes one tuple making them a member. A group can be a member too (group#member): group.created writes that for organization groups, so their members need no tuple of their own.",
			tmplOrgUser, "member", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.deleted":
		return membership(typ,
			"A user left an organization. This deletes their membership tuple.",
			tmplOrgUser, "member", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.role.assigned":
		return membership(typ,
			"A member was given a role in an organization. Roles are objects, not relations: users create them at runtime, and a relation name can't contain a space. Each is keyed org_…|rol_…, so the same role in two organizations stays separate.\n\nAuth0 never says what a role allows. Write that once, by hand:\n\n  role:org_…|rol_admin#assignee  role_admin  organization:org_…\n\nAfter that, this rule and organization.member.role.deleted keep it up to date.",
			tmplOrgUser, "assignee", tmplRoleID, true,
			[]mapping.Requirement{{Type: "role", Relation: "assignee", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgRole}})

	case "organization.member.role.deleted":
		return membership(typ,
			"A role was removed from a member. This deletes the tuple organization.member.role.assigned wrote. The tuple you wrote by hand to say what the role allows is untouched.",
			tmplOrgUser, "assignee", tmplRoleID, false,
			[]mapping.Requirement{{Type: "role", Relation: "assignee", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgRole}})

	case "group.member.added":
		return union(typ,
			"A member joined a group. A member is a user or a whole connection (member_type), so the rule has a tuple for each and exactly one fires. A connection is written as connection:con_…#identity: everyone with an identity in it, without a tuple per person.",
			[]mapping.Tuple{
				{User: tmplGroupMem, Relation: "member", Object: tmplGroupID, When: gateMemberUser},
				{User: tmplMemConn, Relation: "member", Object: tmplGroupID, When: gateMemberConn},
			}, true,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent + "\n\n" + dslGroupMember}})

	case "group.member.deleted":
		return union(typ,
			"A member left a group. This deletes whichever tuple group.member.added wrote, checking member_type the same way.",
			[]mapping.Tuple{
				{User: tmplGroupMem, Relation: "member", Object: tmplGroupID, When: gateMemberUser},
				{User: tmplMemConn, Relation: "member", Object: tmplGroupID, When: gateMemberConn},
			}, false,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent + "\n\n" + dslGroupMember}})

	case "organization.connection.added":
		r := membership(typ,
			"A connection was added to an organization. This writes one tuple linking them, but only if is_enabled is true: a disabled connection grants nothing. If it's enabled later, organization.connection.updated writes the tuple.",
			tmplConnID, "connection", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})
		r.Rule.Tuples[0].When = gateEnabled
		return r

	case "organization.connection.removed":
		return membership(typ,
			"A connection was removed from an organization. This deletes the tuple linking them.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	// --- deletion recipes, which sweep with filters rather than tuples ---

	case "user.deleted":
		return cleanup(typ,
			"A user was deleted. The event doesn't say what they belonged to, so each filter deletes every tuple linking this user to one type: organization, tenant and connection.",
			[]mapping.TupleFilter{
				{User: tmplUserID, Object: "organization:", Action: "delete"},
				{User: tmplUserID, Object: "tenant:", Action: "delete"},
				{User: tmplUserID, Object: "connection:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", DSL: dslUser + "\n\n" + dslOrgBare},
				{Type: "tenant", DSL: dslTenant},
				{Type: "connection", DSL: dslConnection},
			})

	case "organization.deleted":
		return cleanup(typ,
			"An organization was deleted. This deletes every tuple on it: members, tenant, connections and admin roles.",
			[]mapping.TupleFilter{{Object: "organization:{{ input.data.object.id }}", Action: "delete"}},
			[]mapping.Requirement{{Type: "organization", DSL: dslOrgBare}})

	case "group.deleted":
		return cleanup(typ,
			"A group was deleted. This deletes every tuple on the group, and its membership in any organization.",
			[]mapping.TupleFilter{
				{Object: "group:{{ input.data.object.id }}", Action: "delete"},
				{User: tmplGroupSet, Object: "organization:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "group", Relation: "member", UserTypes: []string{"user"},
					DSL: dslUser + "\n\n" + dslGroupMember},
				{Type: "organization", Relation: "member", UserTypes: []string{"user"},
					DSL: dslGroupBare + "\n\n" + dslOrgMember},
			})

	case "connection.deleted":
		return cleanup(typ,
			"A connection was deleted. The first two filters delete its links to organizations and groups, where the connection is the user. The third deletes every tuple on the connection itself: its tenant and clients.",
			[]mapping.TupleFilter{
				{User: "connection:{{ input.data.object.id }}", Object: "organization:", Action: "delete"},
				{User: "connection:{{ input.data.object.id }}", Object: "group:", Action: "delete"},
				{Object: "connection:{{ input.data.object.id }}", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslOrgConn},
				{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslGroupConn},
			})

	case "organization.connection.updated":
		return union(typ,
			"An organization's connection settings changed. Only is_enabled affects access: this writes the tuple when it's true and deletes it when it's false. Both directions matter, because organization.connection.added fires only once, so nothing else would add a re-enabled connection back.\n\nBoth gates check is_enabled is present first: Auth0 sometimes leaves it out, and negating a missing value fails the event.",
			[]mapping.Tuple{
				{User: tmplConnID, Relation: "connection", Object: tmplOrgID, Action: "delete",
					When: gateHasEnabled + " && !input.data.object.is_enabled"},
				{User: tmplConnID, Relation: "connection", Object: tmplOrgID,
					When: gateHasEnabled + " && input.data.object.is_enabled"},
			}, true,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	// --- one creation event that creates a relationship too ---

	case "group.created":
		return union(typ,
			"A group was created. Auth0 scopes a group to a connection, an organization or the whole tenant, named in type. One tuple is written for the first two:\n\n- connection: the group belongs to the connection.\n- organization: the group's members (group:…#member) become members of the organization, and stay in sync as people join and leave.\n\nA tenant group writes nothing: every group is already in the tenant.",
			[]mapping.Tuple{
				{User: tmplGroupCon, Relation: "connection", Object: tmplNewGroup, When: gateScopeConn},
				{User: tmplGroupSet, Relation: "member", Object: tmplGroupOrg, When: gateScopeOrg},
			}, true,
			[]mapping.Requirement{
				{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslGroupConn},
				{Type: "organization", Relation: "member", UserTypes: []string{"user"},
					DSL: dslGroupBare + "\n\n" + dslOrgMember},
			})

	case "user.created":
		return Recipe{
			Explain: "A user was created. This writes up to two tuples: the user becomes a member of the tenant, which is what a check for \"any signed-in user\" asks, and, if app_metadata has a plan, the user becomes that plan's assignee. A user with no plan gets only the first.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
				Tuples: []mapping.Tuple{
					{User: tmplUserID, Relation: "member", Object: tmplTenant},
					{User: tmplUserID, Relation: "assignee", Object: tmplPlan, When: gatePlan},
				},
			},
			Requires: []mapping.Requirement{
				{Type: "tenant", Relation: "member", UserTypes: []string{"user"},
					DSL: dslUser + "\n\n" + dslTenantMem},
				{Type: "plan", Relation: "assignee", UserTypes: []string{"user"},
					DSL: dslUser + "\n\n" + dslPlanAssign},
			},
		}

	case "organization.created":
		return membership(typ,
			"An organization was created. Its only relationship so far is its tenant, so this writes one tuple linking them. Do the same for your own resources: write the link to the parent when the object is created.\n\nThe tenant comes from a0tenant, which every event carries, not data.context, which is optional.",
			tmplTenant, "tenant", tmplNewOrg, true,
			[]mapping.Requirement{{Type: "organization", Relation: "tenant", UserTypes: []string{"tenant"},
				DSL: dslTenant + "\n\n" + dslOrgTenant}})

	// --- a creation event whose relationships are nested in an array ---

	case "connection.created":
		return Recipe{
			Explain: "A connection was created. This writes one tuple linking it to its tenant, and one per application it's enabled for (enabled_clients). The list holds plain client ids, so each item is {{ client }}, not {{ client.id }}.\n\nAuth0 has deprecated enabled_clients on its connections API, but the event still carries it.",
			Rule: mapping.Rule{
				Name:   typ,
				When:   when(typ),
				Tuples: []mapping.Tuple{{User: tmplTenant, Relation: "tenant", Object: tmplNewConn}},
				Iterator: &mapping.Iterator{
					Source: "input.data.object.enabled_clients",
					As:     "client",
					Tuples: []mapping.Tuple{{
						User:     "client:{{ fga_escape(client) }}",
						Relation: "enabled_client",
						Object:   tmplNewConn,
					}},
				},
			},
			Requires: []mapping.Requirement{
				{Type: "connection", Relation: "tenant", UserTypes: []string{"tenant"},
					DSL: dslTenant + "\n\n" + dslConnTenant},
				{Type: "connection", Relation: "enabled_client", UserTypes: []string{"client"},
					DSL: dslClient + "\n\n" + dslConnClient},
			},
		}

	// --- update events that move a relationship rather than an attribute ---

	case "user.updated":
		return Recipe{
			Explain: "A user's profile changed. When their linked logins (identities) change, this writes one " +
				"tuple per identity, and the filter deletes any identity tuple the new list no longer has, so " +
				"unlinking a login removes its tuple.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ) + " && input.data.object.identities != input.data.previous_object.identities" +
					" && len(input.data.object.identities) > 0",
				Filters: []mapping.TupleFilter{{User: tmplUserID, Relation: "identity", Object: "connection:"}},
				Iterator: &mapping.Iterator{
					Source: "input.data.object.identities",
					As:     "identity",
					Tuples: []mapping.Tuple{{
						User:     tmplUserID,
						Relation: "identity",
						Object:   "connection:{{ fga_escape(identity.connection) }}",
					}},
				},
			},
			Requires: []mapping.Requirement{{Type: "connection", Relation: "identity", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent}},
		}

	case "group.updated":
		return Recipe{
			Explain: "A group changed. If it moved to another connection, this deletes the old connection tuple and writes the new one. Any other change, such as a rename, writes nothing, and so does a group that isn't scoped to a connection.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
				Tuples: []mapping.Tuple{
					{
						User:     "connection:{{ input.data.previous_object.connection_id }}",
						Relation: "connection",
						Object:   tmplNewGroup,
						Action:   "delete",
						When:     gateScopeConn + " && " + gatePrevConn + " && input.data.object.connection_id != input.data.previous_object.connection_id",
					},
					{
						User:     tmplGroupCon,
						Relation: "connection",
						Object:   tmplNewGroup,
						When:     gateScopeConn + " && " + gatePrevConn + " && input.data.object.connection_id != input.data.previous_object.connection_id",
					},
				},
			},
			Requires: []mapping.Requirement{{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslGroupConn}},
		}

	case "connection.updated":
		return Recipe{
			Explain: "A connection's settings changed. When enabled_clients changed, this writes one tuple per " +
				"application in the new list, and the filter deletes the tuples for applications no longer " +
				"in it.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ) + " && input.data.object.enabled_clients != input.data.previous_object.enabled_clients" +
					" && len(input.data.object.enabled_clients) > 0",
				Filters: []mapping.TupleFilter{{Relation: "enabled_client", Object: tmplNewConn}},
				Iterator: &mapping.Iterator{
					Source: "input.data.object.enabled_clients",
					As:     "client",
					Tuples: []mapping.Tuple{{
						User:     "client:{{ fga_escape(client) }}",
						Relation: "enabled_client",
						Object:   tmplNewConn,
					}},
				},
			},
			Requires: []mapping.Requirement{{Type: "connection", Relation: "enabled_client", UserTypes: []string{"client"},
				DSL: dslClient + "\n\n" + dslConnClient}},
		}

	// --- no mapping: the one event with nothing relational in it ---
	//
	// organization.updated, not a whole branch. Every other event in the
	// catalog turned out to carry a relationship somewhere — often beside the
	// object rather than inside it, as the tenant is — and this one genuinely
	// does not: an id, a name, a display name, and some branding colours.
	//
	// So the note states the event's own fact rather than a law about update
	// events, of which the other three map perfectly well.
	default:
		return Recipe{Note: "attributes only", Explain: "Nothing to write. This event carries only attributes (name, display name, branding), not relationships. If your model treats one as a relationship, write a rule that compares data.object with data.previous_object, as the other update recipes do."}
	}
}
