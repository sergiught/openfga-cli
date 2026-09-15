package auth0

import "github.com/sergiught/openfga-cli/internal/mapping"

// Recipe is what the wizard knows about mapping one event: the rule it
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
	// Warn is a hole in Auth0's event set that this recipe cannot close, shown
	// beside the rule rather than buried in Explain. These are the failures a
	// user discovers in production: an event Auth0 never sends, or a case the
	// rule deliberately skips. A recipe with no such hole leaves it empty.
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
	dslGroupMember = "type group\n  relations\n    define member: [user, connection#identity]"
	dslGroupConn   = "type group\n  relations\n    define connection: [connection]"
	dslConnIdent   = "type connection\n  relations\n    define identity: [user]"
	dslConnClient  = "type connection\n  relations\n    define enabled_client: [client]"
	dslOrgTenant   = "type organization\n  relations\n    define tenant: [tenant]"
	dslConnTenant  = "type connection\n  relations\n    define tenant: [tenant]"
	dslClient      = "type client"
	dslTenant      = "type tenant"
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
)

// Gates on the tagged unions in the payloads. Auth0 discriminates a group by
// what it is scoped to and a group member by what kind of thing it is, and a
// rule that reads one branch's field without checking the tag fails outright on
// the others: the field is simply absent, and an expression over a missing path
// is an error, not a blank.
const (
	gateScopeConn  = `input.data.object.type == "connection"`
	gateScopeOrg   = `input.data.object.type == "organization"`
	gateMemberUser = `input.data.object.member.member_type == "user"`
	gateMemberConn = `input.data.object.member.member_type == "connection"`
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

// lifecycleWarnings are the gaps Auth0 leaves that no rule can fill. They live
// in one table rather than in each recipe's prose because they are claims about
// Auth0's behaviour, not about the mapping — when Auth0 starts emitting one of
// these events, the fix is to delete a line here.
var lifecycleWarnings = map[string]string{
	"organization.member.deleted": "Auth0 sends no organization.member.role.deleted when someone " +
		"leaves, so their role assignments outlive the membership. No filter can reach them: roles " +
		"are keyed org_…|rol_…, and a filter matches a whole object or a bare type: prefix, never a " +
		"partial id. The shipped model defines admin as `role_admin and member`, so the leftovers " +
		"grant nothing — but they stay in the store until you reconcile.",

	"organization.deleted": "Roles scoped to this organization are left behind, for the same reason " +
		"organization.member.deleted leaves them: role:org_…|rol_… is neither a whole object this " +
		"event knows nor a bare type: prefix. They grant nothing once the memberships are gone.",

	"group.deleted": "The shipped model allows a group to be a member of another group, but no Auth0 " +
		"event writes one — member_type is a closed union of user and connection. If your application " +
		"writes nested groups itself, add a third filter sweeping group:…#member out of group: too.",

	"user.updated": "If identities ever arrives empty this rule is skipped rather than clearing the " +
		"list. mapper treats an empty desired state as a mistake and fails the whole event, so " +
		"skipping is the safer of the two.",

	"connection.updated": "If enabled_clients arrives empty this rule is skipped rather than clearing " +
		"the list, so removing the last application leaves its tuple behind. An empty desired " +
		"state fails the whole event in mapper.",
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
			"A user joined an organization. This writes one tuple: the user becomes a member of the organization.\n\nThe model lets a whole group be a member too — group#member on the same relation — which is what group.created writes for an organization-scoped group. One tuple then covers everyone in it, and Auth0 adding a person to the group keeps it true without reaching this rule at all.",
			tmplOrgUser, "member", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.deleted":
		return membership(typ,
			"A user left an organization. This deletes the tuple the added event wrote.",
			tmplOrgUser, "member", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.role.assigned":
		return membership(typ,
			"A member was granted a role inside an organization.\n\nThe role is an object, not a relation. Your users invent role names at runtime, and a relation named from role.name breaks the first time somebody calls one \"Billing Manager\" — a relation name cannot contain a space. OpenFGA's rule of thumb is the one to keep: if end-users can define it, it goes in tuples; if it is built into your application, it goes in the model.\n\nThe object is keyed org_…|rol_…, because the same role in two organizations must not be the same object. What the role *permits* is the one thing Auth0 never tells you — you say that once, by hand:\n\n  role:org_…|rol_admin#assignee  role_admin  organization:org_…\n\nAfter that this rule and the revocation below keep it true on their own.",
			tmplOrgUser, "assignee", tmplRoleID, true,
			[]mapping.Requirement{{Type: "role", Relation: "assignee", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgRole}})

	case "organization.member.role.deleted":
		return membership(typ,
			"A role was taken away inside an organization. This deletes the tuple the assigned event wrote, matching it on the same org_…|rol_… key.\n\nThe tuple that says what the role means — the one you wrote by hand on the organization — is untouched, which is the point of keeping roles in their own type: revoking a person's role never risks the definition of the role.",
			tmplOrgUser, "assignee", tmplRoleID, false,
			[]mapping.Requirement{{Type: "role", Relation: "assignee", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgRole}})

	case "group.member.added":
		return union(typ,
			"Someone joined a group — and \"someone\" is two different things. Auth0 group members are tagged with member_type: a user, or an entire connection. So this rule carries two tuples, each gated on the tag, and exactly one fires.\n\nThe connection branch writes a userset, connection:con_…#identity, rather than a user: it says everyone with an identity in that connection is a member, without a tuple per person. That is the same identity relation user.created writes.\n\nOne thing to check before adopting this: member.id is the member's id as the group knows it, which for a SCIM-provisioned group is often an email rather than the auth0|… user_id every other recipe here uses. If yours differ, pick one key and map to it, or the same person ends up in your store twice.",
			[]mapping.Tuple{
				{User: tmplGroupMem, Relation: "member", Object: tmplGroupID, When: gateMemberUser},
				{User: tmplMemConn, Relation: "member", Object: tmplGroupID, When: gateMemberConn},
			}, true,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent + "\n\n" + dslGroupMember}})

	case "group.member.deleted":
		return union(typ,
			"Someone left a group. This deletes whichever of the two tuples the added event wrote, gated the same way on member_type — a rule that only handled users would leave a whole connection's worth of access behind.",
			[]mapping.Tuple{
				{User: tmplGroupMem, Relation: "member", Object: tmplGroupID, When: gateMemberUser},
				{User: tmplMemConn, Relation: "member", Object: tmplGroupID, When: gateMemberConn},
			}, false,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent + "\n\n" + dslGroupMember}})

	case "organization.connection.added":
		return membership(typ,
			"A connection was associated with an organization. This records which organization a connection belongs to.",
			tmplConnID, "connection", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	case "organization.connection.removed":
		return membership(typ,
			"A connection was dissociated from an organization. This deletes the tuple the added event wrote.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	// --- deletion recipes, which sweep with filters rather than tuples ---

	case "user.deleted":
		return cleanup(typ,
			"A user was deleted, and every tuple naming them has to go. The event cannot name the organizations, roles and connections they belonged to, so this sweeps by user instead: each filter reads every tuple with this user and that object type, then deletes what it finds.\n\nThree filters is mapper's hard limit per rule, and this catalog writes a user into four places. Group membership is the one left out, because it is the one whose key may not be the auth0|… user_id — see group.member.added. Add a second rule with the same when if your groups key on user_id too.",
			[]mapping.TupleFilter{
				{User: tmplUserID, Object: "organization:", Action: "delete"},
				{User: tmplUserID, Object: "role:", Action: "delete"},
				{User: tmplUserID, Object: "connection:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", DSL: dslUser + "\n\n" + dslOrgBare},
				{Type: "role", DSL: dslRoleBare},
				{Type: "connection", DSL: dslConnection},
			})

	case "organization.deleted":
		return cleanup(typ,
			"An organization was deleted. This removes every tuple that names the object, since none of them outlive it.",
			[]mapping.TupleFilter{{Object: "organization:{{ input.data.object.id }}", Action: "delete"}},
			[]mapping.Requirement{{Type: "organization", DSL: dslOrgBare}})

	case "group.deleted":
		return cleanup(typ,
			"A group was deleted. This removes every tuple that names it, as no membership outlives the group.",
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
			"A connection was deleted. Both events that attach one — organization.connection.added and group.created — put the connection on the user side of the tuple, so these filters match on user and sweep both sides it could have been attached to.",
			[]mapping.TupleFilter{
				{User: "connection:{{ input.data.object.id }}", Object: "organization:", Action: "delete"},
				{User: "connection:{{ input.data.object.id }}", Object: "group:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslOrgConn},
				{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslGroupConn},
			})

	case "organization.connection.updated":
		r := membership(typ,
			"An organization's connection settings changed. The one setting that is a relationship is is_enabled: a disabled connection should stop granting anything, so this deletes the tuple when the flag goes false.\n\nThe flags sit on data.object directly, not inside the nested connection — that object carries only an id.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})
		r.Rule.Tuples[0].When = "!input.data.object.is_enabled"
		return r

	// --- one creation event that creates a relationship too ---

	case "group.created":
		return union(typ,
			"A group was created — and where it was created is a three-way choice. Auth0 scopes a group to a connection, to an organization, or to the whole tenant, and tags which with type. Only the first two are relationships, so this rule carries two tuples, each gated on the tag.\n\nThe connection branch is a parent pointer: the group belongs to that connection. The organization branch is the other shape — group:grp_…#member as the *user* of the tuple, which makes everyone in the group a member of the organization in one tuple, and keeps them members as Auth0 adds and removes people.\n\nA tenant-scoped group gets nothing, on purpose: every group is in the tenant, so saying so would be one tuple per group that never answers a question.",
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

	// --- creation events whose relationships are nested in an array ---

	case "user.created":
		return Recipe{
			Explain: "A user was created with one or more identities, and each identity says which connection it came from. This is the only recipe that fans out: the iterator walks input.data.object.identities and writes a tuple per element, so a user with three logins gets three tuples from one rule.\n\nThe user is keyed on the top-level user_id, not on identity.user_id — that one is the provider's own subject, has no auth0| prefix, and the schema allows it to arrive as a number rather than a string.\n\nRead the rendered tuples before adopting this. Auth0 names the connection here, while every other connection recipe in the catalog uses the con_… id. The payload carries no id to use instead, so pick one key for connections and map to it consistently, or the same connection ends up in your store twice.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
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

	case "organization.created":
		return membership(typ,
			"An organization was created. It relates to nothing else yet, so the only relationship available is the one to the tenant it was created in — the pattern you copy for your own resources: a document or a project names the thing it hangs from, and the tuple is written the moment the object exists.\n\nThe tenant comes from a0tenant on the envelope rather than from data.context, which Auth0's schema marks optional on every event type.",
			tmplTenant, "tenant", tmplNewOrg, true,
			[]mapping.Requirement{{Type: "organization", Relation: "tenant", UserTypes: []string{"tenant"},
				DSL: dslTenant + "\n\n" + dslOrgTenant}})

	case "connection.created":
		return Recipe{
			Explain: "A connection was created. Two relationships come out of one rule, which is why this recipe is worth reading twice: the tuple relates the connection to its tenant, and the iterator below walks enabled_clients and writes one tuple per application the connection is turned on for.\n\nenabled_clients is an array of plain strings, not objects, so the iterator's element is the client id itself — {{ client }}, not {{ client.id }}.\n\nAuth0 has since marked enabled_clients deprecated on the connection API. It is still in the event schema, and still the only place the event says which applications a connection serves.",
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
			Explain: "A user's profile changed, and identities is one of the things that can change: linking a " +
				"second login adds an entry, unlinking one takes it away. The iterator walks the new " +
				"list and writes a tuple per identity.\n\nThe filter below makes that list the whole truth rather than an addition to it. A filter " +
				"with no action is a patch: mapper compares the tuples this rule produced against every " +
				"existing tuple the filter matches, writes the ones that are new, and deletes the ones " +
				"the list no longer has. An unlinked identity loses its tuple without a rule of its own.",
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
			Explain: "A group changed, and one of the things that can change is the connection it belongs to. That is a move, not an edit: the old tuple has to go at the same time the new one arrives, or the group ends up in both connections at once.\n\nSo the rule carries two tuples with opposite actions, each gated by its own when. The gates compare against previous_object and also check the group is still connection-scoped, which keeps the rule silent for the renames and description edits the event usually carries — and stops the two tuples colliding when the connection did not move.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
				Tuples: []mapping.Tuple{
					{
						User:     "connection:{{ input.data.previous_object.connection_id }}",
						Relation: "connection",
						Object:   tmplNewGroup,
						Action:   "delete",
						When:     gateScopeConn + " && input.data.object.connection_id != input.data.previous_object.connection_id",
					},
					{
						User:     tmplGroupCon,
						Relation: "connection",
						Object:   tmplNewGroup,
						When:     gateScopeConn + " && input.data.object.connection_id != input.data.previous_object.connection_id",
					},
				},
			},
			Requires: []mapping.Requirement{{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslGroupConn}},
		}

	case "connection.updated":
		return Recipe{
			Explain: "A connection's settings changed, and enabling an application changes enabled_clients. The " +
				"iterator walks the new list and writes a tuple per client.\n\nThe filter below makes that list the whole truth. A filter with no action is a patch: " +
				"mapper compares the tuples this rule produced against every existing tuple the filter " +
				"matches, writes the ones that are new, and deletes the ones the list no longer names. " +
				"Disabling an application removes its tuple, and connection.deleted still sweeps the rest.",
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
		return Recipe{Note: "attributes", Explain: "Nothing to write. This event carries an organization's id, name, display name and branding metadata — every one of them an attribute, not a relationship. It is the only event in the catalog with nothing relational in it.\n\nIt does carry previous_object, so a rule is still possible if your model treats one of those attributes as a relationship. The other three update events do map — user.updated, group.updated and connection.updated — and all work the same way: compare the two versions, write only when the thing you care about moved."}
	}
}
