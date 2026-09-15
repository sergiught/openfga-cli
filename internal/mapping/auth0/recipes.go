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
	Note     string
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

// Shared DSL fragments, so each type is spelled once.
const (
	dslUser        = "type user"
	dslConnection  = "type connection"
	dslOrgMember   = "type organization\n  relations\n    define member: [user]"
	dslOrgRole     = "type organization\n  relations\n    define admin: [user]"
	dslOrgConn     = "type organization\n  relations\n    define connection: [connection]"
	dslGroupMember = "type group\n  relations\n    define member: [user]"
	dslGroupConn   = "type group\n  relations\n    define connection: [connection]"
	dslConnIdent   = "type connection\n  relations\n    define identity: [user]"
	dslConnClient  = "type connection\n  relations\n    define enabled_client: [client]"
	dslOrgTenant   = "type organization\n  relations\n    define tenant: [tenant]"
	dslConnTenant  = "type connection\n  relations\n    define tenant: [tenant]"
	dslClient      = "type client"
	dslTenant      = "type tenant"
	dslOrgBare     = "type organization"
	dslGroupBare   = "type group"
)

// tmplOrgID and friends are the payload paths, verified against the embedded
// samples. Ids are used for identity, never display names: names change.
const (
	tmplOrgID    = "organization:{{ input.data.object.organization.id }}"
	tmplOrgUser  = "user:{{ fga_escape(input.data.object.user.user_id) }}"
	tmplGroupID  = "group:{{ input.data.object.group.id }}"
	tmplGroupMem = "user:{{ fga_escape(input.data.object.member.id) }}"
	tmplConnID   = "connection:{{ input.data.object.connection.id }}"
	// The creation events name their own object at the root of the payload, and
	// a group names its connection with a flat connection_id rather than the
	// nested object the organization events use.
	tmplNewGroup = "group:{{ input.data.object.id }}"
	tmplGroupCon = "connection:{{ input.data.object.connection_id }}"
	tmplNewOrg   = "organization:{{ input.data.object.id }}"
	tmplNewConn  = "connection:{{ input.data.object.id }}"
	// Every payload carries its tenant beside the object rather than inside it.
	// It is escaped because a tenant name is chosen by whoever made it, unlike
	// the generated ids above.
	tmplTenant = "tenant:{{ fga_escape(input.data.context.tenant.id) }}"
)

func when(typ string) string { return `input.type == "` + typ + `"` }

// membership builds one of the ten relationship recipes. write says whether
// the event grants the relationship or removes it: granting needs no action at
// all, and removing sets "delete" once at rule level rather than on the tuple.
// Either level is legal; what mapper rejects is a rule that sets both.
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

// cleanup builds one of the four deletion recipes. A tuple filter's object is
// mandatory — mapper rejects a blank one — while its user and relation are
// optional. So a filter that can name the doomed object names it in full, and
// one that cannot pins the user instead and leaves the object as a bare type
// prefix, which is what "every organization" looks like.
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

