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
	Explain  string
	Rule     mapping.Rule
	Requires []mapping.Requirement
}

// Maps reports whether this recipe produces anything. It is the single test the
// recipe screen branches on, so the two empty classes cannot drift apart from
// the events that belong to them.
func (r Recipe) Maps() bool {
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
)

func when(typ string) string { return `input.type == "` + typ + `"` }

// membership builds one of the eight relationship recipes. write says whether
// the event grants the relationship or removes it; the action is set at rule
// level because mapper rejects a rule that sets both levels.
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
// mandatory — mapper rejects a blank one — so each filter names at least a type
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
			"A role assigned inside an organization. The relation comes from the event itself, so one rule covers every role you define.",
			tmplOrgUser, "{{ input.data.object.role.name }}", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", DSL: dslUser + "\n\n" + dslOrgRole}})

	case "organization.member.role.deleted":
		return membership(typ,
			"A role taken away inside an organization. This deletes the tuple matching the assigned event that wrote it.",
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
			"A connection was enabled for an organization. This records which organization the connection belongs to.",
			tmplConnID, "connection", tmplOrgID, true,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	case "organization.connection.removed":
		return membership(typ,
			"A connection was disabled for an organization. This deletes the tuple the added event would have written.",
			tmplConnID, "connection", tmplOrgID, false,
			[]mapping.Requirement{{Type: "organization", Relation: "connection", UserTypes: []string{"connection"},
				DSL: dslConnection + "\n\n" + dslOrgConn}})

	// --- the four deletion recipes ---

	case "user.deleted":
		return cleanup(typ,
			"A user was deleted. Tuple filters remove everything that user was related to, in every organization and every group, since no single object id names all of them.",
			[]mapping.TupleFilter{
				{User: "user:{{ fga_escape(input.data.object.user_id) }}", Object: "organization:", Action: "delete"},
				{User: "user:{{ fga_escape(input.data.object.user_id) }}", Object: "group:", Action: "delete"},
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
			"A connection was deleted. This removes every tuple pointing at it.",
			[]mapping.TupleFilter{{Object: "connection:{{ input.data.object.id }}", Action: "delete"}},
			[]mapping.Requirement{{Type: "connection", DSL: dslConnection}})

	// --- no mapping: the four creation events ---
	case "user.created", "organization.created", "group.created", "connection.created":
		return Recipe{Explain: "Nothing to write yet. FGA stores relationships, not objects — a new object needs a tuple only once it is related to something. That happens in the membership events, not this one."}

	// --- no mapping: the five update events ---
	default:
		return Recipe{Explain: "No relationship changed. This event carries a previous_object so you can compare attributes, but attributes are not relationships."}
	}
}