func recipeFor(typ string) Recipe {
	switch typ {

	// --- the eight relationship recipes ---

	case "organization.member.added":
		return membership(typ,
			"A user joined an organization. This writes one tuple: that user becomes a member of that organization.",
			tmplOrgUser, "member", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.deleted":
		return membership(typ,
			"A user left an organization. This deletes the same membership tuple the added event would have written.",
			tmplOrgUser, "member", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslOrgMember}})

	case "organization.member.role.assigned":
		return membership(typ,
			"A role assigned inside an organization. The relation comes from the event itself, so this one rule covers every role you define — but your model needs a relation per role name, and the admin relation below is only an example of the shape.",
			tmplOrgUser, "{{ input.data.object.role.name }}", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", DSL: dslUser + "\n\n" + dslOrgRole}})

	case "organization.member.role.deleted":
		return membership(typ,
			"A role taken away inside an organization. This deletes the tuple matching the assigned event that wrote it. The relation comes from the event, so your model needs a relation per role name, and the admin relation below is only an example of the shape.",
			tmplOrgUser, "{{ input.data.object.role.name }}", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", DSL: dslUser + "\n\n" + dslOrgRole}})

	case "group.member.added":
		return membership(typ,
			"Someone joined a group. The member id here is an email address, so it is escaped before it becomes part of a tuple.",
			tmplGroupMem, "member", tmplGroupID, true,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslGroupMember}})

	case "group.member.deleted":
		return membership(typ,
			"Someone left a group. This deletes the membership tuple matching the added event that wrote it.",
			tmplGroupMem, "member", tmplGroupID, false,
			[]mapping.Requirement{{Type: "group", Relation: "member", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslGroupMember}})

	case "organization.connection.added":
		return membership(typ,
			"A connection was associated with an organization. This records which organization the connection belongs to.",
			tmplConnID, "connection", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	case "organization.connection.removed":
		return membership(typ,
			"A connection was dissociated from an organization. This deletes the tuple the added event would have written.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	// --- the four deletion recipes ---

	case "user.deleted":
		return cleanup(typ,
			"A user was deleted. The two filters use different identifiers on purpose: the organization events identify a member by user_id, the group events by email address, so a single filter could not match both. No one object id names every organization and group, which is why these are filters and not tuples.",
			[]mapping.TupleFilter{
				{User: "user:{{ fga_escape(input.data.object.user_id) }}", Object: "organization:", Action: "delete"},
				{User: "user:{{ fga_escape(input.data.object.email) }}", Object: "group:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", DSL: dslUser + "\n\n" + dslOrgBare},
				{Type: "group", DSL: dslGroupBare},
			})

	case "organization.deleted":
		return cleanup(typ,
			"An organization was deleted. This removes every tuple that names it as the object, since none of them can outlive it.",
			[]mapping.TupleFilter{{Object: "organization:{{ input.data.object.id }}", Action: "delete"}},
			[]mapping.Requirement{{Type: "organization", DSL: dslOrgBare}})

	case "group.deleted":
		return cleanup(typ,
			"A group was deleted. This removes every tuple that names it, so no membership outlives the group.",
			[]mapping.TupleFilter{{Object: "group:{{ input.data.object.id }}", Action: "delete"}},
			[]mapping.Requirement{{Type: "group", DSL: dslGroupBare}})

	case "connection.deleted":
		return cleanup(typ,
			"A connection was deleted. Both events that attach one — organization.connection.added and group.created — put the connection on the user side of the tuple, so these filters match on the user and sweep both sides it could have been attached to.",
			[]mapping.TupleFilter{
				{User: "connection:{{ input.data.object.id }}", Object: "organization:", Action: "delete"},
				{User: "connection:{{ input.data.object.id }}", Object: "group:", Action: "delete"},
			},
			[]mapping.Requirement{
				{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslConnection + "\n\n" + dslOrgConn},
				{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
					DSL: dslGroupConn},
			})

	// --- the settings event that turns out to carry one relationship ---
	case "organization.connection.updated":
		r := membership(typ,
			"Most of this event is attributes, but one field is a relationship: is_enabled. A tenant that disables a connection rather than removing it fires this event and no other, so without this rule the organization→connection tuple outlives the access it stands for. The when clause is what keeps the rule honest — the event also fires for every cosmetic change, and this one only matches the disabling.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})
		r.Rule.When = when(typ) + " && !input.data.object.is_enabled"
		return r

	// --- the one creation event that creates a relationship too ---
	case "group.created":
		return membership(typ,
			"A group was created inside a connection. Most creation events relate nothing, but this one carries connection_id: the group belongs to that connection from the moment it exists, so there is a tuple to write straight away.",
			tmplGroupCon, "connection", tmplNewGroup, true,
			[]mapping.Requirement{{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslGroupConn}})

	// --- the creation event whose relationships are nested in an array ---
	case "user.created":
		return Recipe{
			Explain: "A user was created with one or more identities, and each identity says which connection it came from. This is the only recipe that fans out: an iterator walks input.data.object.identities and writes a tuple per element, so a user with three logins gets three tuples from one rule.\n\nRead the rendered tuples before adopting this. Auth0 names the connection here, while every other connection recipe in this catalog uses its con_… id — pick one key for connection and keep to it, or the same connection ends up in your store twice. And identity.user_id is the provider's id, which is the root user_id without its provider| prefix: if your model keys users the way the organization recipes do, use input.data.object.user_id instead.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
				Iterator: &mapping.Iterator{
					Source: "input.data.object.identities",
					As:     "identity",
					Tuples: []mapping.Tuple{{
						User:     "user:{{ fga_escape(identity.user_id) }}",
						Relation: "identity",
						Object:   "connection:{{ fga_escape(identity.connection) }}",
					}},
				},
			},
			Requires: []mapping.Requirement{{Type: "connection", Relation: "identity", UserTypes: []string{"user"},
				DSL: dslUser + "\n\n" + dslConnIdent}},
		}

	// --- the creation events, which relate their new object to the tenant ---

	case "organization.created":
		return membership(typ,
			"An organization was created. Nothing else in the payload is a relationship — but the tenant beside it is. Every Auth0 event carries data.context.tenant.id, and relating a new object to its tenant is what stops it floating free until someone happens to add a member.\n\nThis is the pattern to copy for your own resources: whatever a document or project hangs from, write that tuple the moment the object exists.",
			tmplTenant, "tenant", tmplNewOrg, true,
			[]mapping.Requirement{{Type: "organization", Relation: "tenant", UserTypes: []string{"tenant"},
				DSL: dslTenant + "\n\n" + dslOrgTenant}})

	case "connection.created":
		return Recipe{
			Explain: "A connection was created. Two relationships in one rule, which is why this recipe is worth reading twice: the tuple relates the connection to its tenant, and the iterator below it walks enabled_clients and writes one tuple per application the connection is turned on for.\n\nenabled_clients is an array of plain strings, not objects, so the iterator's element is the client id itself — {{ client }} rather than {{ client.id }}.",
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

	// --- the update events that move a relationship rather than an attribute ---

	case "user.updated":
		return Recipe{
			Explain: "A user's profile changed, and identities is one of the things that can change: linking a second login adds an entry. The rule re-writes the whole current list rather than working out what is new, because writing a tuple that already exists is free and getting the difference right is not.\n\nIt is gated on identities actually having changed, so the rule stays quiet for the profile edits — a new phone number, a changed nickname — that make up most of this event.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ) + " && input.data.object.identities != input.data.previous_object.identities",
				Iterator: &mapping.Iterator{
					Source: "input.data.object.identities",
					As:     "identity",
					Tuples: []mapping.Tuple{{
						User:     "user:{{ fga_escape(identity.user_id) }}",
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
			Explain: "A group changed, and one of the things that can change is which connection it belongs to. That is a move, not an edit: the old tuple has to go at the same time the new one arrives, or the group ends up in both connections at once.\n\nSo this rule carries two tuples with opposite actions, each gated by its own when. Both gates are the same comparison against previous_object, which is what keeps the rule silent for the renames and description edits this event usually carries — and what stops the two tuples colliding when the connection did not move.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ),
				Tuples: []mapping.Tuple{
					{
						User:     "connection:{{ input.data.previous_object.connection_id }}",
						Relation: "connection",
						Object:   tmplNewGroup,
						Action:   "delete",
						When:     "input.data.object.connection_id != input.data.previous_object.connection_id",
					},
					{
						User:     tmplGroupCon,
						Relation: "connection",
						Object:   tmplNewGroup,
						When:     "input.data.object.connection_id != input.data.previous_object.connection_id",
					},
				},
			},
			Requires: []mapping.Requirement{{Type: "group", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslGroupConn}},
		}

	case "connection.updated":
		return Recipe{
			Explain: "A connection's settings changed, and enabling it for another application changes enabled_clients. The iterator re-writes the current list, gated on that list having changed.\n\nRead the limit before adopting it: this adds, it does not remove. One rule walks one array, so an application taken off the list keeps its tuple. If that matters, pair this with a rule keyed on the same event that deletes what previous_object holds — or accept that removals arrive with connection.deleted.",
			Rule: mapping.Rule{
				Name: typ,
				When: when(typ) + " && input.data.object.enabled_clients != input.data.previous_object.enabled_clients",
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
	// organization.updated is the whole of this branch. Every other event in the
	// catalog turned out to carry a relationship somewhere — often beside the
	// object rather than inside it, as with the tenant — and this one genuinely
	// does not: an id, a name, a display name, some branding colours.
	//
	// It is stated as this event's own fact rather than as a law about update
	// events, because three update events two cases up map perfectly well.
	default:
		return Recipe{Note: "attributes", Explain: "Nothing to write. This event carries an organization's id, name, display name, branding and metadata — and an attribute is not a relationship. It is the only event in this catalog with nothing relational in it.\n\nIt does carry a previous_object, so a rule here is still possible if your model treats one of those attributes as a relationship. The three update events that do map — user.updated, group.updated and connection.updated — all work that way: compare the two versions, and write only when the thing you care about moved."}
	}
}
